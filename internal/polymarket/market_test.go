package polymarket

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDirectMarketLookupRetainsFeeParameters(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/markets/123" {
			t.Errorf("path=%s", r.URL.Path)
		}
		fmt.Fprint(w, string(record(map[string]any{"feesEnabled": true, "feeSchedule": map[string]any{"rate": .01, "exponent": 1}})))
	}))
	defer server.Close()
	c := &Client{baseURL: server.URL, httpClient: server.Client()}
	m, err := c.FetchMarket(context.Background(), "123")
	if err != nil || m.FeesEnabled == nil || !*m.FeesEnabled || string(m.FeeRate) != "0.01" || m.FeeExponent == nil || *m.FeeExponent != 1 {
		t.Fatalf("market=%+v err=%v", m, err)
	}
	for _, id := range []string{"", "../123", "123?other=1"} {
		if _, err := c.FetchMarket(context.Background(), id); err == nil {
			t.Fatal("invalid ID accepted")
		}
	}
}
