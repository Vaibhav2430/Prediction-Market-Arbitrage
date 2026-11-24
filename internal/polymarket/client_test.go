package polymarket

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func record(fields map[string]any) []byte {
	m := map[string]any{"id": "123", "question": "Test?", "active": true, "closed": false,
		"outcomes": `["Yes","No"]`, "outcomePrices": `["0.25","0.75"]`, "clobTokenIds": `["yes-token","no-token"]`}
	for k, v := range fields {
		m[k] = v
	}
	b, err := json.Marshal(m)
	if err != nil {
		panic(err)
	}
	return b
}

func TestParseMarket(t *testing.T) {
	tests := []struct {
		name    string
		fields  map[string]any
		wantErr bool
		include bool
		missing bool
	}{
		{name: "encoded arrays", include: true},
		{name: "native arrays and numeric prices", fields: map[string]any{"outcomes": []string{"Yes", "No"}, "outcomePrices": []any{0, 1}, "clobTokenIds": []string{"a", "b"}}, include: true},
		{name: "missing prices", fields: map[string]any{"outcomePrices": nil}, include: true, missing: true},
		{name: "null price", fields: map[string]any{"outcomePrices": `[null,"0.7"]`}, include: true, missing: true},
		{name: "empty prices", fields: map[string]any{"outcomePrices": "[]"}, include: true, missing: true},
		{name: "no token IDs", fields: map[string]any{"clobTokenIds": nil}, include: true},
		{name: "closed", fields: map[string]any{"closed": true}},
		{name: "inactive", fields: map[string]any{"active": false}},
		{name: "missing status", fields: map[string]any{"active": nil}, wantErr: true},
		{name: "missing ID", fields: map[string]any{"id": ""}, wantErr: true},
		{name: "wrong ID type", fields: map[string]any{"id": 17}, wantErr: true},
		{name: "missing question", fields: map[string]any{"question": " "}, wantErr: true},
		{name: "missing labels", fields: map[string]any{"outcomes": nil}, wantErr: true},
		{name: "bad encoded JSON", fields: map[string]any{"outcomes": "["}, wantErr: true},
		{name: "encoded null labels", fields: map[string]any{"outcomes": "null"}, wantErr: true},
		{name: "duplicate label", fields: map[string]any{"outcomes": `["Yes"," yes "]`}, wantErr: true},
		{name: "null label", fields: map[string]any{"outcomes": `[null,"No"]`}, wantErr: true},
		{name: "empty label", fields: map[string]any{"outcomes": `["","No"]`}, wantErr: true},
		{name: "price length mismatch", fields: map[string]any{"outcomePrices": `["0.2"]`}, wantErr: true},
		{name: "token length mismatch", fields: map[string]any{"clobTokenIds": `["a"]`}, wantErr: true},
		{name: "bad token type", fields: map[string]any{"clobTokenIds": true}, wantErr: true},
		{name: "price object", fields: map[string]any{"outcomePrices": map[string]float64{"Yes": 0.2}}, wantErr: true},
		{name: "invalid price", fields: map[string]any{"outcomePrices": `["abc","0.2"]`}, wantErr: true},
		{name: "negative price", fields: map[string]any{"outcomePrices": `[-0.1,0.2]`}, wantErr: true},
		{name: "price above one", fields: map[string]any{"outcomePrices": `[1.1,0.2]`}, wantErr: true},
		{name: "NaN", fields: map[string]any{"outcomePrices": `["NaN","0.2"]`}, wantErr: true},
		{name: "infinity", fields: map[string]any{"outcomePrices": `["+Inf","0.2"]`}, wantErr: true},
		{name: "boolean price", fields: map[string]any{"outcomePrices": `[true,0.2]`}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			market, include, err := parseMarket(record(tt.fields))
			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v, wantErr=%v", err, tt.wantErr)
			}
			if include != tt.include {
				t.Fatalf("include=%v, want %v", include, tt.include)
			}
			if include {
				if market.ID != "123" || len(market.Outcomes) != 2 {
					t.Fatalf("unexpected market: %+v", market)
				}
				if (market.Outcomes[0].IndicativePrice == nil) != tt.missing {
					t.Fatalf("unexpected missing price: %+v", market.Outcomes[0])
				}
			}
		})
	}
}

func TestReversedOutcomesKeepPricesAndTokensAligned(t *testing.T) {
	m, ok, err := parseMarket(record(map[string]any{"outcomes": `["No","Yes"]`, "outcomePrices": `["0.8","0.2"]`, "clobTokenIds": `["no-token","yes-token"]`}))
	if err != nil || !ok {
		t.Fatalf("parse: %v", err)
	}
	yes, no, binary := m.YesNo()
	if !binary || *yes != 0.2 || *no != 0.8 || m.Outcomes[1].TokenID != "yes-token" {
		t.Fatalf("misaligned market: %+v", m)
	}
}

func TestFetchActiveMarkets(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		want := map[string][]string{"active": {"true"}, "closed": {"false"}, "limit": {"3"}, "offset": {"7"}}
		if r.Method != "GET" || r.URL.Path != "/markets" || !reflect.DeepEqual(map[string][]string(r.URL.Query()), want) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
		}
		if r.Header.Get("Authorization") != "" {
			t.Error("unexpected credentials")
		}
		fmt.Fprintf(w, "[%s,%s,%s]", record(nil), record(map[string]any{"outcomePrices": `["broken",0.2]`}), record(map[string]any{"closed": true}))
	}))
	defer server.Close()
	c := &Client{baseURL: server.URL, httpClient: server.Client()}
	result, err := c.FetchActiveMarkets(context.Background(), 3, 7)
	if err != nil || len(result.Markets) != 1 || len(result.Warnings) != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestFetchFailures(t *testing.T) {
	for _, tt := range []struct {
		name, body string
		status     int
	}{
		{"rate limited", "", 429}, {"server error", "", 500},
		{"object", `{"error":"bad"}`, 200}, {"null", "null", 200},
		{"invalid JSON", "[", 200}, {"trailing JSON", "[] {}", 200},
		{"too large", strings.Repeat(" ", maxResponseBytes+1), 200},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tt.status); fmt.Fprint(w, tt.body) }))
			defer server.Close()
			c := &Client{baseURL: server.URL, httpClient: server.Client()}
			if _, err := c.FetchActiveMarkets(context.Background(), 10, 0); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestEmptyPageAndInvalidRecords(t *testing.T) {
	for _, tt := range []struct {
		body     string
		warnings int
	}{{"[]", 0}, {`[null,42,{"active":true,"closed":false}]`, 3}} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, tt.body) }))
		c := &Client{baseURL: server.URL, httpClient: server.Client()}
		result, err := c.FetchActiveMarkets(context.Background(), 10, 0)
		server.Close()
		if err != nil || len(result.Markets) != 0 || len(result.Warnings) != tt.warnings {
			t.Fatalf("result=%+v err=%v", result, err)
		}
	}
}

func TestInvalidPagination(t *testing.T) {
	for _, p := range [][2]int{{0, 0}, {101, 0}, {1, -1}} {
		if _, err := NewClient().FetchActiveMarkets(context.Background(), p[0], p[1]); err == nil {
			t.Fatalf("accepted %v", p)
		}
	}
}

func TestCancellationAndTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer server.Close()
	t.Run("cancelled context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		c := &Client{baseURL: server.URL, httpClient: server.Client()}
		if _, err := c.FetchActiveMarkets(ctx, 1, 0); !errors.Is(err, context.Canceled) {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("context deadline", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
		defer cancel()
		c := &Client{baseURL: server.URL, httpClient: server.Client()}
		if _, err := c.FetchActiveMarkets(ctx, 1, 0); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("HTTP timeout", func(t *testing.T) {
		c := &Client{baseURL: server.URL, httpClient: &http.Client{Timeout: 30 * time.Millisecond}}
		if _, err := c.FetchActiveMarkets(context.Background(), 1, 0); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("err=%v", err)
		}
	})
}

func TestSettlementMetadataPreserved(t *testing.T) {
	market, include, err := parseMarket(record(map[string]any{
		"description": "Full resolution rules\nincluding exceptions", "endDate": "2027-01-01T00:00:00Z", "resolutionSource": "https://example.com/source",
		"events": []map[string]any{{"title": "Election 2026"}},
	}))
	if err != nil || !include || market.Description != "Full resolution rules\nincluding exceptions" || market.EndDate != "2027-01-01T00:00:00Z" || market.ResolutionSource != "https://example.com/source" || len(market.EventTitles) != 1 {
		t.Fatalf("market=%+v err=%v", market, err)
	}
}
