package signals

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func f(v float64) *float64 { return &v }
func i(v int) *int         { return &v }

func TestMatch(t *testing.T) {
	cases := []struct {
		name string
		c    Condition
		bar  Bar
		prev *PrevBar
		want bool
	}{
		{"vol_ma true", Condition{Kind: "vol_ma", Op: "gt", Value: f(1.5), Period: i(5)}, Bar{Volume: f(15001), VolMA: map[int]float64{5: 10000}}, nil, true},
		{"vol_ma equal", Condition{Kind: "vol_ma", Op: "gt", Value: f(1.5), Period: i(5)}, Bar{Volume: f(15000), VolMA: map[int]float64{5: 10000}}, nil, false},
		{"vol_ma zero ma", Condition{Kind: "vol_ma", Op: "gt", Value: f(1.5), Period: i(5)}, Bar{Volume: f(15001), VolMA: map[int]float64{5: 0}}, nil, false},
		{"vol_ma missing period", Condition{Kind: "vol_ma", Op: "gt", Value: f(1.5), Period: i(5)}, Bar{Volume: f(15001), VolMA: map[int]float64{10: 1}}, nil, false},
		{"vol_ma nil volume", Condition{Kind: "vol_ma", Op: "gt", Value: f(1.5), Period: i(5)}, Bar{VolMA: map[int]float64{5: 10000}}, nil, false},
		{"vol_ma nil period", Condition{Kind: "vol_ma", Op: "gt", Value: f(1.5)}, Bar{Volume: f(15001), VolMA: map[int]float64{5: 10000}}, nil, false},
		{"vol_ma nil value", Condition{Kind: "vol_ma", Op: "gt", Period: i(5)}, Bar{Volume: f(15001), VolMA: map[int]float64{5: 10000}}, nil, false},
		{"insti foreign true", Condition{Kind: "insti_avg", Entity: "foreign", Op: "lt", Value: f(-1000), Period: i(10)}, Bar{ForeignAvg: map[int]float64{10: -1532}}, nil, true},
		{"insti foreign false", Condition{Kind: "insti_avg", Entity: "foreign", Op: "lt", Value: f(-1000), Period: i(10)}, Bar{ForeignAvg: map[int]float64{10: -500}}, nil, false},
		{"insti foreign equal", Condition{Kind: "insti_avg", Entity: "foreign", Op: "lt", Value: f(-1000), Period: i(10)}, Bar{ForeignAvg: map[int]float64{10: -1000}}, nil, false},
		{"insti foreign missing", Condition{Kind: "insti_avg", Entity: "foreign", Op: "lt", Value: f(-1000), Period: i(10)}, Bar{TrustAvg: map[int]float64{10: -2000}}, nil, false},
		{"insti trust true", Condition{Kind: "insti_avg", Entity: "trust", Op: "lt", Value: f(-1000), Period: i(10)}, Bar{TrustAvg: map[int]float64{10: -2000}}, nil, true},
		{"insti bad entity", Condition{Kind: "insti_avg", Entity: "x", Op: "lt", Value: f(-1000), Period: i(10)}, Bar{ForeignAvg: map[int]float64{10: -2000}}, nil, false},
		{"smr true", Condition{Kind: "short_margin_ratio", Op: "gt", Value: f(20)}, Bar{ShortMarginRatio: f(0.235)}, nil, true},
		{"smr equal", Condition{Kind: "short_margin_ratio", Op: "gt", Value: f(20)}, Bar{ShortMarginRatio: f(0.2)}, nil, false},
		{"smr nil", Condition{Kind: "short_margin_ratio", Op: "gt", Value: f(20)}, Bar{}, nil, false},
		{"rsi 9 true", Condition{Kind: "rsi", Op: "gt", Value: f(70), Period: i(9)}, Bar{RSI: map[int]float64{9: 75, 14: 10}}, nil, true},
		{"rsi 14 reads 14", Condition{Kind: "rsi", Op: "gt", Value: f(70), Period: i(14)}, Bar{RSI: map[int]float64{9: 75, 14: 10}}, nil, false},
		{"rsi equal", Condition{Kind: "rsi", Op: "lt", Value: f(30), Period: i(14)}, Bar{RSI: map[int]float64{14: 30}}, nil, false},
		{"rsi missing", Condition{Kind: "rsi", Op: "lt", Value: f(30), Period: i(14)}, Bar{RSI: map[int]float64{9: 1}}, nil, false},
		{"margin_balance true", Condition{Kind: "margin_balance", Op: "gt", Value: f(100)}, Bar{MarginBalance: f(101)}, nil, true},
		{"margin_balance equal", Condition{Kind: "margin_balance", Op: "gt", Value: f(100)}, Bar{MarginBalance: f(100)}, nil, false},
		{"margin_balance nil", Condition{Kind: "margin_balance", Op: "gt", Value: f(100)}, Bar{}, nil, false},
		{"short_balance true", Condition{Kind: "short_balance", Op: "lt", Value: f(100)}, Bar{ShortBalance: f(99)}, nil, true},
		{"short_balance equal", Condition{Kind: "short_balance", Op: "lt", Value: f(100)}, Bar{ShortBalance: f(100)}, nil, false},
		{"short_balance nil", Condition{Kind: "short_balance", Op: "lt", Value: f(100)}, Bar{}, nil, false},
		{"macd_hist true", Condition{Kind: "macd_hist", Op: "gt", Value: f(0)}, Bar{MACDHist: f(0.1)}, nil, true},
		{"macd_hist equal", Condition{Kind: "macd_hist", Op: "gt", Value: f(0)}, Bar{MACDHist: f(0)}, nil, false},
		{"macd_hist nil", Condition{Kind: "macd_hist", Op: "gt", Value: f(0)}, Bar{}, nil, false},
		{"bb true", Condition{Kind: "bb_percent_b", Op: "lt", Value: f(0)}, Bar{BBPercentB: f(-0.1)}, nil, true},
		{"bb equal", Condition{Kind: "bb_percent_b", Op: "lt", Value: f(0)}, Bar{BBPercentB: f(0)}, nil, false},
		{"bb nil", Condition{Kind: "bb_percent_b", Op: "lt", Value: f(0)}, Bar{}, nil, false},
		{"kd_k true", Condition{Kind: "kd_k", Op: "lt", Value: f(20)}, Bar{KDK: f(10)}, nil, true},
		{"kd_k equal", Condition{Kind: "kd_k", Op: "lt", Value: f(20)}, Bar{KDK: f(20)}, nil, false},
		{"kd_k nil", Condition{Kind: "kd_k", Op: "lt", Value: f(20)}, Bar{}, nil, false},
		{"kd golden true", Condition{Kind: "kd_cross", Cross: "golden"}, Bar{KDK: f(50), KDD: f(48)}, &PrevBar{KDK: f(40), KDD: f(45)}, true},
		{"kd golden prev equal", Condition{Kind: "kd_cross", Cross: "golden"}, Bar{KDK: f(50), KDD: f(48)}, &PrevBar{KDK: f(45), KDD: f(45)}, false},
		{"kd golden today equal", Condition{Kind: "kd_cross", Cross: "golden"}, Bar{KDK: f(48), KDD: f(48)}, &PrevBar{KDK: f(40), KDD: f(45)}, false},
		{"kd golden nil prev", Condition{Kind: "kd_cross", Cross: "golden"}, Bar{KDK: f(50), KDD: f(48)}, nil, false},
		{"kd golden nil field", Condition{Kind: "kd_cross", Cross: "golden"}, Bar{KDK: f(50), KDD: f(48)}, &PrevBar{KDK: f(40)}, false},
		{"kd golden nil today", Condition{Kind: "kd_cross", Cross: "golden"}, Bar{KDK: f(50)}, &PrevBar{KDK: f(40), KDD: f(45)}, false},
		{"kd death true", Condition{Kind: "kd_cross", Cross: "death"}, Bar{KDK: f(40), KDD: f(45)}, &PrevBar{KDK: f(50), KDD: f(48)}, true},
		{"kd death on golden data", Condition{Kind: "kd_cross", Cross: "death"}, Bar{KDK: f(50), KDD: f(48)}, &PrevBar{KDK: f(40), KDD: f(45)}, false},
		{"kd bad cross", Condition{Kind: "kd_cross", Cross: "x"}, Bar{KDK: f(50), KDD: f(48)}, &PrevBar{KDK: f(40), KDD: f(45)}, false},
		{"price_ma gt true", Condition{Kind: "price_ma", Op: "gt", Period: i(20)}, Bar{Close: f(1085), MA: map[int]float64{20: 1042}}, nil, true},
		{"price_ma lt false", Condition{Kind: "price_ma", Op: "lt", Period: i(20)}, Bar{Close: f(1085), MA: map[int]float64{20: 1042}}, nil, false},
		{"price_ma lt true", Condition{Kind: "price_ma", Op: "lt", Period: i(20)}, Bar{Close: f(1000), MA: map[int]float64{20: 1042}}, nil, true},
		{"price_ma equal", Condition{Kind: "price_ma", Op: "gt", Period: i(20)}, Bar{Close: f(1042), MA: map[int]float64{20: 1042}}, nil, false},
		{"price_ma missing ma", Condition{Kind: "price_ma", Op: "gt", Period: i(20)}, Bar{Close: f(1085)}, nil, false},
		{"price_ma nil close", Condition{Kind: "price_ma", Op: "gt", Period: i(20)}, Bar{MA: map[int]float64{20: 1042}}, nil, false},
		{"price_ma nil period", Condition{Kind: "price_ma", Op: "gt"}, Bar{Close: f(1085), MA: map[int]float64{20: 1042}}, nil, false},
		{"unknown kind", Condition{Kind: "nope", Op: "gt", Value: f(1)}, Bar{Close: f(5)}, nil, false},
		{"unknown op", Condition{Kind: "kd_k", Op: "ge", Value: f(1)}, Bar{KDK: f(5)}, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, Match(tc.c, tc.bar, tc.prev))
		})
	}
}

func TestSideMatched(t *testing.T) {
	bar := Bar{KDK: f(10), RSI: map[int]float64{14: 80}}
	okLong := Condition{Side: "long", Kind: "kd_k", Op: "lt", Value: f(20)}
	okLong2 := Condition{Side: "long", Kind: "rsi", Op: "gt", Value: f(70), Period: i(14)}
	badLong := Condition{Side: "long", Kind: "kd_k", Op: "gt", Value: f(20)}
	okShort := Condition{Side: "short", Kind: "kd_k", Op: "lt", Value: f(20)}
	badShort := Condition{Side: "short", Kind: "kd_k", Op: "gt", Value: f(20)}

	assert.False(t, SideMatched(nil, "long", bar, nil))
	assert.False(t, SideMatched([]Condition{okShort}, "long", bar, nil))
	assert.True(t, SideMatched([]Condition{okLong, okLong2}, "long", bar, nil))
	assert.False(t, SideMatched([]Condition{okLong, badLong}, "long", bar, nil))
	assert.True(t, SideMatched([]Condition{okLong, badShort}, "long", bar, nil))
	assert.False(t, SideMatched([]Condition{okLong, badShort}, "short", bar, nil))
}
