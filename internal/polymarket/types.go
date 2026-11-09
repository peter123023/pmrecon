package polymarket

// Position 是 data-api /v2/positions 单条记录的子集，字段名与线上返回保持一致（snake_case）。
// 只保留对账需要的字段：金额类字段单位均为 USDC（6 位精度），size 单位为份额。
type Position struct {
	TokenID     string `json:"token_id"`
	ConditionID string `json:"condition_id"`

	CurrentSize  float64 `json:"current_size"`
	TotalCost    float64 `json:"total_cost_usdc"`
	CurrentPrice float64 `json:"current_price"`
	CurrentValue float64 `json:"current_value"`

	RealizedPnL   float64 `json:"realized_pnl"`
	UnrealizedPnL float64 `json:"unrealized_pnl"`

	Status       string `json:"status"`
	Redeemable   bool   `json:"redeemable"`
	Mergeable    bool   `json:"mergeable"`
	NegativeRisk bool   `json:"negative_risk"`

	Title      string `json:"title"`
	Outcome    string `json:"outcome"`
	OppositeID string `json:"opposite_token_id"`
	EndDate    string `json:"end_date"`
}

// Trade 是 /v2/trades 的单条成交。与仓位不同，成交是按 token 逐笔记录的，
// 因此可以把「买过多少、卖过多少」和「现在还持有多少」对齐。
type Trade struct {
	Side        string  `json:"side"`
	Size        float64 `json:"size"`
	Price       float64 `json:"price"`
	TokenID     string  `json:"token_id"`
	ConditionID string  `json:"condition_id"`
	Outcome     string  `json:"outcome"`
	Timestamp   int64   `json:"timestamp"`
	Title       string  `json:"title"`
}
