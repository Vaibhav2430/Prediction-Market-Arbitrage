package kalshi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Vaibhav2430/Prediction-Market-Arbitrage/internal/models"
)

const validMarket = `{"ticker":"TEST-26-A","event_ticker":"TEST-26","title":"Test contract?","yes_sub_title":"Above threshold","no_sub_title":"Below threshold","status":"active","market_type":"binary","close_time":"2027-01-01T00:00:00Z","notional_value_dollars":"1.0000","rules_primary":"Primary rules","rules_secondary":"Secondary rules"}`
const validBook = `{"orderbook_fp":{"yes_dollars":[["0.4001","12.50"],["0.2000","1.00"]],"no_dollars":[["0.3500","2.05"]]}}`

func TestParseFixed(t *testing.T) {
	for _, tt := range []struct {
		raw      string
		decimals int
		want     int64
	}{
		{`"0.4001"`, 4, 4001}, {`0.4001`, 4, 4001}, {`"1"`, 4, 10000}, {`"0"`, 4, 0}, {`"12.50"`, 2, 1250}, {`"0.01"`, 2, 1},
	} {
		got, err := parseFixed(json.RawMessage(tt.raw), tt.decimals)
		if err != nil || got != tt.want {
			t.Fatalf("parse %s: %d %v", tt.raw, got, err)
		}
	}
	for _, raw := range []string{`null`, `true`, `{}`, `[]`, `""`, `"NaN"`, `"Inf"`, `"-0.1"`, `"+0.1"`, `"0.00001"`, `"1e-2"`, `"1."`, `".5"`, `" 0.5"`, `"999999999999999999999"`, `"1.2.3"`} {
		if _, err := parseFixed(json.RawMessage(raw), 4); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}

func TestParseLevels(t *testing.T) {
	levels, err := parseLevels(json.RawMessage(`[["0.2","1.25"],["0.4001","12.50"]]`))
	if err != nil || len(levels) != 2 || levels[0].Price != 4001 || levels[0].Quantity != 1250 {
		t.Fatalf("levels=%+v err=%v", levels, err)
	}
	for _, raw := range []string{"null", "[]", ""} {
		levels, err := parseLevels(json.RawMessage(raw))
		if err != nil || len(levels) != 0 {
			t.Fatalf("empty %q: %v", raw, err)
		}
	}
	for _, raw := range []string{`{}`, `[null]`, `[["0.2"]]`, `[["0.2","1","extra"]]`, `[["1.01","1"]]`, `[["-0.1","1"]]`, `[["0.3","0"]]`, `[["0.3","-2"]]`, `[["0.3","1.001"]]`, `[["0.3",null]]`, `[["0.3","1"],["0.3000","2"]]`} {
		if _, err := parseLevels(json.RawMessage(raw)); err == nil {
			t.Fatalf("accepted levels %s", raw)
		}
	}
}

func TestParseMarket(t *testing.T) {
	market, ok, err := parseMarket(json.RawMessage(validMarket))
	if err != nil || !ok || market.EventTicker != "TEST-26" || market.RulesPrimary != "Primary rules" || market.YesSubtitle != "Above threshold" {
		t.Fatalf("market=%+v include=%v err=%v", market, ok, err)
	}
	for _, tt := range []struct {
		from, to         string
		wantErr, include bool
	}{
		{`"active"`, `"open"`, false, true},
		{`"active"`, `"closed"`, false, false},
		{`"binary"`, `"scalar"`, false, false},
		{`"active"`, `""`, true, false},
		{`"Test contract?"`, `""`, true, false},
		{`"2027-01-01T00:00:00Z"`, `"bad"`, true, false},
		{`"2027-01-01T00:00:00Z"`, `null`, true, false},
		{`"1.0000"`, `"100.0000"`, true, false},
		{`"1.0000"`, `null`, true, false},
		{`"TEST-26-A"`, `"../bad"`, true, false},
	} {
		_, include, err := parseMarket(json.RawMessage(strings.Replace(validMarket, tt.from, tt.to, 1)))
		if (err != nil) != tt.wantErr || include != tt.include {
			t.Fatalf("replace %s with %s: include=%v err=%v", tt.from, tt.to, include, err)
		}
	}
}

func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return &Client{baseURL: server.URL, httpClient: server.Client()}
}

func TestFetchOpenMarketsPagination(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/markets" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
		}
		q := r.URL.Query()
		if len(q) != 5 || q.Get("status") != "open" || q.Get("limit") != "3" || q.Get("cursor") != "abc+/=" || q.Get("series_ticker") != "TEST" || q.Get("mve_filter") != "exclude" {
			t.Errorf("query=%v", q)
		}
		for key := range r.Header {
			if strings.HasPrefix(strings.ToUpper(key), "KALSHI-ACCESS") || strings.EqualFold(key, "Authorization") {
				t.Error("unexpected auth header")
			}
		}
		fmt.Fprintf(w, `{"markets":[%s,{"ticker":"BROKEN"},%s],"cursor":"next+/="}`, validMarket, strings.Replace(validMarket, `"active"`, `"closed"`, 1))
	})
	page, err := c.FetchOpenMarkets(context.Background(), 3, "abc+/=", "TEST")
	if err != nil || len(page.Markets) != 1 || len(page.Warnings) != 1 || page.Cursor != "next+/=" {
		t.Fatalf("page=%+v err=%v", page, err)
	}
}

func TestFetchMarket(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/markets/TEST-26-A" && r.URL.Path != "/markets/DIFFERENT" {
			t.Errorf("path=%s", r.URL.Path)
		}
		fmt.Fprintf(w, `{"market":%s}`, validMarket)
	})
	if _, err := c.FetchMarket(context.Background(), "TEST-26-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.FetchMarket(context.Background(), "DIFFERENT"); err == nil {
		t.Fatal("accepted mismatched ticker")
	}
}

func TestFetchOrderBook(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/markets/TEST-26-A/orderbook" || r.URL.Query().Get("depth") != "5" {
			t.Errorf("request=%s %s", r.Method, r.URL)
		}
		fmt.Fprint(w, validBook)
	})
	book, err := c.FetchOrderBook(context.Background(), "TEST-26-A", 5)
	if err != nil {
		t.Fatal(err)
	}
	if book.Ticker != "TEST-26-A" || book.ReceivedAt.IsZero() || len(book.YesBids) != 2 {
		t.Fatalf("book=%+v", book)
	}
	top := book.Top()
	if top.YesAsk.Price != 6500 || top.YesAsk.Quantity != 205 || top.NoAsk.Price != 5999 || top.NoAsk.Quantity != 1250 {
		t.Fatal("complement mismatch")
	}
}

func TestBookResponseValidation(t *testing.T) {
	for _, tt := range []struct {
		body    string
		wantErr bool
	}{
		{`{"orderbook_fp":{"yes_dollars":[],"no_dollars":null}}`, false},
		{`{"orderbook_fp":{"yes_dollars":[["0","1.00"]]}}`, false},
		{`{"orderbook_fp":{"no_dollars":[["1.0000","0.01"]]}}`, false},
		{`{"orderbook_fp":{"yes_dollars":[["0.7","1"]],"no_dollars":[["0.4","1"]]}}`, true},
		{`{"orderbook_fp":{"yes_dollars":[["0.7","1"]],"no_dollars":[["0.3","1"]]}}`, false},
		{`{}`, true}, {`{"orderbook_fp":null}`, true}, {`{"orderbook":{"yes":[[30,1]]}}`, true},
		{`{"orderbook_fp":{"yes_dollars":{}}}`, true},
	} {
		t.Run(tt.body, func(t *testing.T) {
			c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, tt.body) })
			book, err := c.FetchOrderBook(context.Background(), "TEST-26-A", 0)
			if (err != nil) != tt.wantErr {
				t.Fatalf("book=%+v err=%v", book, err)
			}
		})
	}
}

func TestMarketEnvelopeValidation(t *testing.T) {
	for _, body := range []string{`{}`, `{"markets":null,"cursor":""}`, `{"markets":[],"cursor":null}`, `{"markets":{},"cursor":""}`, `{"markets":[],"cursor":4}`} {
		c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) })
		if _, err := c.FetchOpenMarkets(context.Background(), 1, "", ""); err == nil {
			t.Fatalf("accepted %s", body)
		}
	}
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"markets":[],"cursor":""}`) })
	page, err := c.FetchOpenMarkets(context.Background(), 1, "", "")
	if err != nil || len(page.Markets) != 0 || page.Cursor != "" {
		t.Fatalf("page=%+v err=%v", page, err)
	}
}

func TestHTTPFailures(t *testing.T) {
	for _, tt := range []struct {
		name, body string
		status     int
	}{
		{"rate limit", "", 429}, {"not found", "", 404}, {"server error", "", 500}, {"invalid JSON", "{", 200},
		{"null", "null", 200}, {"array", "[]", 200}, {"trailing", "{} {}", 200}, {"oversize", strings.Repeat(" ", maxResponseBytes+1), 200},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tt.status); fmt.Fprint(w, tt.body) })
			if _, err := c.FetchOrderBook(context.Background(), "TEST-26-A", 1); err == nil {
				t.Fatal("expected HTTP/response error")
			}
		})
	}
}

func TestValidationBeforeNetwork(t *testing.T) {
	c := NewClient()
	for _, limit := range []int{-1, 0, 1001} {
		if _, err := c.FetchOpenMarkets(context.Background(), limit, "", ""); err == nil {
			t.Fatal("bad limit accepted")
		}
	}
	for _, ticker := range []string{"", ".", "..", "bad/ticker", "bad?query", "bad\nheader"} {
		if _, err := c.FetchOrderBook(context.Background(), ticker, 1); err == nil {
			t.Fatal("bad ticker accepted")
		}
		if _, err := c.FetchMarket(context.Background(), ticker); err == nil {
			t.Fatal("bad ticker accepted")
		}
	}
	for _, depth := range []int{-1, 101} {
		if _, err := c.FetchOrderBook(context.Background(), "TEST", depth); err == nil {
			t.Fatal("bad depth accepted")
		}
	}
}

func TestTimeoutAndCancellation(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.FetchOpenMarkets(ctx, 1, "", ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel err=%v", err)
	}
	ctx, cancel = context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := c.FetchOrderBook(ctx, "TEST", 1); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline err=%v", err)
	}
	c.httpClient.Timeout = 30 * time.Millisecond
	if _, err := c.FetchOrderBook(context.Background(), "TEST", 1); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("HTTP timeout err=%v", err)
	}
}

// Ensure quantity values stay exact even beyond float64's integer precision.
func TestLargeExactQuantity(t *testing.T) {
	levels, err := parseLevels(json.RawMessage(`[["0.0001","90071992547409.91"]]`))
	if err != nil || levels[0].Quantity != models.Quantity(9007199254740991) {
		t.Fatalf("levels=%+v err=%v", levels, err)
	}
}
