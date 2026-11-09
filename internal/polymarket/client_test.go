package polymarket

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testClient(srv *httptest.Server) *Client {
	c := New()
	c.BaseURL = srv.URL
	c.Backoff = func(int) time.Duration { return 0 }
	return c
}

func TestPositionsParsesRealPage(t *testing.T) {
	body, err := os.ReadFile("testdata/positions_page1.json")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("user"); got != "0x9cb98fc57fe8a3bdf75c3b48bd419e5d262f7b80" {
			t.Errorf("user param = %q", got)
		}
		if r.URL.Query().Get("cursor") != "" {
			w.Write([]byte(`{"data":[],"pagination":{"has_more":false}}`))
			return
		}
		w.Write(body)
	}))
	defer srv.Close()

	ps, err := testClient(srv).Positions(context.Background(), "0x9cb98fc57fe8a3bdf75c3b48bd419e5d262f7b80")
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 3 {
		t.Fatalf("got %d positions, want 3", len(ps))
	}
	p := ps[0]
	if p.ConditionID == "" || p.TokenID == "" {
		t.Fatalf("condition/token id not decoded: %+v", p)
	}
	if p.Status != "OPEN" && p.Status != "REDEEMABLE" {
		t.Errorf("unexpected status %q", p.Status)
	}
}

func TestPositionsFollowsCursor(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			if r.URL.Query().Get("cursor") != "" {
				t.Error("first page should not carry a cursor")
			}
			w.Write([]byte(`{"data":[{"token_id":"1","condition_id":"0xa","current_size":2,"status":"OPEN"}],
				"pagination":{"limit":1,"offset":0,"has_more":true,"next_cursor":"next-page"}}`))
			return
		}
		if got := r.URL.Query().Get("cursor"); got != "next-page" {
			t.Errorf("cursor = %q, want next-page", got)
		}
		w.Write([]byte(`{"data":[{"token_id":"2","condition_id":"0xb","current_size":3,"status":"OPEN"}],
			"pagination":{"limit":1,"offset":1,"has_more":false}}`))
	}))
	defer srv.Close()

	ps, err := testClient(srv).Positions(context.Background(), "0x9cb98fc57fe8a3bdf75c3b48bd419e5d262f7b80")
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 2 || ps[1].TokenID != "2" {
		t.Fatalf("pages not merged: %+v", ps)
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Errorf("server hit %d times, want 2", calls)
	}
}

func TestPositionsRetiredV1(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusGone)
		w.Write([]byte(`{"error":"Data API v1 has been retired"}`))
	}))
	defer srv.Close()

	_, err := testClient(srv).Positions(context.Background(), "0x9cb98fc57fe8a3bdf75c3b48bd419e5d262f7b80")
	if !errors.Is(err, ErrRetired) {
		t.Fatalf("err = %v, want ErrRetired", err)
	}
}

func TestPositionsRetries429(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Write([]byte(`{"data":[],"pagination":{"has_more":false}}`))
	}))
	defer srv.Close()

	if _, err := testClient(srv).Positions(context.Background(), "0x9cb98fc57fe8a3bdf75c3b48bd419e5d262f7b80"); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Errorf("calls = %d, want 2", calls)
	}
}

func TestPositionsReportsServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"required query param 'user' or 'condition' not provided"}`))
	}))
	defer srv.Close()

	_, err := testClient(srv).Positions(context.Background(), "0x9cb98fc57fe8a3bdf75c3b48bd419e5d262f7b80")
	if err == nil || !strings.Contains(err.Error(), "required query param") {
		t.Fatalf("err = %v, want body snippet", err)
	}
}

func TestValidAddress(t *testing.T) {
	ok := "0x9cb98fc57fe8a3bdf75c3b48bd419e5d262f7b80"
	if !ValidAddress(ok) {
		t.Error("valid address rejected")
	}
	for _, bad := range []string{"", "0x", ok[:41], ok + "0", strings.Replace(ok, "0x", "1x", 1), "0x9cb98fc57fe8a3bdf75c3b48bd419e5d262f7b8g"} {
		if ValidAddress(bad) {
			t.Errorf("bad address %q accepted", bad)
		}
	}
}
