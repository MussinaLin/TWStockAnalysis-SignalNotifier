package signals

// Condition 與 TWStockAPI 存進 user_stock_signals.conditions 的 JSON 相同
type Condition struct {
	ID     string   `json:"id"`
	Side   string   `json:"side"` // "long" | "short"
	Kind   string   `json:"kind"`
	Op     string   `json:"op,omitempty"` // "gt" | "lt"
	Value  *float64 `json:"value,omitempty"`
	Period *int     `json:"period,omitempty"`
	Entity string   `json:"entity,omitempty"` // "foreign" | "trust"
	Cross  string   `json:"cross,omitempty"`  // "golden" | "death"
}

// Bar 是某檔在最新交易日 T 的資料；nil / map 沒有該 period 代表 DB 為 NULL
type Bar struct {
	Symbol, Name                                  string
	Close, Volume                                 *float64
	MarginBalance, ShortBalance, ShortMarginRatio *float64 // ShortMarginRatio 為 DB 原值（小數）
	MACDHist, BBPercentB, KDK, KDD                *float64
	VolMA, ForeignAvg, TrustAvg, RSI, MA          map[int]float64 // period → 值
}

// PrevBar 是 T 之前最近一筆 stock_daily_raw
type PrevBar struct{ Close, KDK, KDD *float64 }

func compare(op string, left, right float64) bool {
	switch op {
	case "gt":
		return left > right
	case "lt":
		return left < right
	}
	return false
}

// cmpPtr 比較 *float64 欄位與 c.Value；任一為 nil 回 false
func cmpPtr(c Condition, field *float64, scale float64) bool {
	if field == nil || c.Value == nil {
		return false
	}
	return compare(c.Op, *field*scale, *c.Value)
}

// cmpMap 比較 m[period] 與 c.Value
func cmpMap(c Condition, m map[int]float64) bool {
	if c.Period == nil || c.Value == nil {
		return false
	}
	v, ok := m[*c.Period]
	if !ok {
		return false
	}
	return compare(c.Op, v, *c.Value)
}

// Match 判斷單一條件在 t（與前一筆 p）上是否成立；缺資料一律 false，比較為嚴格
func Match(c Condition, t Bar, p *PrevBar) bool {
	switch c.Kind {
	case "vol_ma":
		if t.Volume == nil || c.Period == nil || c.Value == nil {
			return false
		}
		ma, ok := t.VolMA[*c.Period]
		if !ok || ma <= 0 {
			return false
		}
		return compare(c.Op, *t.Volume/ma, *c.Value)
	case "insti_avg":
		switch c.Entity {
		case "foreign":
			return cmpMap(c, t.ForeignAvg)
		case "trust":
			return cmpMap(c, t.TrustAvg)
		}
		return false
	case "short_margin_ratio":
		return cmpPtr(c, t.ShortMarginRatio, 100)
	case "rsi":
		return cmpMap(c, t.RSI)
	case "margin_balance":
		return cmpPtr(c, t.MarginBalance, 1)
	case "short_balance":
		return cmpPtr(c, t.ShortBalance, 1)
	case "macd_hist":
		return cmpPtr(c, t.MACDHist, 1)
	case "bb_percent_b":
		return cmpPtr(c, t.BBPercentB, 1)
	case "kd_k":
		return cmpPtr(c, t.KDK, 1)
	case "kd_cross":
		if p == nil || p.KDK == nil || p.KDD == nil || t.KDK == nil || t.KDD == nil {
			return false
		}
		switch c.Cross {
		case "golden":
			return *p.KDK < *p.KDD && *t.KDK > *t.KDD
		case "death":
			return *p.KDK > *p.KDD && *t.KDK < *t.KDD
		}
		return false
	case "price_ma":
		if t.Close == nil || c.Period == nil {
			return false
		}
		ma, ok := t.MA[*c.Period]
		if !ok {
			return false
		}
		return compare(c.Op, *t.Close, ma)
	}
	return false
}

// SideMatched：conds 裡 Side == side 的條件至少一個且全部 Match
func SideMatched(conds []Condition, side string, t Bar, p *PrevBar) bool {
	n := 0
	for _, c := range conds {
		if c.Side != side {
			continue
		}
		n++
		if !Match(c, t, p) {
			return false
		}
	}
	return n > 0
}
