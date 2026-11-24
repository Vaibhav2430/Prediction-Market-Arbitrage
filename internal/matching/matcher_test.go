package matching

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/Vaibhav2430/Prediction-Market-Arbitrage/internal/kalshi"
	"github.com/Vaibhav2430/Prediction-Market-Arbitrage/internal/models"
)

func fixture(venue, id, title string) Contract {
	return Contract{Venue: venue, ID: id, Title: title, YesLabel: "Yes", NoLabel: "No", Deadline: "2027-01-01T00:00:00Z", RulesPrimary: "Resolves using official published result.", ResolutionSource: "Official source"}
}

func TestExactTextStillNeedsReview(t *testing.T) {
	p := fixture("polymarket", "p1", "Will Bitcoin exceed 100000 in December 2026?")
	k := fixture("kalshi", "k1", p.Title)
	pairs, total, err := Find(context.Background(), []Contract{p}, []Contract{k}, Options{Threshold: .45, Top: 5})
	if err != nil || total != 1 || len(pairs) != 1 {
		t.Fatalf("pairs=%+v total=%d err=%v", pairs, total, err)
	}
	c := pairs[0]
	if c.Status != "needs_review" || c.Similarity != 1 || len(c.Warnings) < 4 {
		t.Fatalf("identical title cannot be verified automatically: %+v", c)
	}
	if !strings.Contains(strings.Join(c.Warnings, " "), "direction is unverified") {
		t.Fatal(c.Warnings)
	}
}

func TestDifferentKnownThresholdsAndDirectionsAreRejected(t *testing.T) {
	p := fixture("polymarket", "p", "Will Bitcoin be above 100000 before December 2026?")
	k := fixture("kalshi", "k", "Will Bitcoin be below 90000 after December 2027?")
	pairs, total, stats, err := FindDetailed(context.Background(), []Contract{p}, []Contract{k}, Options{Threshold: .4, Top: 5})
	if err != nil || len(pairs) != 0 || total != 0 || stats.RejectedPairs != 1 || stats.RejectionReasons["threshold"] != 1 || stats.RejectionReasons["direction"] != 1 {
		t.Fatalf("pairs=%+v stats=%+v err=%v", pairs, stats, err)
	}
}

func TestGenericAndNumericOverlapDoesNotCreatePairs(t *testing.T) {
	p := fixture("polymarket", "p", "Will the market be 100 in 2026?")
	k := fixture("kalshi", "k", "Will the contract be 100 in 2026?")
	pairs, total, err := Find(context.Background(), []Contract{p}, []Contract{k}, Options{Threshold: .1, Top: 5})
	if err != nil || total != 0 || len(pairs) != 0 {
		t.Fatalf("generic overlap: %+v %v", pairs, err)
	}
}

func TestEventContextAndLabelsFindCandidate(t *testing.T) {
	p := fixture("polymarket", "p", "Alice Smith wins election")
	k := fixture("kalshi", "k", "Who wins the election?")
	k.YesLabel = "Alice Smith"
	pairs, _, err := Find(context.Background(), []Contract{p}, []Contract{k}, Options{Threshold: .6, Top: 5})
	if err != nil || len(pairs) != 1 {
		t.Fatalf("context not used: %+v %v", pairs, err)
	}
}

func TestBoundedRankingDeterminismAndQuery(t *testing.T) {
	p := fixture("polymarket", "p", "Alice Smith wins election")
	ks := []Contract{fixture("kalshi", "z", p.Title), fixture("kalshi", "a", p.Title), fixture("kalshi", "b", p.Title)}
	pairs, total, err := Find(context.Background(), []Contract{p}, ks, Options{Threshold: .5, Top: 2, Query: "ALICE"})
	if err != nil || total != 3 || len(pairs) != 2 || pairs[0].Kalshi.ID != "a" || pairs[1].Kalshi.ID != "b" {
		t.Fatalf("pairs=%+v total=%d err=%v", pairs, total, err)
	}
	pairs, total, err = Find(context.Background(), []Contract{p}, ks, Options{Threshold: .5, Top: 2, Query: "unrelated"})
	if err != nil || total != 0 || len(pairs) != 0 {
		t.Fatal("query must filter sampled contracts")
	}
}

func TestMissingAndImpreciseMetadata(t *testing.T) {
	p, k := fixture("polymarket", "p", "Alice Smith election"), fixture("kalshi", "k", "Alice Smith election")
	p.Deadline = "2026-12-31"
	p.RulesPrimary = ""
	p.ResolutionSource = ""
	k.Deadline = ""
	warnings := strings.Join(reviewWarnings(p, k), " ")
	for _, want := range []string{"primary rules are missing", "precise RFC3339", "deadline is missing", "resolution source is missing"} {
		if !strings.Contains(warnings, want) {
			t.Fatal(warnings)
		}
	}
}

func TestAdaptersPreserveRulesAndDeadlineMeanings(t *testing.T) {
	m := models.Market{ID: "p", Question: "Alice Smith wins", EndDate: "2027-01-01T00:00:00Z", Description: "Full rules\nincluding exceptions", ResolutionSource: "source", EventTitles: []string{"Election 2026"}, Outcomes: []models.Outcome{{Name: "No"}, {Name: "Yes"}}}
	p, ok := FromPolymarket(m)
	if !ok || p.RulesPrimary != m.Description || p.Event != "Election 2026" || p.YesLabel != "Yes" || p.DeadlineKind != "Gamma endDate" {
		t.Fatalf("adapter=%+v", p)
	}
	m.Outcomes = []models.Outcome{{Name: "Up"}, {Name: "Down"}}
	if _, ok := FromPolymarket(m); ok {
		t.Fatal("non-YES/NO contract accepted")
	}
	k := FromKalshi(kalshi.Market{Ticker: "K", CloseTime: time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC), RulesPrimary: "rules", RulesSecondary: "exception", ExpectedExpiration: "later", EarlyCloseCondition: "can close early"})
	if k.DeadlineKind != "trading close_time" || k.RulesSecondary != "exception" || k.ExpectedExpiration != "later" || k.EarlyCloseCondition != "can close early" {
		t.Fatalf("adapter=%+v", k)
	}
}

func TestCancellationAndInvalidOptions(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := Find(ctx, nil, nil, Options{Threshold: .5, Top: 1}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for _, o := range []Options{{Threshold: 0, Top: 1}, {Threshold: math.NaN(), Top: 1}, {Threshold: math.Inf(1), Top: 1}, {Threshold: 1.1, Top: 1}, {Threshold: .5, Top: 0}, {Threshold: .5, Top: 51}} {
		if _, _, err := Find(context.Background(), nil, nil, o); err == nil {
			t.Fatalf("accepted %+v", o)
		}
	}
}
