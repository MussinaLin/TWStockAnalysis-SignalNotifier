package notifier

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"main/signals"
	"main/store"
	"main/telegram"
)

var testDate = time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)

type recorded struct {
	UserID, Symbol string
	Sides          []string
	Message        string
}

type fakeStore struct {
	ready       bool
	limits      map[int]int
	settings    []store.Setting
	bars        map[string]signals.Bar
	prev        map[string]signals.PrevBar
	sent        map[store.SentKey]bool
	settingsErr error

	recorded []recorded
	cleared  []string
	barSyms  []string
}

func (f *fakeStore) LatestTradeDate(ctx context.Context) (time.Time, error) { return testDate, nil }
func (f *fakeStore) HasIndicators(ctx context.Context, d time.Time) (bool, error) {
	return f.ready, nil
}
func (f *fakeStore) LoadLimits(ctx context.Context) (map[int]int, error) { return f.limits, nil }
func (f *fakeStore) LoadSettings(ctx context.Context) ([]store.Setting, error) {
	return f.settings, f.settingsErr
}
func (f *fakeStore) LoadBars(ctx context.Context, symbols []string, d time.Time) (map[string]signals.Bar, map[string]signals.PrevBar, error) {
	f.barSyms = symbols
	return f.bars, f.prev, nil
}
func (f *fakeStore) LoadSentKeys(ctx context.Context, d time.Time) (map[store.SentKey]bool, error) {
	return f.sent, nil
}
func (f *fakeStore) RecordSent(ctx context.Context, userID, symbol string, d time.Time, sides []string, message string) error {
	f.recorded = append(f.recorded, recorded{userID, symbol, sides, message})
	return nil
}
func (f *fakeStore) ClearTelegram(ctx context.Context, userID string) error {
	f.cleared = append(f.cleared, userID)
	return nil
}

type sendCall struct{ ChatID, Text string }

type fakeSender struct {
	errs  map[string][]error // chatID → 依序回傳的錯誤，用完回 nil
	calls []sendCall
}

func (f *fakeSender) Send(ctx context.Context, chatID, text string) error {
	f.calls = append(f.calls, sendCall{chatID, text})
	if q := f.errs[chatID]; len(q) > 0 {
		f.errs[chatID] = q[1:]
		return q[0]
	}
	return nil
}

type fakeAdmin struct{ msgs []string }

func (f *fakeAdmin) Notify(ctx context.Context, text string) error {
	f.msgs = append(f.msgs, text)
	return nil
}

type harness struct {
	st       *fakeStore
	sender   *fakeSender
	admin    *fakeAdmin
	throttle int
	sleeps   []time.Duration
}

func (h *harness) deps() Deps {
	d := Deps{
		Store:  h.st,
		Sender: h.sender,
		Throttle: func(ctx context.Context) error {
			h.throttle++
			return nil
		},
		Sleep: func(ctx context.Context, d time.Duration) error {
			h.sleeps = append(h.sleeps, d)
			return nil
		},
		SiteBaseURL: "https://example.com",
	}
	if h.admin != nil {
		d.Admin = h.admin
	}
	return d
}

func f64(v float64) *float64 { return &v }

func cond(id, side, op string, v float64) signals.Condition {
	return signals.Condition{ID: id, Side: side, Kind: "kd_k", Op: op, Value: f64(v)}
}

// longOK: K=50 > 10；shortOK: K=50 < 90；shortNo: K=50 < 10 不成立
func longOK() signals.Condition  { return cond("l", "long", "gt", 10) }
func shortOK() signals.Condition { return cond("s", "short", "lt", 90) }

func newHarness(settings ...store.Setting) *harness {
	bars := map[string]signals.Bar{}
	for _, s := range settings {
		bars[s.Symbol] = signals.Bar{Symbol: s.Symbol, Name: "名" + s.Symbol, Close: f64(100), KDK: f64(50)}
	}
	return &harness{
		st: &fakeStore{
			ready:    true,
			limits:   map[int]int{0: 5},
			settings: settings,
			bars:     bars,
			prev:     map[string]signals.PrevBar{},
			sent:     map[store.SentKey]bool{},
		},
		sender: &fakeSender{errs: map[string][]error{}},
		admin:  &fakeAdmin{},
	}
}

func setting(user string, chat int64, symbol string, rank int, conds ...signals.Condition) store.Setting {
	return store.Setting{UserID: user, ChatID: chat, MemberLevel: 0, Symbol: symbol, Rank: rank, Conditions: conds}
}

func TestRun_SendsMatchedSide(t *testing.T) {
	s := setting("u1", 111, "2330", 1, longOK(), shortOK(), cond("x", "short", "lt", 10))
	// short 有一個不成立 → 只有 long
	h := newHarness(s)
	sum, err := Run(context.Background(), h.deps())
	require.NoError(t, err)
	require.Len(t, h.sender.calls, 1)
	assert.Equal(t, "111", h.sender.calls[0].ChatID)
	bar := h.st.bars["2330"]
	want := signals.FormatMessage(bar, nil, testDate, []signals.Hit{{Side: "long", Conditions: []signals.Condition{longOK()}}}, "https://example.com")
	assert.Equal(t, want, h.sender.calls[0].Text)
	require.Len(t, h.st.recorded, 1)
	assert.Equal(t, []string{"long"}, h.st.recorded[0].Sides)
	assert.Equal(t, want, h.st.recorded[0].Message)
	assert.Equal(t, 1, sum.Sent)
}

func TestRun_BothSidesOneMessage(t *testing.T) {
	h := newHarness(setting("u1", 111, "2330", 1, longOK(), shortOK()))
	sum, err := Run(context.Background(), h.deps())
	require.NoError(t, err)
	require.Len(t, h.sender.calls, 1)
	require.Len(t, h.st.recorded, 1)
	assert.Equal(t, []string{"long", "short"}, h.st.recorded[0].Sides)
	assert.Equal(t, 1, sum.Sent)
}

func TestRun_SkipsAlreadySent(t *testing.T) {
	h := newHarness(setting("u1", 111, "2330", 1, longOK()))
	h.st.sent[store.SentKey{UserID: "u1", Symbol: "2330", Side: "long"}] = true
	_, err := Run(context.Background(), h.deps())
	require.NoError(t, err)
	assert.Empty(t, h.sender.calls)

	h = newHarness(setting("u1", 111, "2330", 1, longOK(), shortOK()))
	h.st.sent[store.SentKey{UserID: "u1", Symbol: "2330", Side: "long"}] = true
	_, err = Run(context.Background(), h.deps())
	require.NoError(t, err)
	require.Len(t, h.sender.calls, 1)
	assert.True(t, strings.Contains(h.sender.calls[0].Text, "📉 名2330 2330｜空方訊號"), h.sender.calls[0].Text)
	assert.Equal(t, []string{"short"}, h.st.recorded[0].Sides)
}

func TestRun_InactiveAndMissingLevel(t *testing.T) {
	a1 := setting("A", 1, "2330", 1, longOK())
	a2 := setting("A", 1, "2317", 2, longOK())
	b := setting("B", 2, "2454", 1, longOK())
	b.MemberLevel = 3
	b2 := setting("B", 2, "2412", 2, longOK())
	b2.MemberLevel = 3
	h := newHarness(a1, a2, b, b2)
	h.st.limits = map[int]int{0: 1}
	sum, err := Run(context.Background(), h.deps())
	require.NoError(t, err)
	require.Len(t, h.sender.calls, 1)
	assert.Equal(t, "1", h.sender.calls[0].ChatID)
	assert.Equal(t, 1, sum.MissingLevel)
	assert.Equal(t, []string{"2330"}, h.st.barSyms)
}

func TestRun_NoBarSkips(t *testing.T) {
	h := newHarness(setting("u1", 111, "2330", 1, longOK()))
	h.st.bars = map[string]signals.Bar{}
	sum, err := Run(context.Background(), h.deps())
	require.NoError(t, err)
	assert.Empty(t, h.sender.calls)
	assert.Equal(t, 0, sum.Sent+sum.Failed)
}

func TestRun_BlockedUserSkipsRest(t *testing.T) {
	h := newHarness(
		setting("A", 1, "2330", 1, longOK()),
		setting("A", 1, "2317", 2, longOK()),
		setting("B", 2, "2454", 1, longOK()),
	)
	h.sender.errs["1"] = []error{&telegram.APIError{StatusCode: 403, Description: "Forbidden: bot was blocked by the user"}}
	sum, err := Run(context.Background(), h.deps())
	require.NoError(t, err)
	assert.Equal(t, []string{"A"}, h.st.cleared)
	require.Len(t, h.sender.calls, 2)
	assert.Equal(t, "1", h.sender.calls[0].ChatID)
	assert.Equal(t, "2", h.sender.calls[1].ChatID)
	assert.Equal(t, 1, sum.Unbound)
	assert.Equal(t, 0, sum.Failed)
	assert.Equal(t, 1, sum.Sent)
}

func TestRun_RetryAfter429(t *testing.T) {
	e429 := &telegram.APIError{StatusCode: 429, Description: "Too Many Requests", RetryAfter: 3}
	h := newHarness(setting("A", 1, "2330", 1, longOK()))
	h.sender.errs["1"] = []error{e429}
	sum, err := Run(context.Background(), h.deps())
	require.NoError(t, err)
	assert.Equal(t, []time.Duration{3 * time.Second}, h.sleeps)
	assert.Equal(t, 2, h.throttle)
	assert.Equal(t, 1, sum.Sent)

	h = newHarness(setting("A", 1, "2330", 1, longOK()))
	h.sender.errs["1"] = []error{e429, e429}
	sum, err = Run(context.Background(), h.deps())
	require.NoError(t, err)
	assert.Equal(t, 1, sum.Failed)
	assert.Equal(t, 0, sum.Sent)
	assert.Empty(t, h.st.recorded)
}

func TestRun_ContextErrorsAbort(t *testing.T) {
	e429 := &telegram.APIError{StatusCode: 429, RetryAfter: 1}
	h := newHarness(setting("A", 1, "2330", 1, longOK()))
	h.sender.errs["1"] = []error{e429}
	d := h.deps()
	d.Sleep = func(ctx context.Context, _ time.Duration) error { return context.Canceled }
	_, err := Run(context.Background(), d)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestRun_OtherErrorNotRecorded(t *testing.T) {
	h := newHarness(setting("A", 1, "2330", 1, longOK()))
	h.sender.errs["1"] = []error{&telegram.APIError{StatusCode: 500, Description: "boom"}}
	sum, err := Run(context.Background(), h.deps())
	require.NoError(t, err)
	assert.Equal(t, 1, sum.Failed)
	assert.Empty(t, h.st.recorded)
}

func TestRun_IndicatorsNotReady(t *testing.T) {
	h := newHarness(setting("A", 1, "2330", 1, longOK()))
	h.st.ready = false
	_, err := Run(context.Background(), h.deps())
	assert.True(t, errors.Is(err, ErrIndicatorsNotReady))
	assert.Equal(t, []string{"自訂訊號 11/09：指標尚未算完，未發送"}, h.admin.msgs)
	assert.Empty(t, h.sender.calls)
}

func TestRun_AdminSummary(t *testing.T) {
	h := newHarness(setting("A", 1, "2330", 1, longOK()))
	_, err := Run(context.Background(), h.deps())
	require.NoError(t, err)
	assert.Equal(t, []string{"自訂訊號 11/09：成功 1 則、失敗 0 則、解除綁定 0 人"}, h.admin.msgs)

	b := setting("B", 2, "2454", 1, longOK())
	b.MemberLevel = 9
	h = newHarness(setting("A", 1, "2330", 1, longOK()), b)
	_, err = Run(context.Background(), h.deps())
	require.NoError(t, err)
	assert.Equal(t, []string{"自訂訊號 11/09：成功 1 則、失敗 0 則、解除綁定 0 人、設定缺漏 1 人"}, h.admin.msgs)

	h = newHarness(setting("A", 1, "2330", 1, longOK()))
	h.admin = nil
	assert.NotPanics(t, func() { _, _ = Run(context.Background(), h.deps()) })
	h.st.ready = false
	assert.NotPanics(t, func() { _, _ = Run(context.Background(), h.deps()) })
}

func TestRun_StoreErrorAborts(t *testing.T) {
	h := newHarness(setting("A", 1, "2330", 1, longOK()))
	h.st.settingsErr = errors.New("db down")
	_, err := Run(context.Background(), h.deps())
	require.Error(t, err)
	assert.Empty(t, h.sender.calls)
	assert.Empty(t, h.admin.msgs)
}
