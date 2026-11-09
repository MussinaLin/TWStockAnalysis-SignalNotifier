// Package store 是 notifier 的 DB 存取層：使用者設定、日線資料、已送通知紀錄。
package store

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"main/signals"
)

// DBTX 讓 *pgxpool.Pool 與 pgx.Tx 都可使用
type DBTX interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type Store struct{ q DBTX }

func New(q DBTX) *Store { return &Store{q: q} }

type Setting struct {
	UserID      string
	ChatID      int64
	MemberLevel int
	Symbol      string
	Rank        int // 1 起算，對該 user 全部設定（含 disabled）依 created_time, symbol 排序
	Conditions  []signals.Condition
}

type SentKey struct{ UserID, Symbol, Side string }

// LatestTradeDate 回 max(stock_daily_raw.trade_date)
func (s *Store) LatestTradeDate(ctx context.Context) (time.Time, error) {
	var d *time.Time
	if err := s.q.QueryRow(ctx, `SELECT max(trade_date) FROM stock_daily_raw`).Scan(&d); err != nil {
		return time.Time{}, err
	}
	if d == nil {
		return time.Time{}, fmt.Errorf("stock_daily_raw is empty")
	}
	return *d, nil
}

// HasIndicators：indicators 與 statistics 在 d 都至少有一列
func (s *Store) HasIndicators(ctx context.Context, d time.Time) (bool, error) {
	var ok bool
	err := s.q.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM stock_daily_indicators WHERE trade_date = $1)
		   AND EXISTS (SELECT 1 FROM stock_daily_statistics WHERE trade_date = $1)`, d).Scan(&ok)
	return ok, err
}

// LoadLimits 讀 CUSTOM_SIGNAL_LIMITS：level → stocks
func (s *Store) LoadLimits(ctx context.Context) (map[int]int, error) {
	var raw []byte
	if err := s.q.QueryRow(ctx, `SELECT value FROM user_config WHERE key = 'CUSTOM_SIGNAL_LIMITS'`).Scan(&raw); err != nil {
		return nil, fmt.Errorf("load CUSTOM_SIGNAL_LIMITS: %w", err)
	}
	return parseLimits(raw)
}

// parseLimits 解析 CUSTOM_SIGNAL_LIMITS。非整數的 key、缺 stocks、stocks 為負的等級
// 記 warn 後略過（該等級的使用者會被算進 MissingLevel）；JSON 本身無效才回錯誤。
func parseLimits(raw []byte) (map[int]int, error) {
	var parsed map[string]struct {
		Stocks *int `json:"stocks"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("parse CUSTOM_SIGNAL_LIMITS: %w", err)
	}
	out := make(map[int]int, len(parsed))
	for k, v := range parsed {
		lvl, err := strconv.Atoi(k)
		if err != nil {
			slog.Warn("CUSTOM_SIGNAL_LIMITS: skip non-integer level", "level", k)
			continue
		}
		if v.Stocks == nil || *v.Stocks < 0 {
			slog.Warn("CUSTOM_SIGNAL_LIMITS: skip level with missing or negative stocks", "level", k)
			continue
		}
		out[lvl] = *v.Stocks
	}
	return out, nil
}

// LoadSettings 回已綁定 Telegram 的 user 的 enabled 設定。Rank 對該 user 全部設定計算後才過濾。
// 依 rank, user_id 排序，讓不同使用者交錯、同一使用者的訊息不會連續送出（Telegram 單一 chat 約 1 則/秒）；
// 呼叫端不可假設同一使用者的設定相鄰。
// conditions 無法解析成 []Condition 的列記 warn 後略過。
func (s *Store) LoadSettings(ctx context.Context) ([]Setting, error) {
	rows, err := s.q.Query(ctx, `
		WITH ranked AS (
			SELECT user_id, symbol, enabled, conditions,
			       ROW_NUMBER() OVER (PARTITION BY user_id ORDER BY created_time, symbol) AS rn
			FROM user_stock_signals
		)
		SELECT u.id::text, u.telegram_chat_id, u.member_level, r.symbol, r.rn, r.conditions
		FROM ranked r
		JOIN users u ON u.id = r.user_id
		WHERE r.enabled AND u.telegram_chat_id IS NOT NULL
		ORDER BY r.rn, u.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Setting
	for rows.Next() {
		var st Setting
		var raw []byte
		if err := rows.Scan(&st.UserID, &st.ChatID, &st.MemberLevel, &st.Symbol, &st.Rank, &raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &st.Conditions); err != nil {
			slog.Warn("skip setting with bad conditions", "user_id", st.UserID, "symbol", st.Symbol, "err", err)
			continue
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

var (
	volMAPeriods  = []int{5, 10, 20}
	instiPeriods  = []int{5, 10, 15, 20, 30}
	rsiPeriods    = []int{9, 14}
	maPeriods     = []int{5, 10, 20}
	todayBaseCols = []string{
		"r.close", "r.volume", "r.margin_balance", "r.short_balance", "r.short_margin_ratio",
		"i.macd_hist", "i.bb_percent_b", "i.kd_k", "i.kd_d",
	}
)

// LoadBars：today 為 trade_date = d 的列（LEFT JOIN statistics / indicators）；
// prev 為每個 symbol trade_date < d 且 ≥ d − 1 年的最近一筆。
func (s *Store) LoadBars(ctx context.Context, symbols []string, d time.Time) (map[string]signals.Bar, map[string]signals.PrevBar, error) {
	today := map[string]signals.Bar{}
	prev := map[string]signals.PrevBar{}
	if len(symbols) == 0 {
		return today, prev, nil
	}

	// 欄位順序：base、vol_ma、foreign、trust、rsi、ma
	cols := append([]string{}, todayBaseCols...)
	for _, p := range volMAPeriods {
		cols = append(cols, fmt.Sprintf("s.vol_ma%d", p))
	}
	for _, p := range instiPeriods {
		cols = append(cols, fmt.Sprintf("s.foreign_net_%dd_avg", p))
	}
	for _, p := range instiPeriods {
		cols = append(cols, fmt.Sprintf("s.trust_net_%dd_avg", p))
	}
	for _, p := range rsiPeriods {
		cols = append(cols, fmt.Sprintf("i.rsi_%d", p))
	}
	for _, p := range maPeriods {
		cols = append(cols, fmt.Sprintf("s.ma%d", p))
	}
	sel := make([]string, len(cols))
	for i, c := range cols {
		sel[i] = c + "::float8"
	}
	q := `SELECT r.symbol, r.name, ` + strings.Join(sel, ", ") + `
		FROM stock_daily_raw r
		LEFT JOIN stock_daily_statistics s ON s.symbol = r.symbol AND s.trade_date = r.trade_date
		LEFT JOIN stock_daily_indicators i ON i.symbol = r.symbol AND i.trade_date = r.trade_date
		WHERE r.trade_date = $1 AND r.symbol = ANY($2)`
	rows, err := s.q.Query(ctx, q, d, symbols)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var sym string
		var name *string
		vals := make([]*float64, len(cols))
		dest := []any{&sym, &name}
		for i := range vals {
			dest = append(dest, &vals[i])
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, nil, err
		}
		b := signals.Bar{
			Symbol: sym,
			Close:  vals[0], Volume: vals[1],
			MarginBalance: vals[2], ShortBalance: vals[3], ShortMarginRatio: vals[4],
			MACDHist: vals[5], BBPercentB: vals[6], KDK: vals[7], KDD: vals[8],
		}
		if name != nil {
			b.Name = *name
		}
		i := len(todayBaseCols)
		take := func(periods []int) map[int]float64 {
			m := map[int]float64{}
			for _, p := range periods {
				if v := vals[i]; v != nil {
					m[p] = *v
				}
				i++
			}
			return m
		}
		b.VolMA = take(volMAPeriods)
		b.ForeignAvg = take(instiPeriods)
		b.TrustAvg = take(instiPeriods)
		b.RSI = take(rsiPeriods)
		b.MA = take(maPeriods)
		today[sym] = b
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	rows.Close()

	prows, err := s.q.Query(ctx, `
		SELECT DISTINCT ON (r.symbol) r.symbol, r.close::float8, i.kd_k::float8, i.kd_d::float8
		FROM stock_daily_raw r
		LEFT JOIN stock_daily_indicators i ON i.symbol = r.symbol AND i.trade_date = r.trade_date
		WHERE r.symbol = ANY($1) AND r.trade_date < $2 AND r.trade_date >= ($2::date - INTERVAL '1 year')
		ORDER BY r.symbol, r.trade_date DESC`, symbols, d)
	if err != nil {
		return nil, nil, err
	}
	defer prows.Close()
	for prows.Next() {
		var sym string
		var p signals.PrevBar
		if err := prows.Scan(&sym, &p.Close, &p.KDK, &p.KDD); err != nil {
			return nil, nil, err
		}
		prev[sym] = p
	}
	return today, prev, prows.Err()
}

// LoadSentKeys 回 trade_date = d 已送出的 (user, symbol, side)
func (s *Store) LoadSentKeys(ctx context.Context, d time.Time) (map[SentKey]bool, error) {
	rows, err := s.q.Query(ctx, `SELECT user_id::text, symbol, side FROM user_signal_notifications WHERE trade_date = $1`, d)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[SentKey]bool{}
	for rows.Next() {
		var k SentKey
		if err := rows.Scan(&k.UserID, &k.Symbol, &k.Side); err != nil {
			return nil, err
		}
		out[k] = true
	}
	return out, rows.Err()
}

// RecordSent 每個 side 一列，已存在則略過
func (s *Store) RecordSent(ctx context.Context, userID, symbol string, d time.Time, sides []string, message string) error {
	for _, side := range sides {
		if _, err := s.q.Exec(ctx, `
			INSERT INTO user_signal_notifications (user_id, symbol, trade_date, side, message)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (user_id, symbol, trade_date, side) DO NOTHING`,
			userID, symbol, d, side, message); err != nil {
			return err
		}
	}
	return nil
}

// ClearTelegram 清空使用者的三個 telegram 欄位；只在目前綁定仍是 chatID 時才清，
// 避免跑到一半使用者重新綁定新帳號時把新綁定清掉。
func (s *Store) ClearTelegram(ctx context.Context, userID string, chatID int64) error {
	_, err := s.q.Exec(ctx, `
		UPDATE users SET telegram_chat_id = NULL, telegram_username = NULL, telegram_bound_at = NULL
		WHERE id = $1 AND telegram_chat_id = $2`, userID, chatID)
	return err
}
