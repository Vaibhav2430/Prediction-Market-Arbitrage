package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/Vaibhav2430/Prediction-Market-Arbitrage/internal/kalshi"
	"github.com/Vaibhav2430/Prediction-Market-Arbitrage/internal/models"
	"github.com/Vaibhav2430/Prediction-Market-Arbitrage/internal/polymarket"
)

type fakeTopicGamma struct {
	pages []int
	err   error
	many  bool
}

func (f *fakeTopicGamma) SearchActiveMarkets(_ context.Context, topic string, limit, page int) (polymarket.SearchPage, error) {
	f.pages = append(f.pages, page)
	m := models.Market{ID: "p1", Question: "Bitcoin price above 100000", EndDate: "2026-12-01T05:00:00Z", Description: "Full source-specific rules", Outcomes: []models.Outcome{{Name: "Yes"}, {Name: "No"}}}
	markets := []models.Market{m}
	if f.many {
		for i := 0; i < 1005; i++ {
			copy := m
			copy.ID = string(rune('a' + i))
			markets = append(markets, copy)
		}
	}
	return polymarket.SearchPage{Result: polymarket.Result{Markets: markets}, HasMore: page < 3, Events: 1}, f.err
}

type fakeTopicKalshi struct {
	seriesCalls int
	calls       []string
	noSeries    bool
	err         error
}

func (f *fakeTopicKalshi) FindSeries(context.Context, string, string) (kalshi.SeriesResult, error) {
	f.seriesCalls++
	if f.noSeries {
		return kalshi.SeriesResult{}, nil
	}
	return kalshi.SeriesResult{Series: []kalshi.Series{{Ticker: "KXBTC", Title: "Bitcoin price"}, {Ticker: "KXBTCD", Title: "Bitcoin daily"}}}, f.err
}
func (f *fakeTopicKalshi) FetchOpenMarkets(_ context.Context, limit int, cursor, series string) (kalshi.MarketPage, error) {
	f.calls = append(f.calls, series+":"+cursor)
	next := "next"
	if cursor != "" {
		next = ""
	}
	return kalshi.MarketPage{Markets: []kalshi.Market{{Ticker: series + "-A", Title: "Bitcoin price above 100000", YesSubtitle: "Bitcoin above 100000", CloseTime: time.Date(2026, 12, 1, 5, 0, 0, 0, time.UTC), RulesPrimary: "Different settlement source"}}, Cursor: next}, f.err
}
func topicOptions() options {
	return options{pages: 3, size: 10, top: 5, threshold: .45, format: "json", topic: "bitcoin", category: "Crypto", maxSeries: 2, topicEvents: 5, searchPage: 1}
}
func TestTopicDiscoveryTargetsBothVenuesAndIndependentCursors(t *testing.T) {
	g, k := &fakeTopicGamma{}, &fakeTopicKalshi{}
	var out bytes.Buffer
	if err := executeTopic(context.Background(), g, k, topicOptions(), &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	var r report
	if err := json.Unmarshal(out.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	if r.Topic != "bitcoin" || r.PolymarketMarkets != 1 || r.KalshiMarkets != 2 || r.CandidateCount != 2 || r.PolymarketPages != 3 || r.KalshiPages != 4 {
		t.Fatalf("report=%+v", r)
	}
	if strings.Join(k.calls, ",") != "KXBTC:,KXBTC:next,KXBTCD:,KXBTCD:next" {
		t.Fatal(k.calls)
	}
	if r.Candidates[0].Status != "needs_review" {
		t.Fatal("topic does not imply equivalence")
	}
}
func TestTopicSeriesCapAndExplicitOverride(t *testing.T) {
	o := topicOptions()
	o.maxSeries = 1
	g, k := &fakeTopicGamma{}, &fakeTopicKalshi{}
	var out bytes.Buffer
	if err := executeTopic(context.Background(), g, k, o, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	var r report
	json.Unmarshal(out.Bytes(), &r)
	if len(r.KalshiSeries) != 1 || r.KalshiSeriesMatched != 2 || len(r.Warnings) == 0 {
		t.Fatal("missing series cap disclosure")
	}
	o.series = "KXEXACT"
	o.cursor = "saved"
	out.Reset()
	k = &fakeTopicKalshi{}
	if err := executeTopic(context.Background(), g, k, o, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if k.seriesCalls != 0 || k.calls[0] != "KXEXACT:saved" {
		t.Fatal(k.calls)
	}
}
func TestNoSeriesDoesNotFetchUnrelatedMarkets(t *testing.T) {
	g, k := &fakeTopicGamma{}, &fakeTopicKalshi{noSeries: true}
	var out bytes.Buffer
	if err := executeTopic(context.Background(), g, k, topicOptions(), &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	var r report
	json.Unmarshal(out.Bytes(), &r)
	if len(k.calls) != 0 || r.KalshiMarkets != 0 || !strings.Contains(strings.Join(r.Warnings, " "), "No related Kalshi series") {
		t.Fatal("unrelated fallback")
	}
}
func TestWindowBoundariesAndMissingDates(t *testing.T) {
	from, until := "2026-11-24T00:00:00Z", "2026-12-01T05:00:00Z"
	for _, tt := range []struct {
		date string
		want bool
	}{{from, true}, {until, true}, {"2026-12-02T00:00:00Z", false}, {"2026-11-23T00:00:00Z", false}, {"", false}, {"2026-11-24", false}} {
		if withinWindow(tt.date, from, until) != tt.want {
			t.Fatal(tt)
		}
	}
	if !withinWindow("", "", "") {
		t.Fatal("no window must retain missing metadata for review")
	}
	o := topicOptions()
	o.after = "2027-01-01T00:00:00Z"
	var out bytes.Buffer
	if err := executeTopic(context.Background(), &fakeTopicGamma{}, &fakeTopicKalshi{}, o, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	var r report
	json.Unmarshal(out.Bytes(), &r)
	if r.PolymarketMarkets != 0 || r.KalshiMarkets != 0 || r.WindowOmittedPolymarket != 1 || r.WindowOmittedKalshi != 2 {
		t.Fatalf("report=%+v", r)
	}
}
func TestTopicMarketCapAndErrors(t *testing.T) {
	var out bytes.Buffer
	if err := executeTopic(context.Background(), &fakeTopicGamma{many: true}, &fakeTopicKalshi{}, topicOptions(), &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	var r report
	json.Unmarshal(out.Bytes(), &r)
	if !r.PolymarketCapped || r.PolymarketMarkets != maxTopicMarkets {
		t.Fatalf("report=%+v", r)
	}
	out.Reset()
	if err := executeTopic(context.Background(), &fakeTopicGamma{err: errors.New("search failed")}, &fakeTopicKalshi{}, topicOptions(), &out, io.Discard); err == nil || out.Len() != 0 {
		t.Fatal("failed discovery produced report")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := executeTopic(ctx, &fakeTopicGamma{}, &fakeTopicKalshi{}, topicOptions(), io.Discard, io.Discard); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func TestTopicFlagsRejectedBeforeNetwork(t *testing.T) {
	for _, args := range [][]string{
		{"-topic", "custom topic"}, {"-topic", "bitcoin", "-polymarket-offset", "1"}, {"-topic", "bitcoin", "-kalshi-cursor", "wrong"},
		{"-topic", "bitcoin", "-max-series", "11"}, {"-topic-events", "21"}, {"-search-page", "0"},
		{"-deadline-after", "2026-11-23T00:00:00Z"}, {"-topic", "bitcoin", "-deadline-after", "2026-11-23"},
		{"-topic", "bitcoin", "-deadline-after", "2027-01-01T00:00:00Z", "-deadline-before", "2026-01-01T00:00:00Z"},
	} {
		if err := run(context.Background(), args, io.Discard, io.Discard); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}
