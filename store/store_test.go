package store

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/joho/godotenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"main/db"
)

var (
	initOnce sync.Once
	initErr  error
	seq      int64
)

func testPool(t *testing.T) {
	t.Helper()
	_ = godotenv.Load("../.env")
	if os.Getenv("DATABASE_URL") == "" {
		t.Skip("DATABASE_URL not set")
	}
	initOnce.Do(func() { initErr = db.InitPool() })
	require.NoError(t, initErr)
}

func beginTx(t *testing.T) pgx.Tx {
	t.Helper()
	testPool(t)
	tx, err := db.Pool().Begin(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	return tx
}

func insertUser(t *testing.T, tx pgx.Tx, level int, chatID *int64) string {
	t.Helper()
	seq++
	n := fmt.Sprintf("%d-%d", time.Now().UnixNano(), seq)
	var id string
	var username *string
	var boundAt *time.Time
	if chatID != nil {
		u := "tester"
		now := time.Now()
		username, boundAt = &u, &now
	}
	err := tx.QueryRow(context.Background(),
		`INSERT INTO users (google_sub, email, name, member_level, telegram_chat_id, telegram_username, telegram_bound_at)
		 VALUES ($1, $2, 'T', $3, $4, $5, $6) RETURNING id::text`,
		"sub-"+n, "store-test-"+n+"@example.invalid", level, chatID, username, boundAt).Scan(&id)
	require.NoError(t, err)
	return id
}

func insertSetting(t *testing.T, tx pgx.Tx, userID, symbol string, enabled bool, created time.Time, conditionsJSON string) {
	t.Helper()
	_, err := tx.Exec(context.Background(),
		`INSERT INTO user_stock_signals (user_id, symbol, enabled, conditions, created_time, updated_time)
		 VALUES ($1, $2, $3, $4::jsonb, $5, $5)`, userID, symbol, enabled, conditionsJSON, created)
	require.NoError(t, err)
}

func symbols(t *testing.T, tx pgx.Tx, n int) []string {
	t.Helper()
	rows, err := tx.Query(context.Background(), `SELECT symbol FROM stocks WHERE enabled ORDER BY symbol LIMIT $1`, n)
	require.NoError(t, err)
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		require.NoError(t, rows.Scan(&s))
		out = append(out, s)
	}
	require.Len(t, out, n)
	return out
}

const condJSON = `[{"id":"c1","side":"long","kind":"rsi","op":"gt","value":50,"period":14}]`

func TestLoadSettings_RankAndFilter(t *testing.T) {
	tx := beginTx(t)
	ctx := context.Background()
	s := symbols(t, tx, 4)
	chat := int64(987654321)
	a := insertUser(t, tx, 0, &chat)
	b := insertUser(t, tx, 0, nil)
	base := time.Now().Add(-time.Hour)
	insertSetting(t, tx, a, s[0], true, base, condJSON)
	insertSetting(t, tx, a, s[1], false, base.Add(time.Minute), condJSON)
	insertSetting(t, tx, a, s[2], true, base.Add(2*time.Minute), condJSON)
	insertSetting(t, tx, b, s[3], true, base, condJSON)

	got, err := New(tx).LoadSettings(ctx)
	require.NoError(t, err)
	var mine []Setting
	for _, st := range got {
		assert.NotEqual(t, b, st.UserID)
		if st.UserID == a {
			mine = append(mine, st)
		}
	}
	require.Len(t, mine, 2)
	assert.Equal(t, s[0], mine[0].Symbol)
	assert.Equal(t, 1, mine[0].Rank)
	assert.Equal(t, s[2], mine[1].Symbol)
	assert.Equal(t, 3, mine[1].Rank)
	assert.Equal(t, chat, mine[0].ChatID)
	require.Len(t, mine[0].Conditions, 1)
	assert.Equal(t, "rsi", mine[0].Conditions[0].Kind)
	assert.Equal(t, 14, *mine[0].Conditions[0].Period)
}

func TestLoadSettings_SkipsBadConditions(t *testing.T) {
	tx := beginTx(t)
	s := symbols(t, tx, 1)
	chat := int64(987654322)
	a := insertUser(t, tx, 0, &chat)
	insertSetting(t, tx, a, s[0], true, time.Now(), `{"x":1}`)
	got, err := New(tx).LoadSettings(context.Background())
	require.NoError(t, err)
	for _, st := range got {
		assert.NotEqual(t, a, st.UserID)
	}
}

func TestLoadLimits(t *testing.T) {
	tx := beginTx(t)
	m, err := New(tx).LoadLimits(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 5, m[0])
	assert.Equal(t, 20, m[1])
}

// indicatorDate 回 indicators 與 statistics 都有資料的最近交易日（DEV 的 raw 可能領先 indicators）
func indicatorDate(t *testing.T, tx pgx.Tx) time.Time {
	t.Helper()
	var d time.Time
	require.NoError(t, tx.QueryRow(context.Background(), `
		SELECT max(i.trade_date) FROM stock_daily_indicators i
		WHERE EXISTS (SELECT 1 FROM stock_daily_statistics s WHERE s.trade_date = i.trade_date)`).Scan(&d))
	return d
}

func fp(v *float64) any {
	if v == nil {
		return nil
	}
	return *v
}

func TestLoadBars_MatchesDB(t *testing.T) {
	tx := beginTx(t)
	ctx := context.Background()
	st := New(tx)
	d := indicatorDate(t, tx)

	var sym string
	var wantClose, wantVol, wantRSI, wantK, wantMA20, wantF10 *float64
	err := tx.QueryRow(ctx, `
		SELECT r.symbol, r.close::float8, r.volume::float8, i.rsi_14::float8, i.kd_k::float8, s.ma20::float8, s.foreign_net_10d_avg::float8
		FROM stock_daily_raw r
		JOIN stock_daily_statistics s ON s.symbol = r.symbol AND s.trade_date = r.trade_date
		JOIN stock_daily_indicators i ON i.symbol = r.symbol AND i.trade_date = r.trade_date
		WHERE r.trade_date = $1 AND r.close IS NOT NULL AND r.symbol <> 'ZZ9999'
		  AND EXISTS (SELECT 1 FROM stock_daily_indicators pi
		      WHERE pi.symbol = r.symbol AND pi.kd_k IS NOT NULL AND pi.kd_d IS NOT NULL
		        AND pi.trade_date = (SELECT max(x.trade_date) FROM stock_daily_raw x
		            WHERE x.symbol = r.symbol AND x.trade_date < $1 AND x.trade_date >= $1::date - INTERVAL '1 year'))
		ORDER BY r.symbol LIMIT 1`, d).Scan(&sym, &wantClose, &wantVol, &wantRSI, &wantK, &wantMA20, &wantF10)
	require.NoError(t, err)

	var wantPrev, wantPK, wantPD *float64
	err = tx.QueryRow(ctx, `
		SELECT r.close::float8, i.kd_k::float8, i.kd_d::float8
		FROM stock_daily_raw r
		LEFT JOIN stock_daily_indicators i ON i.symbol = r.symbol AND i.trade_date = r.trade_date
		WHERE r.symbol = $1 AND r.trade_date < $2 AND r.trade_date >= $2::date - INTERVAL '1 year'
		ORDER BY r.trade_date DESC LIMIT 1`, sym, d).Scan(&wantPrev, &wantPK, &wantPD)
	require.NoError(t, err)
	require.NotNil(t, wantPK)
	require.NotNil(t, wantPD)

	today, prev, err := st.LoadBars(ctx, []string{sym}, d)
	require.NoError(t, err)
	b, ok := today[sym]
	require.True(t, ok)
	assert.Equal(t, sym, b.Symbol)
	assert.Equal(t, fp(wantClose), fp(b.Close))
	assert.Equal(t, fp(wantVol), fp(b.Volume))
	assert.Equal(t, fp(wantK), fp(b.KDK))
	if wantRSI != nil {
		assert.Equal(t, *wantRSI, b.RSI[14])
	}
	if wantMA20 != nil {
		assert.Equal(t, *wantMA20, b.MA[20])
	}
	if wantF10 != nil {
		assert.Equal(t, *wantF10, b.ForeignAvg[10])
	}
	p, ok := prev[sym]
	require.True(t, ok)
	assert.Equal(t, fp(wantPrev), fp(p.Close))
	assert.Equal(t, fp(wantPK), fp(p.KDK))
	assert.Equal(t, fp(wantPD), fp(p.KDD))
}

func TestLoadBars_NullsAbsent(t *testing.T) {
	tx := beginTx(t)
	ctx := context.Background()
	st := New(tx)
	d, err := st.LatestTradeDate(ctx)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `INSERT INTO stock_daily_raw (symbol, trade_date, close) VALUES ('ZZ9999', $1, 10)`, d)
	require.NoError(t, err)

	today, prev, err := st.LoadBars(ctx, []string{"ZZ9999"}, d)
	require.NoError(t, err)
	b := today["ZZ9999"]
	require.NotNil(t, b.Close)
	assert.Equal(t, 10.0, *b.Close)
	assert.Nil(t, b.Volume)
	assert.Equal(t, 0, len(b.RSI))
	assert.Nil(t, b.KDK)
	assert.Equal(t, "", b.Name)
	_, has := prev["ZZ9999"]
	assert.False(t, has)
}

func TestHasIndicators(t *testing.T) {
	tx := beginTx(t)
	ctx := context.Background()
	st := New(tx)
	latest, err := st.LatestTradeDate(ctx)
	require.NoError(t, err)
	assert.False(t, latest.IsZero())
	ind := indicatorDate(t, tx)
	ok, err := st.HasIndicators(ctx, ind)
	require.NoError(t, err)
	assert.True(t, ok)
	if !latest.Equal(ind) {
		// 半套資料：raw 已有、indicators / statistics 還沒有
		ok, err = st.HasIndicators(ctx, latest)
		require.NoError(t, err)
		assert.False(t, ok)
	}
	ok, err = st.HasIndicators(ctx, time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestRecordSentAndLoadSentKeys(t *testing.T) {
	tx := beginTx(t)
	ctx := context.Background()
	st := New(tx)
	s := symbols(t, tx, 1)
	chat := int64(987654323)
	u := insertUser(t, tx, 0, &chat)
	d := time.Date(2099, 1, 2, 0, 0, 0, 0, time.UTC)

	require.NoError(t, st.RecordSent(ctx, u, s[0], d, []string{"long", "short"}, "msg"))
	keys, err := st.LoadSentKeys(ctx, d)
	require.NoError(t, err)
	assert.True(t, keys[SentKey{u, s[0], "long"}])
	assert.True(t, keys[SentKey{u, s[0], "short"}])
	require.NoError(t, st.RecordSent(ctx, u, s[0], d, []string{"long", "short"}, "msg"))
}

func TestClearTelegram(t *testing.T) {
	tx := beginTx(t)
	ctx := context.Background()
	chat := int64(987654324)
	u := insertUser(t, tx, 0, &chat)
	require.NoError(t, New(tx).ClearTelegram(ctx, u))
	var c *int64
	var un *string
	var at *time.Time
	require.NoError(t, tx.QueryRow(ctx, `SELECT telegram_chat_id, telegram_username, telegram_bound_at FROM users WHERE id = $1`, u).Scan(&c, &un, &at))
	assert.Nil(t, c)
	assert.Nil(t, un)
	assert.Nil(t, at)
}

func TestTryLock(t *testing.T) {
	testPool(t)
	ctx := context.Background()
	rel, ok, err := TryLock(ctx, db.Pool())
	require.NoError(t, err)
	require.True(t, ok)
	rel2, ok2, err := TryLock(ctx, db.Pool())
	require.NoError(t, err)
	assert.False(t, ok2)
	rel2()
	rel()
	rel3, ok3, err := TryLock(ctx, db.Pool())
	require.NoError(t, err)
	assert.True(t, ok3)
	rel3()
}
