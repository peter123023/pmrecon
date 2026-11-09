# pmrecon

[中文](README.md)

A position reconciliation tool for Polymarket. It answers the three questions the portfolio page doesn't: which positions have already settled but were never redeemed, where you hold both sides of a complementary pair and can merge to free up cash, and whether the share counts reported by the API actually match your on-chain CTF balance.

Read-only. No private keys, no signing, no transactions. `redeem` and `merge` only print a plan — execution is still on you.

## Install

```bash
make build      # -> bin/pmrecon
make test
```

Standard library only. Go 1.22+.

## Usage

```bash
pmrecon positions 0x9cb98fc57fe8a3bdf75c3b48bd419e5d262f7b80
pmrecon redeem    0x9cb98fc57fe8a3bdf75c3b48bd419e5d262f7b80
pmrecon merge     0x9cb98fc57fe8a3bdf75c3b48bd419e5d262f7b80
pmrecon check     0x9cb98fc57fe8a3bdf75c3b48bd419e5d262f7b80
pmrecon history   0x9cb98fc57fe8a3bdf75c3b48bd419e5d262f7b80
pmrecon watch     0x9cb98fc57fe8a3bdf75c3b48bd419e5d262f7b80 -interval 60s
```

- `positions` groups position legs by market (condition) and reports cost, market value and unrealized PnL. A flat list of legs hides the fact that you may be long two sides of the same market; grouping shows the real exposure.
- `redeem` picks positions with `redeemable=true`, groups them by market, marks whether they go through NegRiskAdapter or the standard CTF contract, and totals the recoverable USDC. Payout is computed from `current_value`: the losing leg is priced at 0 and returns 0 on redemption, but you still have to redeem it or the position stays in the list forever.
- `merge` finds the two complementary legs of the same market — not necessarily Yes/No, since Up/Down markets work the same way. A complementary pair is worth exactly 1 USDC in the CTF, so merging turns locked collateral back into cash. Only legs the API marks `mergeable=true` are counted; markets where you hold both sides but can't merge right now are reported separately instead of being counted as recoverable.
- `check` reads `balanceOfBatch` straight from the CTF contract on Polygon and compares it against `current_size` from the API, leg by leg. Shares have 6 decimals, so the default tolerance is 0.001. Exits with code 2 on any mismatch, which makes it safe to drop into a cron job.
- `history` derives the net shares of each leg from trade history (buys minus sells) and reconciles that against current holdings. When the two disagree there are only a few causes — transfers in, merges, redemptions — and none of them leave a SELL record behind. Fully closed legs are counted separately rather than mixed into "unexplained", otherwise an old account's history drowns the conclusion.
- `watch` samples repeatedly and only expands details when market value, redeemable payout, mergeable release or the set of markets changes. Park it in another window and come back when something settles or a merge window opens.

Flags:

```
check   -rpc https://polygon-bor-rpc.publicnode.com -tol 0.001
history -all                       # include legs that were already closed out
watch   -interval 30s -rounds 20   # defaults: 1m, unlimited rounds, Ctrl-C to stop
```

Flags and positional arguments can appear in any order, so `watch <wallet> -rounds 2` and `watch -rounds 2 <wallet>` are equivalent.

## Network

The Data API is not reachable from every network and needs a proxy:

```bash
export https_proxy=http://127.0.0.1:7897 http_proxy=http://127.0.0.1:7897
```

The RPC endpoint usually doesn't, so the on-chain requests in `check` bypass the proxy instead of inheriting it. The two endpoints have opposite reachability, and sharing one proxy setting is a good way to block the path that actually works.

## Data sources

Everything comes from the v2 API at `https://data-api.polymarket.com`. v1 is retired and returns 410. Field names are snake_case:

- `GET /v2/positions?user=<addr>&limit=500` → `{"data":[...], "pagination":{...}}`
- `GET /v2/trades?user=<addr>&limit=500` → same pagination contract, with extra `side`, `price`, `timestamp` and `transaction_hash` fields

Pagination is cursor-only; passing `offset` returns 400 with a message telling you to send `cursor`. The client follows `pagination.next_cursor` to the end, capped at 40 pages of 500 records. Trades vastly outnumber positions, so when the cap is hit `history` says up front that it only covers recent records.

On-chain reads target `ConditionalTokens 0x4D97DCd97eC945f40cF65F87097ACe5EA0476045` on Polygon mainnet. `balanceOfBatch` takes at most 200 tokens per call.

## Known limitations

- Only `OPEN` and `REDEEMABLE` have been observed for `status`. Anything else is printed as-is without interpretation.
- `/v2/holders` returns 403, so there is no holder-level analysis.
- `merge` estimates pairs as `min(leg A, leg B)` and only trusts `mergeable=true`. Whether a merge actually goes through is up to the Polymarket UI.
- The `uint256[]` decoder in `check` only handles the shape `balanceOfBatch` returns; it is not a general ABI decoder.
- `history` makes no cross-wallet attribution: "transferred in" only means the local trade history can't explain it, not that a transfer definitely happened.
- `watch` diffs against an in-memory baseline, so restarting loses the baseline. Nothing is persisted.
- No rate limiting beyond retries; a single query issues between 1 and 40 requests.

## License

MIT, see [LICENSE](LICENSE).
