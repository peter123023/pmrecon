package recon

import (
	"strings"
	"testing"
	"time"

	"github.com/peter123023/pmrecon/internal/polymarket"
)

func trade(token, cond, outcome, side string, size, price float64, ts int64) polymarket.Trade {
	return polymarket.Trade{
		TokenID:     token,
		ConditionID: cond,
		Outcome:     outcome,
		Side:        side,
		Size:        size,
		Price:       price,
		Timestamp:   ts,
		Title:       "Market " + cond,
	}
}

func TestReconcileMatchesWhenNetEqualsHeld(t *testing.T) {
	trades := []polymarket.Trade{
		trade("t1", "0xa", "Yes", "BUY", 100, 0.4, 1000),
		trade("t1", "0xa", "Yes", "SELL", 40, 0.6, 2000),
	}
	ms := Group([]polymarket.Position{
		pos("0xa", "t1", "Yes", 60, 24, 30),
	})
	h := Reconcile(trades, ms, 0.001)
	if len(h.Legs) != 1 {
		t.Fatalf("legs = %d", len(h.Legs))
	}
	l := h.Legs[0]
	if l.Bought != 100 || l.Sold != 40 || l.Net() != 60 || l.Held != 60 {
		t.Fatalf("flow wrong: %+v", l)
	}
	if h.Matched != 1 || h.Mismatched != 0 {
		t.Errorf("matched/mismatched = %d/%d", h.Matched, h.Mismatched)
	}
	if h.BuyCost != 40 || h.SellProfit != 24 {
		t.Errorf("cost/proceeds = %v/%v", h.BuyCost, h.SellProfit)
	}
}

func TestReconcileFlagsSurplusHolding(t *testing.T) {
	ms := Group([]polymarket.Position{
		pos("0xa", "t1", "Yes", 500, 100, 400),
	})
	h := Reconcile([]polymarket.Trade{trade("t1", "0xa", "Yes", "BUY", 100, 0.5, 1)}, ms, 0.001)
	if h.Mismatched != 1 {
		t.Fatalf("want 1 mismatch, got %+v", h)
	}
	if got := h.Legs[0].Note; got != "持仓多于成交，疑似转入或成交窗口截断" {
		t.Errorf("note = %q", got)
	}
}

func TestReconcileFlagsClosedPosition(t *testing.T) {
	trades := []polymarket.Trade{trade("t1", "0xa", "Yes", "BUY", 100, 0.5, 1)}
	h := Reconcile(trades, nil, 0.001)
	if len(h.Legs) != 1 || h.Legs[0].Held != 0 {
		t.Fatalf("legs = %+v", h.Legs)
	}
	if h.Closed != 1 || h.Mismatched != 0 {
		t.Errorf("closed/mismatched = %d/%d", h.Closed, h.Mismatched)
	}
}

func TestReconcilePositionWithoutTrades(t *testing.T) {
	ms := Group([]polymarket.Position{
		pos("0xa", "t1", "Yes", 500, 100, 400),
	})
	h := Reconcile(nil, ms, 0.001)
	if h.UnmatchedPos != 1 {
		t.Fatalf("unmatched = %d", h.UnmatchedPos)
	}
	if got := h.Legs[0].Note; got != "无成交记录，持仓来自转入" {
		t.Errorf("note = %q", got)
	}
}

func TestReconcileSortsByGapThenCost(t *testing.T) {
	ms := Group([]polymarket.Position{
		pos("0xa", "t1", "Yes", 50, 10, 40),
		pos("0xb", "t2", "Yes", 10, 10, 8),
	})
	trades := []polymarket.Trade{
		trade("t1", "0xa", "Yes", "BUY", 10, 0.5, 1),
		trade("t2", "0xb", "Yes", "BUY", 5, 0.5, 1),
	}
	h := Reconcile(trades, ms, 0.001)
	if len(h.Legs) != 2 || h.Legs[0].TokenID != "t1" {
		t.Fatalf("order wrong: %+v", h.Legs)
	}
}

func TestSnapshotAndDiff(t *testing.T) {
	t1 := pos("0xa", "t1", "Yes", 100, 40, 120)
	t1.OppositeID, t1.Mergeable = "t2", true
	t2 := pos("0xa", "t2", "No", 100, 60, 5)
	t2.OppositeID, t2.Mergeable = "t1", true
	ms := Group([]polymarket.Position{t1, t2})
	ps := []polymarket.Position{
		{ConditionID: "0xa", RealizedPnL: 1.5},
	}
	prev := TakeSnapshot(nil, nil, time.Unix(1000, 0))
	cur := TakeSnapshot(ms, ps, time.Unix(1060, 0))

	if cur.Value != 125 || cur.Cost != 100 || cur.MarketCount != 1 {
		t.Fatalf("snapshot = %+v", cur)
	}
	changes := cur.Diff(prev)
	if len(changes) == 0 {
		t.Fatal("expected changes from empty snapshot")
	}
	joined := ""
	for _, c := range changes {
		joined += c + "\n"
	}
	for _, want := range []string{"市值", "可合并", "已实现", "新增"} {
		if !strings.Contains(joined, want) {
			t.Errorf("changes missing %q:\n%s", want, joined)
		}
	}
	if got := cur.Diff(cur); len(got) != 0 {
		t.Errorf("self diff should be empty, got %v", got)
	}
}

func TestSnapshotTracksRedeemableGrowth(t *testing.T) {
	before := TakeSnapshot(Group([]polymarket.Position{
		pos("0xa", "t1", "Yes", 10, 5, 4),
	}), nil, time.Unix(0, 0))
	settled := pos("0xa", "t1", "Yes", 10, 5, 10)
	settled.Status, settled.Redeemable = "REDEEMABLE", true
	after := TakeSnapshot(Group([]polymarket.Position{settled}), nil, time.Unix(60, 0))

	changes := after.Diff(before)
	if len(changes) == 0 || !strings.Contains(strings.Join(changes, "\n"), "待赎回") {
		t.Fatalf("expected redeem change, got %v", changes)
	}
}
