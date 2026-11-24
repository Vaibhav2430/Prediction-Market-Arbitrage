package kalshi

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestFindSeriesByCategoryAndTopic(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/series" || r.URL.Query().Get("category") != "Crypto" {
			t.Errorf("bad request %s", r.URL)
		}
		fmt.Fprint(w, `{"series":[{"ticker":"BTC","title":"Bitcoin range"},{"ticker":"KXLONG","title":"Bitcoin at some distant future date"},{"ticker":"KXBTC","title":"Bitcoin range","category":"Financials"},{"ticker":"KXBTC","title":"Bitcoin range"},{"ticker":"KXETH","title":"Ethereum range"},{"ticker":"BAD/ID","title":"Bitcoin"}]}`)
	})
	result, err := c.FindSeries(context.Background(), "BITCOIN", "Crypto")
	if err != nil || len(result.Series) != 3 || result.Series[0].Ticker != "KXBTC" || len(result.Warnings) != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}
func TestSeriesEnvelopesAndValidation(t *testing.T) {
	for _, body := range []string{`{}`, `{"series":null}`, `{"series":{}}`} {
		c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) })
		if _, err := c.FindSeries(context.Background(), "bitcoin", "Crypto"); err == nil {
			t.Fatal("invalid envelope accepted")
		}
	}
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"series":[]}`) })
	result, err := c.FindSeries(context.Background(), "bitcoin", "Crypto")
	if err != nil || len(result.Series) != 0 {
		t.Fatal(err)
	}
	for _, args := range [][2]string{{"", "Crypto"}, {"bitcoin", ""}, {strings.Repeat("a", 257), "Crypto"}} {
		if _, err := NewClient().FindSeries(context.Background(), args[0], args[1]); err == nil {
			t.Fatal("invalid args accepted")
		}
	}
}

func TestSeriesRankingPrefersTopicTitleAndExactTags(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"series":[{"ticker":"KXBCHMINY","title":"BCH min","tags":["Bitcoin Cash"]},{"ticker":"KXBTCATH","title":"BTC ATH","tags":["Bitcoin"]},{"ticker":"KXBTC","title":"Bitcoin range","tags":["Bitcoin"]},{"ticker":"KXBTCD","title":"Bitcoin price Above/below","tags":["Bitcoin"]}]}`)
	})
	result, err := c.FindSeries(context.Background(), "bitcoin", "Crypto")
	if err != nil || len(result.Series) != 4 || result.Series[0].Ticker != "KXBTC" || result.Series[1].Ticker != "KXBTCD" {
		t.Fatalf("series=%+v err=%v", result.Series, err)
	}
}
