package signals

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// Hit 是某一方（long/short）成立的條件
type Hit struct {
	Side       string      // "long" | "short"
	Conditions []Condition // 該 side 的條件，依設定陣列順序
}

const noValue = "-" // 缺資料時的今日值

// FormatMessage 組出使用者收到的 Telegram 訊息；hits 為 1 或 2 筆，long 在前
func FormatMessage(t Bar, p *PrevBar, tradeDate time.Time, hits []Hit, siteBaseURL string) string {
	name := t.Symbol
	if t.Name != "" {
		name = t.Name + " " + t.Symbol
	}
	var head string
	switch {
	case len(hits) >= 2:
		head = "📈📉 " + name + "｜多空訊號"
	case len(hits) == 1 && hits[0].Side == "short":
		head = "📉 " + name + "｜空方訊號"
	default:
		head = "📈 " + name + "｜多方訊號"
	}

	second := tradeDate.Format("01/02") + " 收盤 " + fmtPtr(t.Close, 2)
	if p != nil && p.Close != nil && t.Close != nil && *p.Close != 0 {
		chg := math.Round((*t.Close-*p.Close) / *p.Close * 10000) / 100
		s := group(chg, 2, false)
		if chg > 0 {
			s = "+" + s
		}
		second += "（" + s + "%）"
	}

	blocks := []string{head + "\n" + second}
	for _, h := range hits {
		var sb strings.Builder
		if len(hits) >= 2 {
			if h.Side == "short" {
				sb.WriteString("📉 空方\n")
			} else {
				sb.WriteString("📈 多方\n")
			}
		}
		lines := make([]string, 0, len(h.Conditions))
		for _, c := range h.Conditions {
			lines = append(lines, "・"+ConditionLine(c, t, p))
		}
		sb.WriteString(strings.Join(lines, "\n"))
		blocks = append(blocks, sb.String())
	}
	blocks = append(blocks, strings.TrimRight(siteBaseURL, "/")+"/app/"+t.Symbol)

	// 兩方時，多空段之間空一行；單方時 head 後空一行（皆為 \n\n 分隔）
	return strings.Join(blocks, "\n\n")
}

func opSym(op string) string {
	if op == "lt" {
		return "<"
	}
	return ">"
}

// ConditionLine 回傳單一條件行（不含開頭的 "・"）；今日值缺資料時以 "-" 代替
func ConditionLine(c Condition, t Bar, p *PrevBar) string {
	const sep = "　" // U+3000
	op := opSym(c.Op)
	period := 0
	if c.Period != nil {
		period = *c.Period
	}
	val := 0.0
	if c.Value != nil {
		val = *c.Value
	}
	mapVal := func(m map[int]float64) (float64, bool) {
		if c.Period == nil {
			return 0, false
		}
		v, ok := m[*c.Period]
		return v, ok
	}

	switch c.Kind {
	case "vol_ma":
		today := noValue
		if t.Volume != nil && c.Period != nil {
			if ma, ok := t.VolMA[period]; ok && ma > 0 {
				today = fixed2(*t.Volume/ma) + " 倍"
			}
		}
		return fmt.Sprintf("量 %s %d日均量×%s%s%s", op, period, fmtNum(val, 2), sep, today)
	case "insti_avg":
		label, m := "外資", t.ForeignAvg
		if c.Entity == "trust" {
			label, m = "投信", t.TrustAvg
		}
		today := noValue
		if v, ok := mapVal(m); ok {
			today = fmtLots(v, true)
		}
		return fmt.Sprintf("%s%d日均買賣超 %s %s%s%s", label, period, op, fmtLots(val, false), sep, today)
	case "margin_balance":
		return lotsLine("融資餘額", op, val, t.MarginBalance, sep)
	case "short_balance":
		return lotsLine("融券餘額", op, val, t.ShortBalance, sep)
	case "short_margin_ratio":
		today := noValue
		if t.ShortMarginRatio != nil {
			today = fmtNum(*t.ShortMarginRatio*100, 2) + "%"
		}
		return fmt.Sprintf("券資比 %s %s%%%s%s", op, fmtNum(val, 2), sep, today)
	case "rsi":
		today := noValue
		if v, ok := mapVal(t.RSI); ok {
			today = fmtNum(v, 2)
		}
		return fmt.Sprintf("RSI%d %s %s%s%s", period, op, fmtNum(val, 2), sep, today)
	case "macd_hist":
		return numLine("MACD 柱", op, val, t.MACDHist, sep)
	case "bb_percent_b":
		return numLine("布林 %b", op, val, t.BBPercentB, sep)
	case "kd_k":
		return numLine("K", op, val, t.KDK, sep)
	case "kd_cross":
		name := "KD 黃金交叉"
		if c.Cross == "death" {
			name = "KD 死亡交叉"
		}
		return fmt.Sprintf("%s%sK %s / D %s", name, sep, fmtPtr(t.KDK, 2), fmtPtr(t.KDD, 2))
	case "price_ma":
		word := "站上"
		if c.Op == "lt" {
			word = "跌破"
		}
		ma := noValue
		if v, ok := mapVal(t.MA); ok {
			ma = fmtNum(v, 2)
		}
		return fmt.Sprintf("%s %dMA%s收 %s / MA %s", word, period, sep, fmtPtr(t.Close, 2), ma)
	}
	return c.Kind
}

func lotsLine(label, op string, val float64, field *float64, sep string) string {
	today := noValue
	if field != nil {
		today = fmtLots(*field, true)
	}
	return fmt.Sprintf("%s %s %s%s%s", label, op, fmtLots(val, false), sep, today)
}

func numLine(label, op string, val float64, field *float64, sep string) string {
	return fmt.Sprintf("%s %s %s%s%s", label, op, fmtNum(val, 2), sep, fmtPtr(field, 2))
}

func fmtPtr(v *float64, maxDecimals int) string {
	if v == nil {
		return noValue
	}
	return fmtNum(*v, maxDecimals)
}

// fixed2 固定兩位小數（含千分位）
func fixed2(v float64) string { return group(math.Round(v*100)/100, 2, false) }

// fmtNum：四捨五入到 maxDecimals 位，去掉尾端 0 與小數點，整數部分加千分位
func fmtNum(v float64, maxDecimals int) string { return group(v, maxDecimals, true) }

func group(v float64, decimals int, trim bool) string {
	pow := math.Pow(10, float64(decimals))
	r := math.Round(v*pow) / pow
	s := strconv.FormatFloat(math.Abs(r), 'f', decimals, 64)
	intPart, frac := s, ""
	if i := strings.IndexByte(s, '.'); i >= 0 {
		intPart, frac = s[:i], s[i+1:]
	}
	if trim {
		frac = strings.TrimRight(frac, "0")
	}
	var b strings.Builder
	for i, ch := range intPart {
		if i > 0 && (len(intPart)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(ch)
	}
	out := b.String()
	if frac != "" {
		out += "." + frac
	}
	if r < 0 && out != "0" && strings.Trim(out, "0.,") != "" {
		out = "-" + out
	}
	return out
}

// fmtLots：|v| >= 10000 → 萬張（最多一位），否則整數張；spaced 時數字與單位間留一個空白
func fmtLots(v float64, spaced bool) string {
	sp := ""
	if spaced {
		sp = " "
	}
	if math.Abs(v) >= 10000 {
		return fmtNum(v/10000, 1) + sp + "萬張"
	}
	return fmtNum(v, 0) + sp + "張"
}
