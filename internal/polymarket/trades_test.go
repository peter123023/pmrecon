package polymarket

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTradesParsesAndPaginates(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if got := r.URL.Query().Get("user"); got != "0x9cb98fc57fe8a3bdf75c3b48bd419e5d262f7b80" {
			t.Errorf("user = %q", got)
		}
		if calls == 1 {
			w.Write([]byte(`{"data":[{"side":"BUY","size":18,"price":0.0676,"token_id":"1","condition_id":"0xa","outcome":"Yes","timestamp":1794244291}],
				"pagination":{"has_more":true,"next_cursor":"c2"}}`))
			return
		}
		w.Write([]byte(`{"data":[{"side":"SELL","size":5,"price":0.4,"token_id":"1","condition_id":"0xa","outcome":"Yes","timestamp":1794244391}],
			"pagination":{"has_more":false}}`))
	}))
	defer srv.Close()

	ts, err := testClient(srv).Trades(context.Background(), "0x9cb98fc57fe8a3bdf75c3b48bd419e5d262f7b80")
	if err != nil {
		t.Fatal(err)
	}
	if len(ts) != 2 {
		t.Fatalf("trades = %d, want 2", len(ts))
	}
	if ts[0].Side != "BUY" || ts[0].Size != 18 || ts[0].Price != 0.0676 {
		t.Errorf("first trade = %+v", ts[0])
	}
	if ts[1].Side != "SELL" || ts[1].Timestamp != 1794244391 {
		t.Errorf("second trade = %+v", ts[1])
	}
}

func TestTradesRejectsBadAddress(t *testing.T) {
	c := New()
	if _, err := c.Trades(context.Background(), "0x123"); err == nil {
		t.Fatal("expected error for bad address")
	}
}
