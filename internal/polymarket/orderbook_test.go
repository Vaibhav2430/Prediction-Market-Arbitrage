package polymarket

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
)

const validTokenBook = `{"asset_id":"123456","market":"condition-1","timestamp":"1795478687525","hash":"abc123","tick_size":"0.001","min_order_size":"5","neg_risk":false,"bids":[{"price":"0.2","size":"1.000001"},{"price":"0.4","size":"12.50"}],"asks":[{"price":"0.9","size":"20"},{"price":"0.6","size":"3.123456"}]}`

func changedBook(t *testing.T, fields map[string]any) []byte {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(validTokenBook), &m); err != nil {
		t.Fatal(err)
	}
	for k, v := range fields {
		m[k] = v
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestBookDecimal(t *testing.T) {
	for _, tt := range []struct {
		raw   string
		scale int
		want  int64
	}{
		{`"0.4001"`, 4, 4001}, {`0.4001`, 4, 4001}, {`"0.4001000"`, 4, 4001},
		{`"3.123456"`, 6, 3123456}, {`"1.000001"`, 6, 1000001}, {`"0"`, 4, 0},
		{`"1795478687525"`, 0, 1795478687525}, {`1795478687525`, 0, 1795478687525},
		{`"9007199254.740991"`, 6, 9007199254740991},
	} {
		got, err := bookDecimal(json.RawMessage(tt.raw), tt.scale)
		if err != nil || got != tt.want {
			t.Fatalf("%s: got %d err %v", tt.raw, got, err)
		}
	}
	for _, raw := range []string{`null`, `true`, `{}`, `[]`, `""`, `"NaN"`, `"Inf"`, `"-0.1"`, `"+0.1"`, `"0.00001"`, `"1e-2"`, `"1."`, `".5"`, `" 0.5"`, `"999999999999999999999"`, `"1.2.3"`} {
		if _, err := bookDecimal(json.RawMessage(raw), 4); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}

func TestParseOrderBook(t *testing.T) {
	book, err := parseOrderBook([]byte(validTokenBook), "123456")
	if err != nil {
		t.Fatal(err)
	}
	if book.BestBid().Price != 4000 || book.BestBid().Size != 12500000 || book.BestAsk().Price != 6000 || book.BestAsk().Size != 3123456 {
		t.Fatalf("wrong best levels: %+v", book)
	}
	if book.Bids[1].Size != 1000001 {
		t.Fatal("size rounded")
	}
	if book.Timestamp.UnixMilli() != 1795478687525 || book.TickSize != 10 || book.MinOrderSize != 5000000 {
		t.Fatalf("metadata=%+v", book)
	}
}

func TestOrderBookInvalidResponses(t *testing.T) {
	for _, tt := range []struct {
		name   string
		fields map[string]any
	}{
		{"wrong asset", map[string]any{"asset_id": "other"}},
		{"missing market", map[string]any{"market": ""}},
		{"missing hash", map[string]any{"hash": ""}},
		{"missing risk flag", map[string]any{"neg_risk": nil}},
		{"missing timestamp", map[string]any{"timestamp": nil}},
		{"zero timestamp", map[string]any{"timestamp": "0"}},
		{"overflow timestamp", map[string]any{"timestamp": "9999999999999999999999"}},
		{"bad tick", map[string]any{"tick_size": "0"}},
		{"large tick", map[string]any{"tick_size": "2"}},
		{"bad minimum", map[string]any{"min_order_size": "-1"}},
		{"missing bids", map[string]any{"bids": nil}},
		{"invalid ask", map[string]any{"asks": map[string]any{}}},
		{"price above one", map[string]any{"bids": []any{map[string]any{"price": "1.1", "size": "2"}}}},
		{"negative price", map[string]any{"bids": []any{map[string]any{"price": "-0.1", "size": "2"}}}},
		{"zero size", map[string]any{"bids": []any{map[string]any{"price": "0.2", "size": "0"}}}},
		{"size precision", map[string]any{"bids": []any{map[string]any{"price": "0.2", "size": "1.0000001"}}}},
		{"price precision", map[string]any{"bids": []any{map[string]any{"price": "0.20001", "size": "2"}}}},
		{"off tick", map[string]any{"bids": []any{map[string]any{"price": "0.2001", "size": "2"}}}},
		{"duplicate", map[string]any{"bids": []any{map[string]any{"price": "0.2", "size": "2"}, map[string]any{"price": "0.200", "size": "3"}}}},
		{"crossed", map[string]any{"asks": []any{map[string]any{"price": "0.3", "size": "2"}}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := parseOrderBook(changedBook(t, tt.fields), "123456"); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
	for _, body := range []string{"null", "[]", "{", "{} {}", `{"error":"No orderbook exists"}`} {
		if _, err := parseOrderBook([]byte(body), "123456"); err == nil {
			t.Fatalf("accepted %s", body)
		}
	}
}

func TestEmptyAndOneSidedTokenBooks(t *testing.T) {
	empty := []any{}
	book, err := parseOrderBook(changedBook(t, map[string]any{"bids": empty, "asks": empty}), "123456")
	if err != nil || book.BestBid() != nil || book.BestAsk() != nil {
		t.Fatalf("book=%+v err=%v", book, err)
	}
	book, err = parseOrderBook(changedBook(t, map[string]any{"bids": empty}), "123456")
	if err != nil || book.BestBid() != nil || book.BestAsk() == nil {
		t.Fatalf("book=%+v err=%v", book, err)
	}
	book, err = parseOrderBook(changedBook(t, map[string]any{"bids": []any{map[string]any{"price": 0, "size": 1}}, "asks": []any{map[string]any{"price": 1, "size": 2}}}), "123456")
	if err != nil || book.BestBid().Price != 0 || book.BestAsk().Price != 10000 {
		t.Fatalf("boundary quotes: %+v %v", book, err)
	}
}

func TestFetchTokenBook(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/book" || r.URL.Query().Get("token_id") != "123456" || len(r.URL.Query()) != 1 {
			t.Errorf("request=%s %s", r.Method, r.URL)
		}
		if r.Header.Get("Authorization") != "" || r.Header.Get("POLY_API_KEY") != "" {
			t.Error("unexpected auth")
		}
		fmt.Fprint(w, validTokenBook)
	}))
	defer server.Close()
	client := &BookClient{baseURL: server.URL, httpClient: server.Client()}
	book, err := client.FetchOrderBook(context.Background(), "123456")
	if err != nil || book.ReceivedAt.IsZero() || book.BestBid().Price != 4000 {
		t.Fatalf("book=%+v err=%v", book, err)
	}
}

func TestTokenBookHTTPFailures(t *testing.T) {
	for _, tt := range []struct {
		name, body string
		status     int
	}{
		{"not found", "", 404}, {"rate limit", "", 429}, {"server failure", "", 500},
		{"bad JSON", "{", 200}, {"bad envelope", "[]", 200}, {"oversize", strings.Repeat(" ", maxResponseBytes+1), 200},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tt.status); fmt.Fprint(w, tt.body) }))
			defer server.Close()
			client := &BookClient{baseURL: server.URL, httpClient: server.Client()}
			if _, err := client.FetchOrderBook(context.Background(), "123456"); err == nil {
				t.Fatal("expected failure")
			}
		})
	}
}

func TestTokenBookTimeouts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer server.Close()
	client := &BookClient{baseURL: server.URL, httpClient: server.Client()}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.FetchOrderBook(ctx, "123456"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	ctx, cancel = context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := client.FetchOrderBook(ctx, "123456"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	client.httpClient.Timeout = 30 * time.Millisecond
	if _, err := client.FetchOrderBook(context.Background(), "123456"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}

func TestTokenIDValidation(t *testing.T) {
	for _, id := range []string{"", "1&other=2", "-1", "1.2", "abc", strings.Repeat("1", 79)} {
		if _, err := NewBookClient().FetchOrderBook(context.Background(), id); err == nil {
			t.Fatalf("accepted %q", id)
		}
	}
}

func TestGammaV2AssetSelection(t *testing.T) {
	market, ok, err := parseMarket(record(map[string]any{"version": "v2", "positionIds": []string{"111", "222"}}))
	if err != nil || !ok {
		t.Fatal(err)
	}
	yes, no, binary := market.YesNoOutcomes()
	if !binary || yes.TokenID != "111" || no.TokenID != "222" {
		t.Fatalf("wrong V2 IDs: %+v", market)
	}
}
