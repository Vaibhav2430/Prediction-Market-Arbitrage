package kalshi

import (
	"context"
	"fmt"
	"net/http"
	"testing"
)

func TestRuleSourcesCaptureEventAndBindingSeriesTerms(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/events/E" {
			fmt.Fprint(w, `{"event":{"event_ticker":"E","series_ticker":"S","settlement_sources":[{"name":"Official party","url":"https://example.com"}]}}`)
		} else if r.URL.Path == "/series/S" {
			fmt.Fprint(w, `{"series":{"ticker":"S","contract_terms_url":"https://assets.kalshi.com/contract_terms/T.pdf","settlement_sources":[]}}`)
		} else {
			t.Errorf("unexpected request %s", r.URL)
		}
	})
	sources, err := c.FetchRuleSources(context.Background(), "E")
	if err != nil || sources.SeriesTicker != "S" || len(sources.EventSources) != 1 || sources.TermsURL == "" {
		t.Fatalf("sources=%+v err=%v", sources, err)
	}
}
