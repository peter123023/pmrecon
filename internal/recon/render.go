package recon

import (
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"
)

// 这些列宽是终端可读性上限，标题超长直接截断，避免整张表被一个市场名撑坏。
const (
	titleWidth   = 46
	outcomeWidth = 12
)

type col struct {
	name  string
	right bool
	width int
}

// table 按 rune 数算列宽。按字节算的话，带变音符的标题（Polymarket 里很常见）
// 会被截成半个字符，列也会错位。
func table(w io.Writer, cols []col, rows [][]string) {
	widths := make([]int, len(cols))
	for i, c := range cols {
		widths[i] = utf8.RuneCountInString(c.name)
	}
	cells := make([][]string, len(rows))
	for i, r := range rows {
		cells[i] = make([]string, len(cols))
		for j := range cols {
			v := ""
			if j < len(r) {
				v = r[j]
			}
			cells[i][j] = v
			if n := utf8.RuneCountInString(v); n > widths[j] {
				widths[j] = n
			}
		}
	}
	for i, c := range cols {
		if c.width > 0 && widths[i] > c.width {
			widths[i] = c.width
		}
	}

	writeRow := func(vals []string) {
		parts := make([]string, len(cols))
		for i := range cols {
			v := vals[i]
			if n := widths[i]; utf8.RuneCountInString(v) > n {
				if n <= 3 {
					v = ""
				} else {
					v = truncate(v, n)
				}
			}
			pad := strings.Repeat(" ", widths[i]-utf8.RuneCountInString(v))
			if cols[i].right {
				parts[i] = pad + v
			} else {
				parts[i] = v + pad
			}
		}
		fmt.Fprintln(w, strings.TrimRight(strings.Join(parts, "  "), " "))
	}

	head := make([]string, len(cols))
	for i, c := range cols {
		head[i] = c.name
	}
	writeRow(head)
	for _, r := range cells {
		writeRow(r)
	}
}

func title(s string) string { return truncate(s, titleWidth) }

// label 在标题缺失时退回 condition 短号：成交记录偶尔不带 title，留空列没有信息量。
func label(title, conditionID string) string {
	if title == "" {
		return short(conditionID)
	}
	return truncate(title, titleWidth)
}

func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return clip(s, n-3) + "..."
}

// clip 硬截断，不补省略号。
func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

func money(v float64) string { return fmt.Sprintf("%.2f", v) }
func size(v float64) string  { return fmt.Sprintf("%.4f", v) }

func Pos(w io.Writer, wallet string, ms []Market, t Totals) {
	fmt.Fprintf(w, "仓位 %s\n", wallet)
	if len(ms) == 0 {
		fmt.Fprintln(w, "该钱包当前没有持仓。")
		return
	}
	fmt.Fprintf(w, "%d 个市场 / %d 条持仓腿\n\n", t.Markets, t.Legs)

	rows := make([][]string, 0, t.Legs)
	for _, m := range ms {
		for i, l := range m.Legs {
			name := ""
			if i == 0 {
				name = title(m.Title)
			}
			rows = append(rows, []string{
				name,
				clip(l.Outcome, outcomeWidth),
				size(l.Size),
				size(l.Price),
				money(l.Value),
				money(l.Value - l.Cost),
				strings.Join(m.Statuses(), ","),
			})
		}
	}
	table(w, []col{
		{name: "MARKET"},
		{name: "OUTCOME"},
		{name: "SIZE", right: true},
		{name: "PRICE", right: true},
		{name: "VALUE", right: true},
		{name: "PNL", right: true},
		{name: "STATUS"},
	}, rows)

	fmt.Fprintf(w, "\n成本 %.2f  市值 %.2f  未实现 %.2f  已实现 %.2f USDC\n", t.Cost, t.Value, t.Unrealized, t.Realized)
}

func Redeem(w io.Writer, wallet string, plan RedeemPlan) {
	fmt.Fprintf(w, "待赎回 %s\n", wallet)
	if len(plan.Items) == 0 {
		fmt.Fprintln(w, "没有处于可赎回状态的仓位。")
		return
	}
	fmt.Fprintf(w, "%d 个市场可赎回，预计回收 %.2f USDC，其中 %d 条为归零腿（回收 0，但赎回后才会从列表消失）\n\n",
		len(plan.Items), plan.Payout, plan.Worthless)

	rows := make([][]string, 0, len(plan.Items))
	for _, it := range plan.Items {
		legs := make([]string, 0, len(it.Legs))
		for _, l := range it.Legs {
			legs = append(legs, l.Outcome)
		}
		rows = append(rows, []string{
			title(it.Title),
			strings.Join(legs, "+"),
			it.EndDate,
			money(it.Payout),
			it.Path,
		})
	}
	table(w, []col{
		{name: "MARKET"},
		{name: "OUTCOME"},
		{name: "END"},
		{name: "PAYOUT", right: true},
		{name: "PATH"},
	}, rows)
	fmt.Fprintf(w, "\n合计 %.2f USDC\n", plan.Payout)
}

func Merge(w io.Writer, wallet string, plan MergePlan) {
	fmt.Fprintf(w, "可合并 %s\n", wallet)
	if len(plan.Items) == 0 {
		msg := "没有同时持有互补份额的市场。"
		if plan.Blocked > 0 {
			msg = fmt.Sprintf("有 %d 个市场两边都持有，但接口未标记可合并（可能已进入结算或赎回流程）。", plan.Blocked)
		}
		fmt.Fprintln(w, msg)
		return
	}
	fmt.Fprintf(w, "%d 个市场可合并，可释放 %.2f USDC（互补份额每对恒定 1 USDC）\n\n", len(plan.Items), plan.Total)

	rows := make([][]string, 0, len(plan.Items))
	for _, it := range plan.Items {
		rows = append(rows, []string{
			title(it.Title),
			clip(it.LegA.Outcome, outcomeWidth),
			size(it.LegA.Size),
			clip(it.LegB.Outcome, outcomeWidth),
			size(it.LegB.Size),
			size(it.Pairs),
			money(it.Release),
		})
	}
	table(w, []col{
		{name: "MARKET"},
		{name: "LEG A"},
		{name: "SIZE A", right: true},
		{name: "LEG B"},
		{name: "SIZE B", right: true},
		{name: "PAIRS", right: true},
		{name: "RELEASE", right: true},
	}, rows)
	fmt.Fprintf(w, "\n合计 %.2f USDC\n", plan.Total)
	if plan.Blocked > 0 {
		fmt.Fprintf(w, "另有 %d 个市场两边都持有但当前不可合并。\n", plan.Blocked)
	}
}

func Check(w io.Writer, wallet string, diffs []Diff, legs int, tolerance float64) {
	fmt.Fprintf(w, "链上校验 %s\n", wallet)
	if len(diffs) == 0 {
		fmt.Fprintf(w, "%d 条持仓腿与链上余额一致（容差 %.6f）。\n", legs, tolerance)
		return
	}
	fmt.Fprintf(w, "%d 条持仓腿中 %d 条与链上余额不符（容差 %.6f）\n\n", legs, len(diffs), tolerance)

	rows := make([][]string, 0, len(diffs))
	for _, d := range diffs {
		rows = append(rows, []string{
			title(d.Title),
			clip(d.Outcome, outcomeWidth),
			short(d.TokenID),
			size(d.API),
			size(d.Chain),
			fmt.Sprintf("%+.4f", d.Delta),
		})
	}
	table(w, []col{
		{name: "MARKET"},
		{name: "OUTCOME"},
		{name: "TOKEN"},
		{name: "API", right: true},
		{name: "CHAIN", right: true},
		{name: "DELTA", right: true},
	}, rows)
	fmt.Fprintln(w, "\nAPI 小于链上：可能刚成交、索引未追上；API 大于链上：多为已赎回但接口缓存未刷新。")
}

func History(w io.Writer, wallet string, h Ledger, all bool) {
	fmt.Fprintf(w, "成交对账 %s\n", wallet)
	if len(h.Legs) == 0 {
		fmt.Fprintln(w, "该钱包没有成交记录，也没有持仓。")
		return
	}
	if h.Truncated {
		fmt.Fprintln(w, "注意：成交只覆盖接口分页上限内的最近记录，更早的成交没有参与对账，差异可能来自这里。")
	}
	fmt.Fprintf(w, "%d 条腿：买入 %.2f 份额 / %.2f USDC，卖出 %.2f 份额 / %.2f USDC\n",
		len(h.Legs), h.Bought, h.BuyCost, h.Sold, h.SellProfit)
	fmt.Fprintf(w, "持仓可解释 %d 条，待确认 %d 条（其中 %d 条无成交记录），已清仓 %d 条\n\n",
		h.Matched, h.Mismatched, h.UnmatchedPos, h.Closed)

	if h.Mismatched == 0 && !all {
		fmt.Fprintln(w, "当前持仓都能被成交记录解释。加 -all 可看含已清仓腿的完整流水。")
		return
	}

	rows := make([][]string, 0, len(h.Legs))
	for _, l := range h.Legs {
		if !all && l.Held == 0 {
			continue
		}
		rows = append(rows, []string{
			label(l.Title, l.ConditionID),
			clip(l.Outcome, outcomeWidth),
			size(l.Bought),
			size(l.Sold),
			size(l.Net()),
			size(l.Held),
			fmt.Sprintf("%+.4f", l.Net()-l.Held),
			l.Note,
		})
	}
	table(w, []col{
		{name: "MARKET"},
		{name: "OUTCOME"},
		{name: "BOUGHT", right: true},
		{name: "SOLD", right: true},
		{name: "NET", right: true},
		{name: "HELD", right: true},
		{name: "DELTA", right: true},
		{name: "NOTE"},
	}, rows)
}

func WatchStart(w io.Writer, wallet string, interval time.Duration, rounds int) {
	scope := "不限轮数"
	if rounds > 0 {
		scope = fmt.Sprintf("%d 轮", rounds)
	}
	fmt.Fprintf(w, "盯盘 %s，每 %s 一轮，%s\n", wallet, interval, scope)
}

func WatchRound(w io.Writer, s Snapshot, changes []string, round int) {
	fmt.Fprintf(w, "[%s] #%d 市值 %.2f 市场 %d 待赎回 %d腿/%.2f 可合并 %.2f\n",
		s.At.Format("15:04:05"), round, s.Value, s.MarketCount, s.RedeemLegs, s.RedeemPayout, s.MergeRelease)
	for _, c := range changes {
		fmt.Fprintf(w, "    %s\n", c)
	}
}

func short(id string) string {
	if len(id) > 12 {
		return id[:6] + ".." + id[len(id)-4:]
	}
	return id
}
