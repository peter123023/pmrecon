package chain

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 这组 calldata 与金额都是在 Polygon 主网 CTF 合约上实跑过的，改动编码逻辑会让它失配。
const (
	goldenOwner = "0x9cb98fc57fe8a3bdf75c3b48bd419e5d262f7b80"
	goldenToken = "94476829201604408463453426454480212459887267917122244941405244686637914508323"
	goldenCall  = "0x4e1273f4" +
		"0000000000000000000000000000000000000000000000000000000000000040" +
		"0000000000000000000000000000000000000000000000000000000000000080" +
		"0000000000000000000000000000000000000000000000000000000000000001" +
		"0000000000000000000000009cb98fc57fe8a3bdf75c3b48bd419e5d262f7b80" +
		"0000000000000000000000000000000000000000000000000000000000000001" +
		"d0dffd87b8698c79edca896f3c9b40f55e86efc6e009a66104ddbab7cd81cc23"
)

func TestEncodeBalanceOfBatchMatchesChain(t *testing.T) {
	got, err := encodeBalanceOfBatch(goldenOwner, []string{goldenToken})
	if err != nil {
		t.Fatal(err)
	}
	if got != goldenCall {
		t.Fatalf("calldata mismatch\n got %s\nwant %s", got, goldenCall)
	}
}

func TestEncodeBalanceOfBatchRejectsBadInput(t *testing.T) {
	if _, err := encodeBalanceOfBatch("0x1234", []string{"1"}); err == nil {
		t.Error("short owner accepted")
	}
	if _, err := encodeBalanceOfBatch(goldenOwner, []string{"not-a-number"}); err == nil {
		t.Error("non-numeric token id accepted")
	}
}

func TestDecodeUintArray(t *testing.T) {
	raw, err := hex.DecodeString("0000000000000000000000000000000000000000000000000000000000000020" +
		"0000000000000000000000000000000000000000000000000000000000000002" +
		"00000000000000000000000000000000000000000000000000000000ee6b2800" +
		"0000000000000000000000000000000000000000000000000000000000000001")
	if err != nil {
		t.Fatal(err)
	}
	vals, err := decodeUintArray(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(vals) != 2 || vals[0].Cmp(big.NewInt(4000000000)) != 0 || vals[1].Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("decoded %v", vals)
	}
	if _, err := decodeUintArray(raw[:40]); err == nil {
		t.Error("truncated data accepted")
	}
}

func TestBalancesConvertsSixDecimals(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req rpcRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("bad request: %v", err)
		}
		if req.Method != "eth_call" {
			t.Errorf("method = %s", req.Method)
		}
		params := req.Params[0].(map[string]any)
		if params["to"] != CTFAddress {
			t.Errorf("to = %v", params["to"])
		}
		if !strings.HasPrefix(params["data"].(string), selectorBalanceOfBatch) {
			t.Errorf("data selector = %v", params["data"])
		}
		w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x` +
			"0000000000000000000000000000000000000000000000000000000000000020" +
			"0000000000000000000000000000000000000000000000000000000000000001" +
			"00000000000000000000000000000000000000000000000000000000ee6b2800" + `"}`))
	}))
	defer srv.Close()

	c := New()
	c.RPCURL = srv.URL
	bal, err := c.Balances(context.Background(), goldenOwner, []string{goldenToken})
	if err != nil {
		t.Fatal(err)
	}
	if bal[goldenToken] != 4000 {
		t.Fatalf("balance = %v, want 4000", bal[goldenToken])
	}
}

func TestBalancesSurfacesRevert(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":3,"message":"execution reverted"}}`))
	}))
	defer srv.Close()

	c := New()
	c.RPCURL = srv.URL
	_, err := c.Balances(context.Background(), goldenOwner, []string{goldenToken})
	if err == nil || !strings.Contains(err.Error(), "execution reverted") {
		t.Fatalf("err = %v", err)
	}
}
