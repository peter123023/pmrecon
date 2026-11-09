package recon

import (
	"math"
	"sort"

	"github.com/peter123023/pmrecon/internal/polymarket"
)

// Leg 是某市场下的一条持仓腿（一个 outcome token）。
type Leg struct {
	TokenID  string
	Outcome  string
	Size     float64
	Cost     float64
	Price    float64
	Value    float64
	Status   string
	Redeem   bool
	Merge    bool
	Opposite string
}

// Market 把同一 condition 的腿聚合到一起，仓位页面按市场看才不容易漏。
type Market struct {
	ConditionID string
	Title       string
	EndDate     string
	NegRisk     bool
	Legs        []Leg
	Cost        float64
	Value       float64
	Unrealized  float64
}

func (m Market) Statuses() []string {
	seen := map[string]bool{}
	var out []string
	for _, l := range m.Legs {
		if !seen[l.Status] {
			seen[l.Status] = true
			out = append(out, l.Status)
		}
	}
	return out
}

// Group 按 condition 聚合持仓，并按市值降序排列。
func Group(ps []polymarket.Position) []Market {
	idx := map[string]*Market{}
	var order []*Market
	for _, p := range ps {
		if p.CurrentSize == 0 && p.CurrentValue == 0 {
			continue
		}
		m, ok := idx[p.ConditionID]
		if !ok {
			m = &Market{
				ConditionID: p.ConditionID,
				Title:       p.Title,
				EndDate:     p.EndDate,
				NegRisk:     p.NegativeRisk,
			}
			idx[p.ConditionID] = m
			order = append(order, m)
		}
		m.Legs = append(m.Legs, Leg{
			TokenID:  p.TokenID,
			Outcome:  p.Outcome,
			Size:     p.CurrentSize,
			Cost:     p.TotalCost,
			Price:    p.CurrentPrice,
			Value:    p.CurrentValue,
			Status:   p.Status,
			Redeem:   p.Redeemable,
			Merge:    p.Mergeable,
			Opposite: p.OppositeID,
		})
		m.Cost += p.TotalCost
		m.Value += p.CurrentValue
		m.Unrealized += p.UnrealizedPnL
	}
	sort.SliceStable(order, func(i, j int) bool {
		if order[i].Value != order[j].Value {
			return order[i].Value > order[j].Value
		}
		return order[i].Title < order[j].Title
	})
	out := make([]Market, len(order))
	for i, m := range order {
		sort.SliceStable(m.Legs, func(a, b int) bool { return m.Legs[a].Outcome < m.Legs[b].Outcome })
		out[i] = *m
	}
	return out
}

// Totals 是全账户口径，包含已结算但仍挂着赎回权的仓位。
type Totals struct {
	Markets    int
	Legs       int
	Cost       float64
	Value      float64
	Unrealized float64
	Realized   float64
}

func Sum(ps []polymarket.Position, ms []Market) Totals {
	t := Totals{Markets: len(ms)}
	for _, m := range ms {
		t.Legs += len(m.Legs)
		t.Cost += m.Cost
		t.Value += m.Value
		t.Unrealized += m.Unrealized
	}
	for _, p := range ps {
		t.Realized += p.RealizedPnL
	}
	return t
}

// RedeemItem 是一个待赎回市场。Payout 只算 current_value：输掉的那一腿价格归零，
// 赎回动作本身回收 0 USDC，但仍然要执行，否则仓位会一直挂在列表里。
type RedeemItem struct {
	Market
	Payout    float64
	Worthless int
	Path      string
}

type RedeemPlan struct {
	Items     []RedeemItem
	Payout    float64
	Worthless int
}

func PlanRedeem(ms []Market) RedeemPlan {
	var plan RedeemPlan
	for _, m := range ms {
		var item RedeemItem
		item.Market = m
		item.Legs = nil
		for _, l := range m.Legs {
			if !l.Redeem {
				continue
			}
			item.Legs = append(item.Legs, l)
			item.Payout += l.Value
			if l.Value == 0 {
				item.Worthless++
			}
		}
		if len(item.Legs) == 0 {
			continue
		}
		// NegRisk 市场走 NegRiskAdapter，标准市场走 CTF 合约，两条链上路径不通用。
		if m.NegRisk {
			item.Path = "neg-risk"
		} else {
			item.Path = "ctf"
		}
		plan.Items = append(plan.Items, item)
		plan.Payout += item.Payout
		plan.Worthless += item.Worthless
	}
	sort.SliceStable(plan.Items, func(i, j int) bool { return plan.Items[i].Payout > plan.Items[j].Payout })
	return plan
}

// MergeItem 表示同一市场两条互补腿同时持有。一对互补份额在 CTF 里恒定值 1 USDC，
// 合并即把锁死的资金拿回来。两腿的 outcome 名不一定是 Yes/No（Up/Down 之类的市场同样存在），
// 所以这里保留腿本身，渲染时不要猜名字。
type MergeItem struct {
	Market
	LegA    Leg
	LegB    Leg
	Pairs   float64
	Release float64
}

type MergePlan struct {
	Items []MergeItem
	Total float64
	// Blocked 是两边都持有、但接口没有标记 mergeable 的市场数，
	// 多见于已进入结算或赎回流程、暂时不能合并的情况。
	Blocked int
}

func PlanMerge(ms []Market) MergePlan {
	var plan MergePlan
	for _, m := range ms {
		a, b, pairs := complementaryPair(m.Legs)
		if pairs == 0 {
			if bothSides(m.Legs) {
				plan.Blocked++
			}
			continue
		}
		plan.Items = append(plan.Items, MergeItem{Market: m, LegA: a, LegB: b, Pairs: pairs, Release: pairs})
		plan.Total += pairs
	}
	sort.SliceStable(plan.Items, func(i, j int) bool { return plan.Items[i].Release > plan.Items[j].Release })
	return plan
}

// complementaryPair 取一对互补腿。只有接口明确标记 mergeable 的腿才算数：
// 双方都标了才说明现在真的能合，避免把已结算市场误报成可释放资金。
func complementaryPair(legs []Leg) (Leg, Leg, float64) {
	for i, a := range legs {
		if !a.Merge || a.Size <= 0 {
			continue
		}
		for _, b := range legs[i+1:] {
			if !b.Merge || b.Size <= 0 || b.TokenID == a.TokenID {
				continue
			}
			if a.Opposite == b.TokenID || b.Opposite == a.TokenID {
				return a, b, round6(min(a.Size, b.Size))
			}
		}
	}
	return Leg{}, Leg{}, 0
}

// bothSides 判断「两边都持有但不是互补对」，用于解释为什么有对却合不了。
// 必须恰好两条有效腿且互为 opposite：NegRisk 的多结果市场可以同时持有多条，
// 但那些腿之间没有互补关系，按「两边都持有」统计会给出误导性的提示。
func bothSides(legs []Leg) bool {
	var positive []Leg
	for _, l := range legs {
		if l.Size > 0 {
			positive = append(positive, l)
		}
	}
	if len(positive) != 2 {
		return false
	}
	a, b := positive[0], positive[1]
	return a.Opposite == b.TokenID || b.Opposite == a.TokenID
}

// Diff 是链上余额与 API 口径的单条差异。
type Diff struct {
	TokenID string
	Outcome string
	Title   string
	API     float64
	Chain   float64
	Delta   float64
}

// Compare 把链上余额（按 token 索引，单位已换算成份额）与仓位数据对齐。
// 只报告超过 tolerance 的差异，链上缺失按 0 处理——那通常意味着仓位已被赎回但 API 还没刷新。
// 覆盖范围受限于 API 报告的 token：链上有、接口没列的仓位不会出现在这里。
func Compare(ms []Market, chain map[string]float64, tolerance float64) []Diff {
	var out []Diff
	for _, m := range ms {
		for _, l := range m.Legs {
			cb := chain[l.TokenID]
			d := l.Size - cb
			if math.Abs(d) > tolerance {
				out = append(out, Diff{
					TokenID: l.TokenID,
					Outcome: l.Outcome,
					Title:   m.Title,
					API:     l.Size,
					Chain:   cb,
					Delta:   round6(d),
				})
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return math.Abs(out[i].Delta) > math.Abs(out[j].Delta) })
	return out
}

func round6(v float64) float64 {
	return math.Round(v*1e6) / 1e6
}
