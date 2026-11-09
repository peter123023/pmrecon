package recon

import (
	"math"
	"testing"

	"github.com/peter123023/pmrecon/internal/polymarket"
)

// pos 只填对账会用到的字段，状态类字段在用例里按需补。
func pos(cond, token, outcome string, size, cost, value float64) polymarket.Position {
	return polymarket.Position{
		ConditionID:   cond,
		TokenID:       token,
		Outcome:       outcome,
		CurrentSize:   size,
		TotalCost:     cost,
		CurrentValue:  value,
		UnrealizedPnL: value - cost,
		Title:         "Market " + cond,
	}
}

func TestGroupAggregatesByCondition(t *testing.T) {
	yes := pos("0xa", "t1", "Yes", 100, 40, 120)
	yes.OppositeID, yes.Status = "t2", "OPEN"
	no := pos("0xa", "t2", "No", 100, 60, 5)
	no.OppositeID, no.Status = "t1", "OPEN"
	cleared := pos("0xb", "t3", "Yes", 0, 3, 0)
	cleared.Status, cleared.Redeemable = "REDEEMABLE", true

	ms := Group([]polymarket.Position{yes, no, cleared})
	if len(ms) != 1 {
		t.Fatalf("markets = %d, want 1 (zero-size leg dropped)", len(ms))
	}
	m := ms[0]
	if len(m.Legs) != 2 {
		t.Fatalf("legs = %d, want 2", len(m.Legs))
	}
	if m.Cost != 100 || m.Value != 125 || m.Unrealized != 25 {
		t.Errorf("cost/value/unrealized = %v/%v/%v", m.Cost, m.Value, m.Unrealized)
	}
	if got := m.Statuses(); len(got) != 1 || got[0] != "OPEN" {
		t.Errorf("statuses = %v", got)
	}
}

func TestGroupSortsByValueDesc(t *testing.T) {
	ms := Group([]polymarket.Position{
		pos("0xsmall", "a", "Yes", 10, 1, 2),
		pos("0xbig", "b", "Yes", 10, 1, 90),
	})
	if len(ms) != 2 || ms[0].Value != 90 {
		t.Fatalf("order wrong: %+v", ms)
	}
}

func TestPlanRedeemSplitsWinnersAndWorthless(t *testing.T) {
	win := pos("0xwin", "a", "Yes", 33, 1, 33)
	win.OppositeID, win.Status, win.Redeemable, win.NegativeRisk = "b", "REDEEMABLE", true, true
	lose := pos("0xlose", "c", "Yes", 50, 2, 0)
	lose.OppositeID, lose.Status, lose.Redeemable = "d", "REDEEMABLE", true
	open := pos("0xopen", "e", "Yes", 10, 3, 4)
	open.Status = "OPEN"

	plan := PlanRedeem(Group([]polymarket.Position{win, lose, open}))
	if len(plan.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(plan.Items))
	}
	if plan.Payout != 33 {
		t.Errorf("payout = %v, want 33", plan.Payout)
	}
	if plan.Worthless != 1 {
		t.Errorf("worthless = %d, want 1", plan.Worthless)
	}
	if plan.Items[0].Path != "neg-risk" || plan.Items[1].Path != "ctf" {
		t.Errorf("paths = %q/%q", plan.Items[0].Path, plan.Items[1].Path)
	}
}

func TestPlanRedeemEmpty(t *testing.T) {
	ms := Group([]polymarket.Position{pos("0xa", "t", "Yes", 5, 1, 5)})
	if plan := PlanRedeem(ms); len(plan.Items) != 0 || plan.Payout != 0 {
		t.Fatalf("expected empty plan, got %+v", plan)
	}
}

func TestPlanMergePairsComplementarySizes(t *testing.T) {
	a1 := pos("0xa", "t1", "Yes", 4000, 2000, 3298)
	a1.OppositeID, a1.Mergeable = "t2", true
	a2 := pos("0xa", "t2", "No", 4000, 2000, 700)
	a2.OppositeID, a2.Mergeable = "t1", true
	b1 := pos("0xb", "t3", "Yes", 10, 5, 5)
	b1.OppositeID, b1.Mergeable = "t4", true
	b2 := pos("0xb", "t4", "No", 4, 2, 2)
	b2.OppositeID, b2.Mergeable = "t3", true

	plan := PlanMerge(Group([]polymarket.Position{a1, a2, b1, b2}))
	if len(plan.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(plan.Items))
	}
	if plan.Items[0].Pairs != 4000 || plan.Items[0].Release != 4000 {
		t.Errorf("big market pairs = %v release = %v", plan.Items[0].Pairs, plan.Items[0].Release)
	}
	if plan.Items[1].Pairs != 4 {
		t.Errorf("small market pairs = %v, want 4", plan.Items[1].Pairs)
	}
	if plan.Total != 4004 {
		t.Errorf("total = %v, want 4004", plan.Total)
	}
}

func TestPlanMergeIgnoresRedeemableLegs(t *testing.T) {
	yes := pos("0xa", "t1", "Yes", 100, 50, 100)
	yes.OppositeID, yes.Status, yes.Redeemable = "t2", "REDEEMABLE", true
	no := pos("0xa", "t2", "No", 100, 50, 0)
	no.OppositeID, no.Status, no.Redeemable = "t1", "REDEEMABLE", true

	plan := PlanMerge(Group([]polymarket.Position{yes, no}))
	if len(plan.Items) != 0 {
		t.Fatalf("redeemable legs must not be merged: %+v", plan.Items)
	}
}

func TestCompareReportsBothDirections(t *testing.T) {
	ms := Group([]polymarket.Position{
		pos("0xa", "t1", "Yes", 10, 1, 1),
		pos("0xa", "t2", "No", 5, 1, 1),
	})
	// t1 链上少 0.5，t2 链上为 0，t3 只在链上有余额。
	chain := map[string]float64{"t1": 9.5, "t3": 7}
	diffs := Compare(ms, chain, 0.001)
	if len(diffs) != 3 {
		t.Fatalf("diffs = %d, want 3: %+v", len(diffs), diffs)
	}
	byToken := map[string]Diff{}
	for _, d := range diffs {
		byToken[d.TokenID] = d
	}
	if d := byToken["t1"]; d.Delta != 0.5 {
		t.Errorf("t1 diff = %+v, want +0.5", d)
	}
	if d := byToken["t3"]; d.Delta != -7 || d.API != 0 {
		t.Errorf("chain-only diff = %+v", d)
	}
	if math.Abs(diffs[0].Delta) < math.Abs(diffs[len(diffs)-1].Delta) {
		t.Errorf("diffs not sorted by magnitude: %+v", diffs)
	}
}

func TestCompareHonoursTolerance(t *testing.T) {
	ms := Group([]polymarket.Position{pos("0xa", "t1", "Yes", 10, 1, 1)})
	if diffs := Compare(ms, map[string]float64{"t1": 9.9999}, 0.001); len(diffs) != 0 {
		t.Fatalf("tolerance not applied: %+v", diffs)
	}
}
