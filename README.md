# pmrecon

[English](README.en.md)

Polymarket 仓位对账工具：把官网持仓页看不出问题的几件事算清楚——哪些仓位已经结算但还没赎回、哪些 YES/NO 互补份额可以合并释放资金、接口显示的份额和链上 CTF 余额是否一致。

只读。不读私钥，不签名，不发交易。赎回和合并只输出计划，执行仍然要你自己去做。

## 装

```bash
make build      # -> bin/pmrecon
make test
```

只用到标准库。Go 1.22+。

## 用

```bash
pmrecon positions 0x9cb98fc57fe8a3bdf75c3b48bd419e5d262f7b80
pmrecon redeem    0x9cb98fc57fe8a3bdf75c3b48bd419e5d262f7b80
pmrecon merge     0x9cb98fc57fe8a3bdf75c3b48bd419e5d262f7b80
pmrecon check     0x9cb98fc57fe8a3bdf75c3b48bd419e5d262f7b80
pmrecon history   0x9cb98fc57fe8a3bdf75c3b48bd419e5d262f7b80
pmrecon watch     0x9cb98fc57fe8a3bdf75c3b48bd419e5d262f7b80 -interval 60s
```

- `positions` 按市场（condition）聚合持仓腿，给出成本、市值、未实现盈亏。同一市场只持有一边时平铺看容易漏，聚合后才看得出实际敞口。
- `redeem` 挑出 `redeemable=true` 的仓位，按市场分组，标出走 NegRiskAdapter 还是标准 CTF 路径，并汇总可回收 USDC。注意回收金额按 `current_value` 算：输掉的那一腿价格为 0，赎回拿回 0，但仍然要赎回，否则仓位会一直挂在列表里。
- `merge` 找同一市场里的两条互补腿——不一定是 Yes/No，Up/Down 这类市场同样适用。互补份额在 CTF 里恒定值 1 USDC，合并即把锁死的资金换回来。只统计接口标记 `mergeable=true` 的腿；两边都持有但当前不可合并的会单独提示，不会当成可释放资金。
- `check` 直接读 Polygon 上 CTF 合约的 `balanceOfBatch`，和接口的 `current_size` 逐条比对。份额精度是 6 位，默认容差 0.001。有差异时退出码为 2，方便放进定时任务。
- `history` 用成交记录反推每条腿的净份额（买入 - 卖出），和当前持仓对齐。对不上只有几种原因：转入、合并、赎回，而这三件事都不会留下 SELL 记录。已清仓的腿单独计数，不混进"待确认"，否则老账户的历史会淹没结论。
- `watch` 反复采样，只在市值、待赎回、可合并、市场增删有变化时展开细节。切到别的窗口挂着，等结算或等合并窗口出现时回来看。

参数：

```
check   -rpc https://polygon-bor-rpc.publicnode.com -tol 0.001
history -all                       # 连已清仓的腿一起看
watch   -interval 30s -rounds 20   # 默认 1m、不限轮数，Ctrl-C 退出
```

flag 和位置参数可以任意顺序，`watch <wallet> -rounds 2` 和 `watch -rounds 2 <wallet>` 等价。

## 网络

Data API 在部分网络环境下访问不了，需要代理：

```bash
export https_proxy=http://127.0.0.1:7897 http_proxy=http://127.0.0.1:7897
```

RPC 节点通常不需要代理，所以 `check` 的链上请求走直连，不继承上面的代理设置——两边可达性正好相反，共用代理反而容易把能用的那条路堵死。

## 数据来源

全部来自 `https://data-api.polymarket.com` 的 v2 接口。v1 已经下线（返回 410），字段是 snake_case：

- `GET /v2/positions?user=<addr>&limit=500` → `{"data":[...], "pagination":{...}}`
- `GET /v2/trades?user=<addr>&limit=500` → 同一套分页契约，字段多出 `side`、`price`、`timestamp`、`transaction_hash`

分页只支持游标，`offset` 会返回 400 并提示传 `cursor`。工具内部按 `pagination.next_cursor` 一路翻到底，上限 40 页（500 条/页）。成交远多于持仓，撞到上限时 `history` 会在开头标注只覆盖最近记录。

链上读 `ConditionalTokens 0x4D97DCd97eC945f40cF65F87097ACe5EA0476045`（Polygon 主网），`balanceOfBatch` 一次最多查 200 个 token。

## 已知局限

- `status` 目前只观察到 `OPEN` 和 `REDEEMABLE` 两种取值，其余状态按原样展示，不做解释。
- `/v2/holders` 返回 403，所以没有做"市场持有人"维度的分析。
- `merge` 只按 `min(腿 A, 腿 B)` 估算可合并对，且只认 `mergeable=true`；实际能否操作以 Polymarket 页面为准。
- `check` 用的 `uint256[]` 解码只覆盖 `balanceOfBatch` 的返回形状，不是通用 ABI 解码器。
- `history` 不做跨钱包归因：判定为"转入"只说明本地成交解释不了，不代表一定有转账。
- `watch` 是内存态的前后差分，重启就丢基线，也不写任何持久化文件。
- 没有做重试以外的限流控制；一次查询默认只请求 1～40 次。

## 许可

MIT，见 [LICENSE](LICENSE)。
