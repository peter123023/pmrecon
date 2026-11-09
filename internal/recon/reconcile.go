package recon

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/peter123023/pmrecon/internal/polymarket"
)

// LegFlow 把一条腿的成交流水和当前持仓放在一起看。
// 净份额（买入 - 卖出）与持仓不符的位置，通常就是仓位页看不出问题的地方：
// 跨钱包划转、合并、赎回都不会留下 SELL 记录。
type LegFlow struct {
	TokenID     string
	ConditionID string
	Title       string
	Outcome     string
	Bought      float64
	Sold        float64
	BuyCost     float64
	SellProfit  float64
	Trades      int
	LastAt      int64
	Held        float64
	Status      string
	Note        string
}

func (f LegFlow) Net() float64 { return round6(f.Bought - f.Sold) }

type Ledger struct {
	Legs       []LegFlow
	Bought     float64
	Sold       float64
	BuyCost    float64
	SellProfit float64
	// Matched / Mismatched 只统计仍然持仓的腿；清仓的腿归到 Closed，不参与结论。
	Matched    int
	Mismatched int
	Closed     int
	// UnmatchedPos 是「有持仓但完全找不到成交」的腿数，通常是转入。
	UnmatchedPos int
	// Truncated 表示成交数据只覆盖接口分页上限内的记录，结论可能因此出现假差异。
	Truncated bool
}

// Reconcile 以 token 为单位对齐成交与持仓。tolerance 用于吸收 6 位精度造成的尾差。
func Reconcile(trades []polymarket.Trade, ms []Market, tolerance float64) Ledger {
	idx := map[string]*LegFlow{}
	get := func(id string) *LegFlow {
		if f, ok := idx[id]; ok {
			return f
		}
		f := &LegFlow{TokenID: id}
		idx[id] = f
		return f
	}

	for _, t := range trades {
		f := get(t.TokenID)
		f.ConditionID = t.ConditionID
		if f.Title == "" {
			f.Title = t.Title
		}
		f.Outcome = t.Outcome
		f.Trades++
		if t.Timestamp > f.LastAt {
			f.LastAt = t.Timestamp
		}
		if strings.EqualFold(t.Side, "SELL") {
			f.Sold += t.Size
			f.SellProfit += t.Size * t.Price
		} else {
			f.Bought += t.Size
			f.BuyCost += t.Size * t.Price
		}
	}

	for _, m := range ms {
		for _, l := range m.Legs {
			f := get(l.TokenID)
			f.ConditionID = m.ConditionID
			f.Title = m.Title
			f.Outcome = l.Outcome
			f.Held = l.Size
			f.Status = l.Status
		}
	}

	var h Ledger
	for _, f := range idx {
		delta := f.Net() - f.Held
		switch {
		case f.Held == 0 && f.Net() == 0:
			f.Note = "买卖相抵，已清仓"
			h.Closed++
		case f.Held == 0:
			f.Note = "已清仓（结算/赎回或卖出未入账）"
			h.Closed++
		case math.Abs(delta) <= tolerance:
			f.Note = "一致"
			h.Matched++
		case f.Trades == 0:
			f.Note = "无成交记录，持仓来自转入"
			h.Mismatched++
			h.UnmatchedPos++
		case delta < 0:
			f.Note = "持仓多于成交，疑似转入或成交窗口截断"
			h.Mismatched++
		default:
			f.Note = "成交多于持仓，疑似已合并或赎回"
			h.Mismatched++
		}
		h.Bought += f.Bought
		h.Sold += f.Sold
		h.BuyCost += f.BuyCost
		h.SellProfit += f.SellProfit
		h.Legs = append(h.Legs, *f)
	}

	sort.SliceStable(h.Legs, func(i, j int) bool {
		a, b := h.Legs[i], h.Legs[j]
		if math.Abs(a.Net()-a.Held) != math.Abs(b.Net()-b.Held) {
			return math.Abs(a.Net()-a.Held) > math.Abs(b.Net()-b.Held)
		}
		if a.BuyCost != b.BuyCost {
			return a.BuyCost > b.BuyCost
		}
		return a.Title < b.Title
	})
	return h
}

// Snapshot 是某一时刻的账户状态，watch 靠它做前后两轮的差分。
type Snapshot struct {
	At           time.Time
	Cost         float64
	Value        float64
	Unrealized   float64
	Realized     float64
	RedeemPayout float64
	RedeemLegs   int
	MergeRelease float64
	MarketCount  int
	MarketValue  map[string]float64
	MarketTitles map[string]string
}

func TakeSnapshot(ms []Market, ps []polymarket.Position, at time.Time) Snapshot {
	s := Snapshot{
		At:           at,
		MarketValue:  make(map[string]float64, len(ms)),
		MarketTitles: make(map[string]string, len(ms)),
		MarketCount:  len(ms),
	}
	redeem := PlanRedeem(ms)
	merge := PlanMerge(ms)
	for _, m := range ms {
		s.Cost += m.Cost
		s.Value += m.Value
		s.Unrealized += m.Unrealized
		s.MarketValue[m.ConditionID] = m.Value
		s.MarketTitles[m.ConditionID] = m.Title
	}
	for _, p := range ps {
		s.Realized += p.RealizedPnL
	}
	s.RedeemPayout = redeem.Payout
	for _, it := range redeem.Items {
		s.RedeemLegs += len(it.Legs)
	}
	s.MergeRelease = merge.Total
	return s
}

// Diff 生成给人看的变更行。空切片代表没有任何变化，watch 可以据此决定是否静默。
func (s Snapshot) Diff(prev Snapshot) []string {
	var out []string
	if d := s.Value - prev.Value; math.Abs(d) >= 0.01 {
		out = append(out, fmt.Sprintf("市值 %+.2f → %.2f", d, s.Value))
	}
	if d := s.RedeemPayout - prev.RedeemPayout; math.Abs(d) >= 0.01 || s.RedeemLegs != prev.RedeemLegs {
		out = append(out, fmt.Sprintf("待赎回 %+.2f USDC，%d 条腿（原 %d 条）", d, s.RedeemLegs, prev.RedeemLegs))
	}
	if d := s.MergeRelease - prev.MergeRelease; math.Abs(d) >= 0.01 {
		out = append(out, fmt.Sprintf("可合并 %+.2f USDC → %.2f", d, s.MergeRelease))
	}
	if d := s.Realized - prev.Realized; math.Abs(d) >= 0.01 {
		out = append(out, fmt.Sprintf("已实现 %+.2f USDC → %.2f", d, s.Realized))
	}

	var added, removed []string
	for id := range s.MarketValue {
		if _, ok := prev.MarketValue[id]; !ok {
			added = append(added, s.MarketTitles[id])
		}
	}
	for id := range prev.MarketValue {
		if _, ok := s.MarketValue[id]; !ok {
			removed = append(removed, prev.MarketTitles[id])
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	if len(added) > 0 {
		out = append(out, fmt.Sprintf("新增 %d 个市场：%s", len(added), joinCap(added, 3)))
	}
	if len(removed) > 0 {
		out = append(out, fmt.Sprintf("减少 %d 个市场：%s", len(removed), joinCap(removed, 3)))
	}
	return out
}

func joinCap(items []string, n int) string {
	if len(items) <= n {
		return strings.Join(items, "; ")
	}
	return strings.Join(items[:n], "; ") + fmt.Sprintf(" 等 %d 个", len(items))
}
