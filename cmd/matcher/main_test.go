package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/Vaibhav2430/Prediction-Market-Arbitrage/internal/kalshi"
	"github.com/Vaibhav2430/Prediction-Market-Arbitrage/internal/matching"
	"github.com/Vaibhav2430/Prediction-Market-Arbitrage/internal/models"
	"github.com/Vaibhav2430/Prediction-Market-Arbitrage/internal/polymarket"
)

type fakeGamma struct {
	calls []int
	err   error
}

func (f *fakeGamma) FetchActiveMarkets(_ context.Context, limit, offset int) (polymarket.Result, error) {
	f.calls = append(f.calls, offset)
	return polymarket.Result{Markets: []models.Market{{ID: "p1", Question: "Alice Smith wins election", Description: "Full rule with cancellation exception.", EndDate: "2027-01-01T00:00:00Z", Outcomes: []models.Outcome{{Name: "Yes"}, {Name: "No"}}}}, Warnings: []error{errors.New("bad record")}}, f.err
}

type fakeKalshi struct {
	calls  []string
	repeat bool
	err    error
}

func (f *fakeKalshi) FetchOpenMarkets(_ context.Context, limit int, cursor, series string) (kalshi.MarketPage, error) {
	f.calls = append(f.calls, cursor)
	next := "next+/="
	if cursor != "" && !f.repeat {
		next = ""
	}
	return kalshi.MarketPage{Markets: []kalshi.Market{{Ticker: "K1", Title: "Alice Smith wins election", YesSubtitle: "Alice Smith", RulesPrimary: "Official result.", RulesSecondary: "Full exception text."}}, Cursor: next}, f.err
}
func testOptions() options {
	return options{pages: 3, size: 10, offset: 7, top: 5, threshold: .45, format: "json"}
}

func TestDiscoveryPaginationDeduplicationAndJSON(t *testing.T) {
	g, k := &fakeGamma{}, &fakeKalshi{}
	var out, errOut bytes.Buffer
	if err := execute(context.Background(), g, k, testOptions(), &out, &errOut); err != nil {
		t.Fatal(err)
	}
	var result report
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.PolymarketMarkets != 1 || result.KalshiMarkets != 1 || result.PolymarketPages != 3 || result.KalshiPages != 2 || result.CandidateCount != 1 || result.NextPolymarketOffset != 37 || result.NextKalshiCursor != "" {
		t.Fatalf("report=%+v", result)
	}
	if len(g.calls) != 3 || g.calls[0] != 7 || g.calls[2] != 27 || len(k.calls) != 2 || k.calls[1] != "next+/=" {
		t.Fatalf("wrong pages: %v %v", g.calls, k.calls)
	}
	if result.Candidates[0].Status != "needs_review" || result.Candidates[0].Polymarket.RulesPrimary != "Full rule with cancellation exception." || errOut.Len() == 0 {
		t.Fatal("missing metadata or warnings")
	}
}

func TestRepeatedCursorStops(t *testing.T) {
	g, k := &fakeGamma{}, &fakeKalshi{repeat: true}
	var out bytes.Buffer
	if err := execute(context.Background(), g, k, testOptions(), &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	var result report
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(k.calls) != 2 || !strings.Contains(strings.Join(result.Warnings, " "), "cursor repeated") {
		t.Fatalf("report=%+v calls=%v", result, k.calls)
	}
}

func TestFailuresDoNotProducePartialReport(t *testing.T) {
	for _, clients := range []struct {
		g *fakeGamma
		k *fakeKalshi
	}{{&fakeGamma{err: errors.New("Gamma 500")}, &fakeKalshi{}}, {&fakeGamma{}, &fakeKalshi{err: errors.New("Kalshi 429")}}} {
		var out bytes.Buffer
		if err := execute(context.Background(), clients.g, clients.k, testOptions(), &out, io.Discard); err == nil || out.Len() != 0 {
			t.Fatal("failed discovery must not masquerade as complete")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := execute(ctx, &fakeGamma{}, &fakeKalshi{}, testOptions(), io.Discard, io.Discard); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestReportShowsFullSideBySideRulesAndCaveats(t *testing.T) {
	var out bytes.Buffer
	rules := strings.Repeat("exceptional settlement condition ", 8) + "FINAL EXCEPTION"
	result := report{Note: "Sample only", Candidates: []matching.Candidate{{Status: "needs_review", Similarity: 1, Polymarket: matching.Contract{Title: "Alice Smith election", RulesPrimary: rules}, Kalshi: matching.Contract{RulesSecondary: "Kalshi cancellation exception"}, Warnings: []string{"Check thresholds"}}}}
	if err := printReport(&out, result); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Polymarket", "Kalshi", "Primary rules", "Secondary rules", "FINAL EXCEPTION", "Kalshi cancellation exception", "needs_review", "not confidence", "Check thresholds"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q in %s", want, out.String())
		}
	}
}
func TestNoCandidatesIsOnlyASample(t *testing.T) {
	var out bytes.Buffer
	if err := printReport(&out, report{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "does not establish") {
		t.Fatal("must disclose sample coverage")
	}
}
func TestFlagsAndHelp(t *testing.T) {
	for _, args := range [][]string{{"-pages", "0"}, {"-pages", "11"}, {"-page-size", "101"}, {"-polymarket-offset", "-1"}, {"-top", "51"}, {"-min-score", "NaN"}, {"-min-score", "0"}, {"-min-score", "1.1"}, {"-format", "bad"}, {"-timeout", "0s"}, {"extra"}} {
		if err := run(context.Background(), args, io.Discard, io.Discard); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	if err := run(context.Background(), []string{"-help"}, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
}
func TestWrapKeepsLongIDsAndCleansControls(t *testing.T) {
	value := strings.Repeat("a", 120)
	if got := strings.Join(wrap(value, 54), ""); got != value {
		t.Fatal("long identifiers lost")
	}
	if clean("a\x1b\n b") != "a b" {
		t.Fatal("terminal controls not removed")
	}
}
