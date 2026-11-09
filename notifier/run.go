// Package notifier 是每日自訂訊號通知的主流程。
package notifier

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"main/signals"
	"main/store"
	"main/telegram"
)

type Store interface { // *store.Store 符合
	LatestTradeDate(ctx context.Context) (time.Time, error)
	HasIndicators(ctx context.Context, d time.Time) (bool, error)
	LoadLimits(ctx context.Context) (map[int]int, error)
	LoadSettings(ctx context.Context) ([]store.Setting, error)
	LoadBars(ctx context.Context, symbols []string, d time.Time) (map[string]signals.Bar, map[string]signals.PrevBar, error)
	LoadSentKeys(ctx context.Context, d time.Time) (map[store.SentKey]bool, error)
	RecordSent(ctx context.Context, userID, symbol string, d time.Time, sides []string, message string) error
	ClearTelegram(ctx context.Context, userID string, chatID int64) error
}

type Sender interface {
	Send(ctx context.Context, chatID, text string) error // telegram.Client 符合
}

type AdminNotifier interface {
	Notify(ctx context.Context, text string) error
}

type Deps struct {
	Store       Store
	Sender      Sender
	Admin       AdminNotifier                                    // nil → 不發摘要與告警
	Throttle    func(ctx context.Context) error                  // 每次呼叫 Sender.Send 之前（含重試）
	Sleep       func(ctx context.Context, d time.Duration) error // 429 等待用
	SiteBaseURL string
}

type Summary struct {
	TradeDate                           time.Time
	Sent, Failed, Unbound, MissingLevel int
}

var ErrIndicatorsNotReady = errors.New("indicators not ready")

// SummaryText 組出給管理者的每日摘要
func SummaryText(s Summary) string {
	text := fmt.Sprintf("自訂訊號 %s：成功 %d 則、失敗 %d 則、解除綁定 %d 人",
		s.TradeDate.Format("01/02"), s.Sent, s.Failed, s.Unbound)
	if s.MissingLevel > 0 {
		text += fmt.Sprintf("、設定缺漏 %d 人", s.MissingLevel)
	}
	return text
}

func notifyAdmin(ctx context.Context, d Deps, text string) {
	if d.Admin == nil {
		return
	}
	if err := d.Admin.Notify(ctx, text); err != nil {
		slog.Error("notify admin failed", "error", err)
	}
}

// sendOnce 先 Throttle 再 Send；ctx 類錯誤（Throttle 失敗）以 fatal 回傳
func sendOnce(ctx context.Context, d Deps, chatID, text string) (sendErr, fatal error) {
	if err := d.Throttle(ctx); err != nil {
		return nil, err
	}
	return d.Sender.Send(ctx, chatID, text), nil
}

// sendWithRetry 遇 429 等待後重試一次
func sendWithRetry(ctx context.Context, d Deps, chatID, text string) (sendErr, fatal error) {
	sendErr, fatal = sendOnce(ctx, d, chatID, text)
	if fatal != nil || sendErr == nil {
		return sendErr, fatal
	}
	wait, ok := telegram.RetryAfter(sendErr)
	if !ok {
		return sendErr, nil
	}
	if err := d.Sleep(ctx, wait); err != nil {
		return nil, err
	}
	return sendOnce(ctx, d, chatID, text)
}

// recordWithRetry 記錄失敗時以全新 context 重試一次（原 ctx 可能已逾時）。
// 仍失敗就回錯誤，由呼叫端中止整個流程，避免之後的訊息都沒留下紀錄而在下次重送。
func recordWithRetry(ctx context.Context, st Store, s store.Setting, day time.Time, sides []string, msg string) error {
	err := st.RecordSent(ctx, s.UserID, s.Symbol, day, sides, msg)
	if err == nil {
		return nil
	}
	slog.Error("record sent failed, retrying", "user_id", s.UserID, "symbol", s.Symbol, "error", err)
	rctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return st.RecordSent(rctx, s.UserID, s.Symbol, day, sides, msg)
}

func Run(ctx context.Context, deps Deps) (Summary, error) {
	st := deps.Store
	day, err := st.LatestTradeDate(ctx)
	if err != nil {
		return Summary{}, err
	}
	sum := Summary{TradeDate: day}

	ready, err := st.HasIndicators(ctx, day)
	if err != nil {
		return sum, err
	}
	if !ready {
		notifyAdmin(ctx, deps, fmt.Sprintf("自訂訊號 %s：指標尚未算完，未發送", day.Format("01/02")))
		return sum, ErrIndicatorsNotReady
	}

	limits, err := st.LoadLimits(ctx)
	if err != nil {
		return sum, err
	}
	all, err := st.LoadSettings(ctx)
	if err != nil {
		return sum, err
	}

	var active []store.Setting
	var symbols []string
	seenSym := map[string]bool{}
	missing := map[string]bool{}
	for _, s := range all {
		limit, ok := limits[s.MemberLevel]
		if !ok {
			if !missing[s.UserID] {
				missing[s.UserID] = true
				slog.Warn("member level not in limits", "user_id", s.UserID, "member_level", s.MemberLevel)
			}
			continue
		}
		if s.Rank > limit {
			continue
		}
		active = append(active, s)
		if !seenSym[s.Symbol] {
			seenSym[s.Symbol] = true
			symbols = append(symbols, s.Symbol)
		}
	}
	sum.MissingLevel = len(missing)

	if len(active) > 0 {
		bars, prev, err := st.LoadBars(ctx, symbols, day)
		if err != nil {
			return sum, err
		}
		sent, err := st.LoadSentKeys(ctx, day)
		if err != nil {
			return sum, err
		}
		if err := process(ctx, deps, day, active, bars, prev, sent, &sum); err != nil {
			return sum, err
		}
	}

	notifyAdmin(ctx, deps, SummaryText(sum))
	return sum, nil
}

func process(ctx context.Context, deps Deps, day time.Time, active []store.Setting,
	bars map[string]signals.Bar, prev map[string]signals.PrevBar, sent map[store.SentKey]bool, sum *Summary) error {
	st := deps.Store
	blocked := map[string]bool{}
	for _, s := range active {
		if blocked[s.UserID] {
			continue
		}
		bar, ok := bars[s.Symbol]
		if !ok {
			continue
		}
		var p *signals.PrevBar
		if pv, ok := prev[s.Symbol]; ok {
			p = &pv
		}

		var hits []signals.Hit
		var sides []string
		for _, side := range []string{"long", "short"} {
			if sent[store.SentKey{UserID: s.UserID, Symbol: s.Symbol, Side: side}] {
				continue
			}
			if !signals.SideMatched(s.Conditions, side, bar, p) {
				continue
			}
			var conds []signals.Condition
			for _, c := range s.Conditions {
				if c.Side == side {
					conds = append(conds, c)
				}
			}
			hits = append(hits, signals.Hit{Side: side, Conditions: conds})
			sides = append(sides, side)
		}
		if len(hits) == 0 {
			continue
		}

		msg := signals.FormatMessage(bar, p, day, hits, deps.SiteBaseURL)
		sendErr, fatal := sendWithRetry(ctx, deps, strconv.FormatInt(s.ChatID, 10), msg)
		if fatal != nil {
			return fatal
		}
		switch {
		case sendErr == nil:
			sum.Sent++
			if err := recordWithRetry(ctx, st, s, day, sides, msg); err != nil {
				return fmt.Errorf("record sent (user %s, symbol %s): %w", s.UserID, s.Symbol, err)
			}
		case telegram.IsBlocked(sendErr):
			blocked[s.UserID] = true
			if err := st.ClearTelegram(ctx, s.UserID, s.ChatID); err != nil {
				slog.Error("clear telegram failed", "user_id", s.UserID, "error", err)
			} else {
				sum.Unbound++
			}
		default:
			slog.Error("send failed", "user_id", s.UserID, "symbol", s.Symbol, "error", sendErr)
			sum.Failed++
		}
	}
	return nil
}
