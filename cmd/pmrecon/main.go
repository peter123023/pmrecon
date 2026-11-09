package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/peter123023/pmrecon/internal/chain"
	"github.com/peter123023/pmrecon/internal/polymarket"
	"github.com/peter123023/pmrecon/internal/recon"
)

const usage = `pmrecon - Polymarket 仓位与赎回对账（只读，不接触私钥）

用法:
  pmrecon positions <wallet>          按市场聚合的持仓、成本与未实现盈亏
  pmrecon redeem    <wallet>          可赎回仓位与预计回收 USDC
  pmrecon merge     <wallet>          互补份额可合并对与可释放 USDC
  pmrecon check     <wallet>          链上 CTF 余额与接口仓位逐条比对
  pmrecon history   <wallet>          成交流水与当前持仓对账（找出转入、合并、赎回缺口）
  pmrecon watch     <wallet>          轮询盯盘，只在状态变化时展开细节

参数:
  history 支持 -all 输出完整流水
  watch   支持 -interval 轮询间隔（默认 1m）与 -rounds 轮数（默认 0，一直跑）
  check   支持 -rpc <url> 指定 Polygon JSON-RPC，-tol <shares> 设定比对容差（默认 0.001）

退出码:
  0 正常  1 出错  2 参数错误或 check 发现差异
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	cmd := os.Args[1]

	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	rpcURL := fs.String("rpc", chain.DefaultRPCURL, "Polygon JSON-RPC endpoint")
	tolerance := fs.Float64("tol", 0.001, "check 的份额比对容差")
	all := fs.Bool("all", false, "history 输出完整流水")
	interval := fs.Duration("interval", time.Minute, "watch 的轮询间隔")
	rounds := fs.Int("rounds", 0, "watch 的轮数，0 表示一直跑")
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage) }

	switch cmd {
	case "positions", "redeem", "merge", "check", "history", "watch":
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err := fs.Parse(splitArgs(fs, os.Args[2:])); err != nil {
		os.Exit(2)
	}
	if fs.NArg() != 1 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	wallet := fs.Arg(0)
	if !polymarket.ValidAddress(wallet) {
		fmt.Fprintf(os.Stderr, "钱包地址格式不对: %s\n", wallet)
		os.Exit(2)
	}
	// 容差为负会让每条腿都判成不一致；间隔过小会把接口打成限流。
	if *tolerance < 0 {
		fmt.Fprintln(os.Stderr, "-tol 不能为负")
		os.Exit(2)
	}
	if *interval < time.Second {
		fmt.Fprintln(os.Stderr, "-interval 至少 1s")
		os.Exit(2)
	}
	if *rounds < 0 {
		fmt.Fprintln(os.Stderr, "-rounds 不能为负，0 表示一直跑")
		os.Exit(2)
	}

	base, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	api := polymarket.New()
	if cmd == "watch" {
		if err := runWatch(base, api, wallet, *interval, *rounds); err != nil {
			fmt.Fprintf(os.Stderr, "盯盘失败: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// history 要把成交翻到分页上限，请求数是别的命令的几倍，给更宽的时间。
	timeout := 90 * time.Second
	if cmd == "history" {
		timeout = 180 * time.Second
	}
	ctx, cancel := context.WithTimeout(base, timeout)
	defer cancel()

	positions, err := api.Positions(ctx, wallet)
	if err != nil {
		fmt.Fprintf(os.Stderr, "拉取仓位失败: %v\n", err)
		os.Exit(1)
	}
	markets := recon.Group(positions)

	switch cmd {
	case "positions":
		recon.Pos(os.Stdout, wallet, markets, recon.Sum(positions, markets))
	case "redeem":
		recon.Redeem(os.Stdout, wallet, recon.PlanRedeem(markets))
	case "merge":
		recon.Merge(os.Stdout, wallet, recon.PlanMerge(markets))
	case "history":
		trades, err := api.Trades(ctx, wallet)
		truncated := errors.Is(err, polymarket.ErrTruncated)
		if err != nil && !truncated {
			fmt.Fprintf(os.Stderr, "拉取成交失败: %v\n", err)
			os.Exit(1)
		}
		ledger := recon.Reconcile(trades, markets, *tolerance)
		ledger.Truncated = truncated
		recon.History(os.Stdout, wallet, ledger, *all)
	case "check":
		ids := tokenIDs(markets)
		c := chain.New()
		c.RPCURL = *rpcURL
		balances, err := c.Balances(ctx, wallet, ids)
		if err != nil {
			fmt.Fprintf(os.Stderr, "读取链上余额失败: %v\n", err)
			os.Exit(1)
		}
		diffs := recon.Compare(markets, balances, *tolerance)
		recon.Check(os.Stdout, wallet, diffs, len(ids), *tolerance)
		if len(diffs) > 0 {
			os.Exit(2)
		}
	}
}

// splitArgs 让 flag 和位置参数可以任意穿插：标准库的 flag 遇到第一个非 flag 就停止解析，
// 而 `watch <wallet> -rounds 2` 这种写法更符合直觉。
func splitArgs(fs *flag.FlagSet, args []string) []string {
	var flags, positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "" || a == "-" || a[0] != '-' {
			positional = append(positional, a)
			continue
		}
		flags = append(flags, a)
		name := strings.TrimLeft(a, "-")
		if strings.Contains(name, "=") || i+1 >= len(args) {
			continue
		}
		f := fs.Lookup(name)
		if f == nil || !takesValue(f) {
			continue
		}
		flags = append(flags, args[i+1])
		i++
	}
	return append(flags, positional...)
}

// takesValue 判断 flag 是否要跟一个值：bool flag 不跟，否则 `-all 0xabc`
// 会把钱包地址吃掉。
func takesValue(f *flag.Flag) bool {
	_, isBool := f.Value.(interface{ IsBoolFlag() bool })
	return !isBool
}

func runWatch(ctx context.Context, api *polymarket.Client, wallet string, interval time.Duration, rounds int) error {
	recon.WatchStart(os.Stdout, wallet, interval, rounds)
	var prev recon.Snapshot
	fails := 0
	for i := 1; rounds == 0 || i <= rounds; i++ {
		positions, err := api.Positions(ctx, wallet)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			// 长时间盯盘时一次抖动不该结束任务：连续失败 3 次才放弃，失败轮不占轮数。
			fails++
			fmt.Fprintf(os.Stdout, "[%s] #%d 拉取失败 %d/3: %v\n", time.Now().Format("15:04:05"), i, fails, err)
			if fails >= 3 {
				return err
			}
			if !sleepCtx(ctx, interval) {
				return nil
			}
			i--
			continue
		}
		fails = 0
		cur := recon.TakeSnapshot(recon.Group(positions), positions, time.Now())
		var changes []string
		if i > 1 {
			changes = cur.Diff(prev)
		}
		recon.WatchRound(os.Stdout, cur, changes, i)
		prev = cur

		if rounds != 0 && i == rounds {
			return nil
		}
		if !sleepCtx(ctx, interval) {
			return nil
		}
	}
	return nil
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

func tokenIDs(ms []recon.Market) []string {
	seen := make(map[string]bool)
	var ids []string
	for _, m := range ms {
		for _, l := range m.Legs {
			if l.Size == 0 || seen[l.TokenID] {
				continue
			}
			seen[l.TokenID] = true
			ids = append(ids, l.TokenID)
		}
	}
	return ids
}
