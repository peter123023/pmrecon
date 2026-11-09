package polymarket

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultBaseURL  = "https://data-api.polymarket.com"
	DefaultPageSize = 500
	maxPages        = 40
)

// 这两个错误值得单独区分：v1 下线是永久性的，重试没有意义。
var (
	ErrRetired = errors.New("Data API v1 已下线，本客户端只支持 /v2")
	// ErrTruncated 与已取到的数据一起返回：调用方可以继续用部分结果，但必须自己说明覆盖范围。
	ErrTruncated = errors.New("已到分页上限，结果只覆盖最近的记录")
)

type Client struct {
	BaseURL  string
	PageSize int
	HTTP     *http.Client
	// Backoff 在 429/5xx 时被调用，测试里替换成空实现以消除等待。
	Backoff func(attempt int) time.Duration
}

func New() *Client {
	return &Client{
		BaseURL:  DefaultBaseURL,
		PageSize: DefaultPageSize,
		HTTP:     &http.Client{Timeout: 30 * time.Second},
		Backoff:  func(n int) time.Duration { return time.Duration(1<<uint(n)) * 300 * time.Millisecond },
	}
}

type page[T any] struct {
	Data       []T `json:"data"`
	Pagination struct {
		HasMore    bool   `json:"has_more"`
		NextCursor string `json:"next_cursor"`
	} `json:"pagination"`
}

// Positions 拉取钱包全部仓位。接口只支持游标分页，offset 会直接 400。
// 超过分页上限时和 Trades 一样返回 ErrTruncated：仓位被截断会让 redeem 少算可回收金额，
// 静默返回部分结果比报错更危险。
func (c *Client) Positions(ctx context.Context, user string) ([]Position, error) {
	q := url.Values{}
	q.Set("user", user)
	return paginate[Position](ctx, c, "/v2/positions", q)
}

// Trades 拉取钱包全部成交。同一分页契约，但成交量远大于持仓，maxPages 会先撞到上限。
func (c *Client) Trades(ctx context.Context, user string) ([]Trade, error) {
	q := url.Values{}
	q.Set("user", user)
	return paginate[Trade](ctx, c, "/v2/trades", q)
}

func paginate[T any](ctx context.Context, c *Client, path string, q url.Values) ([]T, error) {
	user := q.Get("user")
	if !ValidAddress(user) {
		return nil, fmt.Errorf("钱包地址不合法: %q", user)
	}
	q.Set("limit", strconv.Itoa(c.PageSize))

	var all []T
	cursor := ""
	for i := 0; i < maxPages; i++ {
		qs := q.Encode()
		if cursor != "" {
			qs += "&cursor=" + url.QueryEscape(cursor)
		}
		var p page[T]
		if err := c.get(ctx, path+"?"+qs, &p); err != nil {
			return nil, err
		}
		all = append(all, p.Data...)
		if !p.Pagination.HasMore || p.Pagination.NextCursor == "" {
			return all, nil
		}
		cursor = p.Pagination.NextCursor
	}
	return all, ErrTruncated
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	var lastErr error
	for attempt := 0; attempt < 4; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(c.Backoff(attempt)):
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Accept", "application/json")

		resp, err := c.HTTP.Do(req)
		if err != nil {
			return err
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		resp.Body.Close()
		if err != nil {
			return err
		}

		switch {
		case resp.StatusCode == http.StatusOK:
			if err := json.Unmarshal(body, out); err != nil {
				return fmt.Errorf("解析 %s 失败: %w", path, err)
			}
			return nil
		case resp.StatusCode == http.StatusGone:
			return ErrRetired
		case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
			lastErr = fmt.Errorf("%s 返回 %s: %s", path, resp.Status, snippet(body))
			continue
		default:
			return fmt.Errorf("%s 返回 %s: %s", path, resp.Status, snippet(body))
		}
	}
	return lastErr
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if r := []rune(s); len(r) > 200 {
		return string(r[:200]) + "..."
	}
	return s
}

// ValidAddress 只做格式校验：0x + 40 个十六进制字符。
func ValidAddress(s string) bool {
	if len(s) != 42 || s[0] != '0' || s[1] != 'x' {
		return false
	}
	for _, r := range s[2:] {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f', r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return true
}
