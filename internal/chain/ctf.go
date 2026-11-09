package chain

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ConditionalTokens 是 Polymarket 使用的 ERC-1155 仓位合约，Polygon 主网。
const (
	CTFAddress             = "0x4D97DCd97eC945f40cF65F87097ACe5EA0476045"
	DefaultRPCURL          = "https://polygon-bor-rpc.publicnode.com"
	selectorBalanceOfBatch = "0x4e1273f4" // balanceOfBatch(address[],uint256[])
	maxTokensPerCall       = 200
	maxUintArrayLen        = 10000
)

type Client struct {
	RPCURL string
	HTTP   *http.Client
}

func New() *Client {
	return &Client{
		RPCURL: DefaultRPCURL,
		// RPC 走直连：Polygon 节点通常不需要代理，而 Data API 在被墙的环境里必须走代理，
		// 两者的可达性模式经常相反，所以不共用 transport 的代理设置。
		HTTP: &http.Client{
			Timeout:   30 * time.Second,
			Transport: &http.Transport{Proxy: func(*http.Request) (*url.URL, error) { return nil, nil }},
		},
	}
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	Result string    `json:"result"`
	Error  *rpcError `json:"error"`
}

// Balances 读取 owner 在 CTF 合约上的各 token 余额，单位换算回份额（去掉 6 位精度）。
// 返回的 map 只包含链上非零余额，缺失即视为 0。
func (c *Client) Balances(ctx context.Context, owner string, tokenIDs []string) (map[string]float64, error) {
	out := make(map[string]float64, len(tokenIDs))
	for start := 0; start < len(tokenIDs); start += maxTokensPerCall {
		end := min(start+maxTokensPerCall, len(tokenIDs))
		chunk := tokenIDs[start:end]
		call, err := encodeBalanceOfBatch(owner, chunk)
		if err != nil {
			return nil, err
		}
		raw, err := c.ethCall(ctx, CTFAddress, call)
		if err != nil {
			return nil, err
		}
		vals, err := decodeUintArray(raw)
		if err != nil {
			return nil, err
		}
		if len(vals) != len(chunk) {
			return nil, fmt.Errorf("balanceOfBatch 返回 %d 个值，但请求了 %d 个 token", len(vals), len(chunk))
		}
		for i, v := range vals {
			f, _ := new(big.Float).Quo(new(big.Float).SetInt(v), big.NewFloat(1e6)).Float64()
			if f != 0 {
				out[chunk[i]] = f
			}
		}
	}
	return out, nil
}

// ethCall 失败时重试两次。公共 RPC 节点抖动很常见，而 check 一次要发多个分片请求，
// 任何一个分片失败都会让整轮对账作废。
func (c *Client) ethCall(ctx context.Context, to, data string) ([]byte, error) {
	body, err := json.Marshal(rpcRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "eth_call",
		Params:  []any{map[string]string{"to": to, "data": data}, "latest"},
	})
	if err != nil {
		return nil, err
	}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt) * 500 * time.Millisecond):
			}
		}
		raw, retry, err := c.callOnce(ctx, body)
		if err == nil {
			return raw, nil
		}
		lastErr = err
		if !retry {
			return nil, err
		}
	}
	return nil, lastErr
}

// callOnce 返回 retry=true 表示这轮失败值得重试（网络错误、429、5xx）；
// 合约 revert 或 JSON-RPC 业务错误重试也不会变好。
func (c *Client) callOnce(ctx context.Context, body []byte) ([]byte, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.RPCURL, bytes.NewReader(body))
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, true, err
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, true, err
	}
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return nil, true, fmt.Errorf("rpc 返回 %s: %s", resp.Status, strings.TrimSpace(string(payload)))
	}
	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("rpc 返回 %s: %s", resp.Status, strings.TrimSpace(string(payload)))
	}
	var rr rpcResponse
	if err := json.Unmarshal(payload, &rr); err != nil {
		return nil, true, fmt.Errorf("解析 rpc 响应失败: %w", err)
	}
	if rr.Error != nil {
		return nil, false, fmt.Errorf("rpc 错误 %d: %s", rr.Error.Code, rr.Error.Message)
	}
	raw, err := hex.DecodeString(strings.TrimPrefix(rr.Result, "0x"))
	if err != nil {
		return nil, false, err
	}
	return raw, false, nil
}

func encodeBalanceOfBatch(owner string, tokenIDs []string) (string, error) {
	addr, err := hex.DecodeString(strings.TrimPrefix(owner, "0x"))
	if err != nil || len(addr) != 20 {
		return "", errors.New("owner 必须是 20 字节的十六进制地址")
	}

	var buf bytes.Buffer
	selector, err := hex.DecodeString(strings.TrimPrefix(selectorBalanceOfBatch, "0x"))
	if err != nil {
		return "", err
	}
	buf.Write(selector)

	n := len(tokenIDs)
	// 两个动态参数：头部先放各自相对数据区起点的偏移。
	writeWord(&buf, big.NewInt(0x40))
	writeWord(&buf, big.NewInt(int64(0x40+32*(n+1))))

	writeWord(&buf, big.NewInt(int64(n)))
	ownerWord := make([]byte, 32)
	copy(ownerWord[12:], addr)
	for i := 0; i < n; i++ {
		buf.Write(ownerWord)
	}

	writeWord(&buf, big.NewInt(int64(n)))
	for _, id := range tokenIDs {
		v, ok := new(big.Int).SetString(id, 10)
		if !ok {
			return "", fmt.Errorf("token id %q 不是十进制整数", id)
		}
		writeWord(&buf, v)
	}
	return "0x" + hex.EncodeToString(buf.Bytes()), nil
}

func writeWord(buf *bytes.Buffer, v *big.Int) {
	word := make([]byte, 32)
	b := v.Bytes()
	if len(b) > 32 {
		b = b[len(b)-32:]
	}
	copy(word[32-len(b):], b)
	buf.Write(word)
}

func decodeUintArray(raw []byte) ([]*big.Int, error) {
	if len(raw) < 64 {
		return nil, fmt.Errorf("返回数据过短：%d 字节", len(raw))
	}
	offset := new(big.Int).SetBytes(raw[:32]).Int64()
	if offset < 0 || offset+32 > int64(len(raw)) {
		return nil, errors.New("返回数据偏移越界")
	}
	length := new(big.Int).SetBytes(raw[offset : offset+32]).Int64()
	// 不设上限的话，一个畸形 length 就能让下面的 make 直接申请几 GB 内存。
	if length < 0 || length > maxUintArrayLen {
		return nil, fmt.Errorf("返回数组长度异常：%d", length)
	}
	if total := offset + 32 + 32*length; total > int64(len(raw)) {
		return nil, fmt.Errorf("返回数据不完整：需要 %d 字节，实际 %d", total, len(raw))
	}
	vals := make([]*big.Int, length)
	for i := int64(0); i < length; i++ {
		start := offset + 32 + 32*i
		vals[i] = new(big.Int).SetBytes(raw[start : start+32])
	}
	return vals, nil
}
