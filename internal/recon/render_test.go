package recon

import (
	"bytes"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/peter123023/pmrecon/internal/polymarket"
)

func TestMergeRendersActualOutcomeNames(t *testing.T) {
	up := pos("0xa", "t1", "Up", 100, 40, 60)
	up.OppositeID, up.Mergeable = "t2", true
	down := pos("0xa", "t2", "Down", 80, 30, 20)
	down.OppositeID, down.Mergeable = "t1", true

	var buf bytes.Buffer
	Merge(&buf, "0xabc", PlanMerge(Group([]polymarket.Position{up, down})))
	out := buf.String()
	for _, want := range []string{"Up", "Down", "80.0000", "80.00"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "YES") || strings.Contains(out, "NO") {
		t.Errorf("outcome names were guessed:\n%s", out)
	}
}

func TestMergeBlockedMessage(t *testing.T) {
	yes := pos("0xa", "t1", "Yes", 100, 40, 100)
	yes.OppositeID, yes.Status, yes.Redeemable = "t2", "REDEEMABLE", true
	no := pos("0xa", "t2", "No", 100, 60, 0)
	no.OppositeID, no.Status, no.Redeemable = "t1", "REDEEMABLE", true

	var buf bytes.Buffer
	Merge(&buf, "0xabc", PlanMerge(Group([]polymarket.Position{yes, no})))
	if !strings.Contains(buf.String(), "未标记可合并") {
		t.Errorf("blocked message missing:\n%s", buf.String())
	}
}

func TestHistoryRenderShowsOnlyHeldByDefault(t *testing.T) {
	ms := Group([]polymarket.Position{pos("0xnew", "t1", "Yes", 50, 20, 30)})
	trades := []polymarket.Trade{
		trade("t1", "0xnew", "Yes", "BUY", 10, 0.5, 1),
	}
	ledger := Reconcile(trades, ms, 0.001)
	ledger.Legs = append(ledger.Legs, LegFlow{TokenID: "t9", ConditionID: "0xold", Title: "Old Market", Held: 0, Bought: 5, Note: "已清仓（结算/赎回或卖出未入账）"})

	var buf bytes.Buffer
	History(&buf, "0xabc", ledger, false)
	out := buf.String()
	if strings.Contains(out, "Old Market") {
		t.Errorf("closed leg leaked into default view:\n%s", out)
	}
	if !strings.Contains(out, "New Market") && !strings.Contains(out, "Market 0xnew") {
		t.Errorf("held leg missing:\n%s", out)
	}

	buf.Reset()
	History(&buf, "0xabc", ledger, true)
	if !strings.Contains(buf.String(), "Old Market") {
		t.Errorf("-all should include closed legs:\n%s", buf.String())
	}
}

func TestTableTruncatesInsteadOfPanicking(t *testing.T) {
	var buf bytes.Buffer
	table(&buf, []col{{name: "A", width: 6}}, [][]string{{"0123456789"}})
	if !strings.Contains(buf.String(), "...") {
		t.Errorf("expected truncation, got %q", buf.String())
	}
}

func TestTitleTruncatesOnRuneBoundaries(t *testing.T) {
	long := strings.Repeat("Will Chênois win the 2026 cup? ", 4)
	got := title(long)
	if !utf8.ValidString(got) {
		t.Fatalf("truncation broke UTF-8: %q", got)
	}
	if utf8.RuneCountInString(got) != titleWidth {
		t.Errorf("rune length = %d, want %d", utf8.RuneCountInString(got), titleWidth)
	}
	if clip("Chênois Féminin", 8) != "Chênois " {
		t.Errorf("clip = %q", clip("Chênois Féminin", 8))
	}
}

func TestLabelFallsBackToCondition(t *testing.T) {
	if got := label("", "0x7ad403c350deadbeef"); got != "0x7ad4..beef" {
		t.Errorf("label = %q", got)
	}
	if got := label("Real Title", "0x1"); got != "Real Title" {
		t.Errorf("label = %q", got)
	}
}
