package signals

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

var fmtDate = time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)

func fmtBar() Bar {
	return Bar{
		Symbol: "2330", Name: "台積電",
		Close: f(1085), Volume: f(21300), MACDHist: f(-0.82),
		VolMA:      map[int]float64{5: 10000},
		ForeignAvg: map[int]float64{10: 3412},
		RSI:        map[int]float64{14: 62.3},
		KDK:        f(54.1), KDD: f(49.8),
	}
}

func fmtPrev() *PrevBar { return &PrevBar{Close: f(1070), KDK: f(40), KDD: f(45)} }

func TestFormatMessage_Long(t *testing.T) {
	conds := []Condition{
		{Kind: "vol_ma", Op: "gt", Period: i(5), Value: f(1.5)},
		{Kind: "insti_avg", Op: "gt", Entity: "foreign", Period: i(10), Value: f(2000)},
		{Kind: "rsi", Op: "gt", Period: i(14), Value: f(50)},
		{Kind: "kd_cross", Cross: "golden"},
	}
	got := FormatMessage(fmtBar(), fmtPrev(), fmtDate, []Hit{{Side: "long", Conditions: conds}}, "https://darvishkzone.com/")
	want := "📈 台積電 2330｜多方訊號\n" +
		"11/09 收盤 1,085（+1.40%）\n\n" +
		"・量 > 5日均量×1.5　2.13 倍\n" +
		"・外資10日均買賣超 > 2,000張　3,412 張\n" +
		"・RSI14 > 50　62.3\n" +
		"・KD 黃金交叉　K 54.1 / D 49.8\n\n" +
		"https://darvishkzone.com/app/2330"
	assert.Equal(t, want, got)
}

func TestFormatMessage_BothSides(t *testing.T) {
	hits := []Hit{
		{Side: "long", Conditions: []Condition{{Kind: "rsi", Op: "gt", Period: i(14), Value: f(50)}}},
		{Side: "short", Conditions: []Condition{{Kind: "macd_hist", Op: "lt", Value: f(0)}}},
	}
	got := FormatMessage(fmtBar(), fmtPrev(), fmtDate, hits, "https://darvishkzone.com/")
	want := "📈📉 台積電 2330｜多空訊號\n" +
		"11/09 收盤 1,085（+1.40%）\n\n" +
		"📈 多方\n" +
		"・RSI14 > 50　62.3\n\n" +
		"📉 空方\n" +
		"・MACD 柱 < 0　-0.82\n\n" +
		"https://darvishkzone.com/app/2330"
	assert.Equal(t, want, got)
}

func TestFormatMessage_ShortOnlyAndNoName(t *testing.T) {
	b := fmtBar()
	b.Name = ""
	hits := []Hit{{Side: "short", Conditions: []Condition{{Kind: "macd_hist", Op: "lt", Value: f(0)}}}}
	got := FormatMessage(b, nil, fmtDate, hits, "https://x.com")
	assert.Contains(t, got, "📉 2330｜空方訊號\n11/09 收盤 1,085\n\n")
	assert.True(t, len(got) > 0 && got[len(got)-len("https://x.com/app/2330"):] == "https://x.com/app/2330")
}

func TestFormatMessage_NoPrev(t *testing.T) {
	hits := []Hit{{Side: "long", Conditions: []Condition{{Kind: "rsi", Op: "gt", Period: i(14), Value: f(50)}}}}
	got := FormatMessage(fmtBar(), nil, fmtDate, hits, "https://x.com")
	assert.Contains(t, got, "\n11/09 收盤 1,085\n")
	assert.NotContains(t, got, "（")
	// prev close nil
	got = FormatMessage(fmtBar(), &PrevBar{}, fmtDate, hits, "https://x.com")
	assert.Contains(t, got, "\n11/09 收盤 1,085\n")
}

func TestFormatMessage_Change(t *testing.T) {
	hits := []Hit{{Side: "long", Conditions: []Condition{{Kind: "rsi", Op: "gt", Period: i(14), Value: f(50)}}}}
	got := FormatMessage(fmtBar(), &PrevBar{Close: f(1085)}, fmtDate, hits, "https://x.com")
	assert.Contains(t, got, "11/09 收盤 1,085（0.00%）\n")
	got = FormatMessage(fmtBar(), &PrevBar{Close: f(1090.7)}, fmtDate, hits, "https://x.com")
	assert.Contains(t, got, "（-0.52%）")
}

func TestConditionLine(t *testing.T) {
	b := fmtBar()
	b.MarginBalance = f(123000)
	b.ShortBalance = f(8512)
	b.ShortMarginRatio = f(0.235)
	b.BBPercentB = f(0.82)
	b.KDK = f(85.2)
	b.MA = map[int]float64{20: 1042}
	b.TrustAvg = map[int]float64{5: -1532}
	kdP := &PrevBar{KDK: f(40), KDD: f(45)}
	cases := []struct {
		name string
		c    Condition
		t    Bar
		want string
	}{
		{"vol_ma", Condition{Kind: "vol_ma", Op: "gt", Period: i(5), Value: f(1.5)}, b, "量 > 5日均量×1.5　2.13 倍"},
		{"insti foreign", Condition{Kind: "insti_avg", Op: "gt", Entity: "foreign", Period: i(10), Value: f(2000)}, b, "外資10日均買賣超 > 2,000張　3,412 張"},
		{"insti trust", Condition{Kind: "insti_avg", Op: "lt", Entity: "trust", Period: i(5), Value: f(-1000)}, b, "投信5日均買賣超 < -1,000張　-1,532 張"},
		{"insti wan neg", Condition{Kind: "insti_avg", Op: "lt", Entity: "foreign", Period: i(10), Value: f(-10000)},
			Bar{ForeignAvg: map[int]float64{10: -12345}}, "外資10日均買賣超 < -1萬張　-1.2 萬張"},
		{"margin", Condition{Kind: "margin_balance", Op: "gt", Value: f(100000)}, Bar{MarginBalance: f(123000)}, "融資餘額 > 10萬張　12.3 萬張"},
		{"margin 12", Condition{Kind: "margin_balance", Op: "gt", Value: f(100000)}, Bar{MarginBalance: f(120000)}, "融資餘額 > 10萬張　12 萬張"},
		{"short_balance", Condition{Kind: "short_balance", Op: "lt", Value: f(20000)}, b, "融券餘額 < 2萬張　8,512 張"},
		{"smr", Condition{Kind: "short_margin_ratio", Op: "gt", Value: f(20)}, b, "券資比 > 20%　23.5%"},
		{"rsi", Condition{Kind: "rsi", Op: "gt", Period: i(14), Value: f(50)}, b, "RSI14 > 50　62.3"},
		{"macd", Condition{Kind: "macd_hist", Op: "gt", Value: f(0)}, Bar{MACDHist: f(1.25)}, "MACD 柱 > 0　1.25"},
		{"bb", Condition{Kind: "bb_percent_b", Op: "gt", Value: f(0.75)}, b, "布林 %b > 0.75　0.82"},
		{"kd_k", Condition{Kind: "kd_k", Op: "gt", Value: f(80)}, b, "K > 80　85.2"},
		{"golden", Condition{Kind: "kd_cross", Cross: "golden"}, b, "KD 黃金交叉　K 85.2 / D 49.8"},
		{"death", Condition{Kind: "kd_cross", Cross: "death"}, Bar{KDK: f(45.2), KDD: f(50.1)}, "KD 死亡交叉　K 45.2 / D 50.1"},
		{"price_ma gt", Condition{Kind: "price_ma", Op: "gt", Period: i(20)}, Bar{Close: f(1085), MA: map[int]float64{20: 1042}}, "站上 20MA　收 1,085 / MA 1,042"},
		{"price_ma lt", Condition{Kind: "price_ma", Op: "lt", Period: i(20)}, Bar{Close: f(1030), MA: map[int]float64{20: 1042.4567}}, "跌破 20MA　收 1,030 / MA 1,042.46"},
		{"nil fallback", Condition{Kind: "rsi", Op: "gt", Period: i(14), Value: f(50)}, Bar{}, "RSI14 > 50　-"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, ConditionLine(tc.c, tc.t, kdP))
		})
	}
}
