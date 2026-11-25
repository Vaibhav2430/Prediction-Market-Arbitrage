package registry

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Vaibhav2430/Prediction-Market-Arbitrage/internal/kalshi"
	"github.com/Vaibhav2430/Prediction-Market-Arbitrage/internal/models"
	"github.com/Vaibhav2430/Prediction-Market-Arbitrage/internal/settlement"
)

type fakeGamma struct {
	p   models.Market
	err error
}

func (f fakeGamma) FetchMarket(context.Context, string) (models.Market, error) { return f.p, f.err }

type fakeKalshi struct {
	k                   kalshi.Market
	s                   kalshi.RuleSources
	bookCalls, feeCalls int
}

func (f *fakeKalshi) FetchMarket(context.Context, string) (kalshi.Market, error) { return f.k, nil }
func (f *fakeKalshi) FetchRuleSources(context.Context, string) (kalshi.RuleSources, error) {
	return f.s, nil
}
func (f *fakeKalshi) FetchFeeInfo(context.Context, string) (kalshi.FeeInfo, error) {
	f.feeCalls++
	return kalshi.FeeInfo{Type: "quadratic", Multiplier: json.RawMessage(`1`)}, nil
}
func (f *fakeKalshi) FetchOrderBook(context.Context, string, int) (models.OrderBook, error) {
	f.bookCalls++
	return models.OrderBook{Ticker: f.k.Ticker, YesBids: []models.PriceLevel{{Price: 4000, Quantity: 1000}}, NoBids: []models.PriceLevel{{Price: 6000, Quantity: 1000}}}, nil
}

type fakeTerms struct {
	body string
	err  error
}

func (f fakeTerms) Read(context.Context, string) ([]byte, error) { return []byte(f.body), f.err }

type fakeBooks struct{ stale bool }

func (f fakeBooks) FetchOrderBook(_ context.Context, id string) (models.TokenBook, error) {
	stamp := time.Now()
	if f.stale {
		stamp = stamp.Add(-time.Hour)
	}
	return models.TokenBook{AssetID: id, ConditionID: "cond", Timestamp: stamp, MinOrderSize: 5000000, Asks: []models.TokenLevel{{Price: 3000, Size: 10000000}}}, nil
}
func fixture(t *testing.T) (Entry, fakeGamma, *fakeKalshi, fakeTerms) {
	t.Helper()
	enabled := true
	exponent := 1
	p := models.Market{ID: "123", Question: "Candidate wins and accepts nomination", ConditionID: "cond", Description: "Official rules", EndDate: "2028-11-07T10:00:00Z", FeesEnabled: &enabled, FeeRate: json.RawMessage(`0.01`), FeeExponent: &exponent, Outcomes: []models.Outcome{{Name: "Yes", TokenID: "111"}, {Name: "No", TokenID: "222"}}}
	k := &fakeKalshi{k: kalshi.Market{Ticker: "K", EventTicker: "E", Title: "Candidate wins and accepts nomination", RulesPrimary: "Official rules", CloseTime: time.Date(2028, 11, 7, 10, 0, 0, 0, time.UTC)}, s: kalshi.RuleSources{EventTicker: "E", SeriesTicker: "S", TermsURL: "https://assets.kalshi.com/contract_terms/TEST.pdf"}}
	terms := fakeTerms{body: "%PDF-test baseline"}
	snapshot, err := BuildSnapshot(p, k.k, k.s, []byte(terms.body))
	if err != nil {
		t.Fatal(err)
	}
	entry := Entry{ID: "example", Status: "needs_review", Relation: "same", Snapshot: snapshot, Fingerprint: Fingerprint(snapshot, "same")}
	return entry, fakeGamma{p: p}, k, terms
}
func approve(entry Entry) Entry {
	model := settlement.Model{Version: 1, Profile: "presidential_nomination", PairID: entry.ID, RulesFingerprint: entry.Fingerprint, Relation: entry.Relation, Scope: "Synthetic test review", Sources: []settlement.Evidence{{ID: "p", Venue: "polymarket", Field: "rules_primary", Excerpt: "Official rules", Explanation: "Fixture"}, {ID: "k", Venue: "kalshi", Field: "rules_primary", Excerpt: "Official rules", Explanation: "Fixture"}}}
	for _, category := range settlement.RequiredCategories() {
		model.Cases = append(model.Cases, settlement.Case{ID: category, Category: category, Description: "Synthetic fixture", Polymarket: settlement.Assertion{Outcome: "yes", Evidence: []string{"p"}, Reason: "Fixture"}, Kalshi: settlement.Assertion{Outcome: "yes", Evidence: []string{"k"}, Reason: "Fixture"}})
	}
	entry.SettlementModel = &model
	entry.Status = "approved"
	entry.Review = Review{Reviewer: "Test reviewer", ReviewedAt: time.Now().UTC(), OutcomeEvidence: "Same predicate", TimingEvidence: "Aligned observation and expiry", ExceptionEvidence: "Aligned cancellation and delay outcomes", SourceEvidence: "Same official agency", ResidualRisks: []string{"Execution risk"}}
	entry.Review.SettlementFingerprint = settlement.Fingerprint(model)
	return entry
}

func TestPricesAndFeesDoNotInvalidateRules(t *testing.T) {
	e, g, k, terms := fixture(t)
	value := .123
	g.p.Outcomes[0].IndicativePrice = &value
	g.p.FeeRate = json.RawMessage(`0.05`)
	s, err := BuildSnapshot(g.p, k.k, k.s, []byte(terms.body))
	if err != nil {
		t.Fatal(err)
	}
	if Fingerprint(s, e.Relation) != e.Fingerprint {
		t.Fatal("ordinary prices/fees changed rule fingerprint")
	}
	g.p.Description += " New exception."
	s, err = BuildSnapshot(g.p, k.k, k.s, []byte(terms.body))
	if err != nil {
		t.Fatal(err)
	}
	if State(e, s) != "rules_changed" {
		t.Fatal("rule change not detected")
	}
}
func TestPendingAndRejectedEntriesNeverFetchQuotes(t *testing.T) {
	for _, status := range []string{"needs_review", "rejected"} {
		e, g, k, terms := fixture(t)
		e.Status = status
		result, err := Refresh(context.Background(), e, g, k, fakeBooks{}, terms, 5, time.Minute)
		if err != nil || result.Status != status || k.bookCalls != 0 || k.feeCalls != 0 || len(result.Scenarios) != 0 {
			t.Fatalf("result=%+v err=%v", result, err)
		}
	}
}
func TestApprovedRefreshRequiresUnchangedRulesAndTerms(t *testing.T) {
	e, g, k, terms := fixture(t)
	e = approve(e)
	result, err := Refresh(context.Background(), e, g, k, fakeBooks{}, terms, 5, time.Minute)
	if err != nil || result.Status != "approved_rules_current" || len(result.Scenarios) != 2 || k.bookCalls != 1 || result.Scenarios[0].Status != "conditional_only" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	k.bookCalls, k.feeCalls = 0, 0
	terms.body = "%PDF-changed binding terms"
	result, err = Refresh(context.Background(), e, g, k, fakeBooks{}, terms, 5, time.Minute)
	if err != nil || result.Status != "rules_changed" || k.bookCalls != 0 || k.feeCalls != 0 {
		t.Fatalf("changed terms fetched quotes: %+v %v", result, err)
	}
}
func TestUnverifiableAndStaleSnapshotsWithholdResults(t *testing.T) {
	e, g, k, terms := fixture(t)
	e = approve(e)
	terms.err = errors.New("terms unavailable")
	result, err := Refresh(context.Background(), e, g, k, fakeBooks{}, terms, 5, time.Minute)
	if err == nil || result.Status != "unverifiable" || k.bookCalls != 0 {
		t.Fatal("failed terms verification allowed quotes")
	}
	terms.err = nil
	result, err = Refresh(context.Background(), e, g, k, fakeBooks{stale: true}, terms, 5, time.Minute)
	if err != nil || result.Status != "stale_quotes" || len(result.Scenarios) != 0 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}
func TestMappingDatesIdentitiesAndSourcesAreFingerprinted(t *testing.T) {
	e, _, _, _ := fixture(t)
	if e.Fingerprint == Fingerprint(e.Snapshot, "inverted") {
		t.Fatal("mapping omitted from fingerprint")
	}
	for _, change := range []func(*Snapshot){
		func(s *Snapshot) { s.Polymarket.Deadline = "later" },
		func(s *Snapshot) { s.Kalshi.RulesSecondary = "new exception" },
		func(s *Snapshot) { s.ConditionID = "other" },
		func(s *Snapshot) { s.Sources.TermsURL = "https://assets.kalshi.com/contract_terms/OTHER.pdf" },
	} {
		s := e.Snapshot
		change(&s)
		if State(e, s) != "rules_changed" {
			t.Fatal("material change omitted")
		}
	}
}
func TestRegistryValidation(t *testing.T) {
	e, _, _, _ := fixture(t)
	encode := func(f File) string {
		b, err := json.Marshal(f)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	valid := File{Version: 1, Entries: []Entry{e}}
	if _, err := Read(strings.NewReader(encode(valid))); err != nil {
		t.Fatal(err)
	}
	for _, f := range []File{{Version: 2, Entries: []Entry{}}, {Version: 1}, {Version: 1, Entries: []Entry{e, e}}} {
		if _, err := Read(strings.NewReader(encode(f))); err == nil {
			t.Fatal("invalid registry accepted")
		}
	}
	e.Status = "approved"
	if err := Validate(e); err == nil {
		t.Fatal("approval without evidence accepted")
	}
	e = approve(e)
	e.Review.Findings = []string{"unresolved exception"}
	if err := Validate(e); err == nil {
		t.Fatal("approval with unresolved findings accepted")
	}
	e.Review.Findings = nil
	e.Relation = "inverted"
	if err := Validate(e); err == nil {
		t.Fatal("changed mapping with old fingerprint accepted")
	}
	if _, err := Read(strings.NewReader(encode(valid) + " {}")); err == nil {
		t.Fatal("trailing JSON accepted")
	}
	if _, err := Read(strings.NewReader(`{"version":1,"entries":[],"typo":true}`)); err == nil {
		t.Fatal("unknown registry fields accepted")
	}
}
func TestTermsURLRestrictions(t *testing.T) {
	for _, address := range []string{"http://assets.kalshi.com/contract_terms/T.pdf", "https://example.com/T.pdf", "https://assets.kalshi.com/other/T.pdf", "https://user@assets.kalshi.com/contract_terms/T.pdf", "https://assets.kalshi.com/contract_terms/T.pdf?token=secret"} {
		if _, err := NewTermsClient().Read(context.Background(), address); err == nil {
			t.Fatalf("unsafe URL accepted: %s", address)
		}
	}
}

func TestSettlementReviewCannotBeBypassedOrEditedSilently(t *testing.T) {
	e, _, _, _ := fixture(t)
	e = approve(e)
	if err := Validate(e); err != nil {
		t.Fatal(err)
	}
	e.SettlementModel.Cases[0].Polymarket.Outcome = "unknown"
	e.Review.SettlementFingerprint = settlement.Fingerprint(*e.SettlementModel)
	if err := Validate(e); err == nil {
		t.Fatal("unresolved model accepted even with matching fingerprint")
	}
	e, _, _, _ = fixture(t)
	e = approve(e)
	e.SettlementModel.Cases[0].Description += " edited"
	if err := Validate(e); err == nil {
		t.Fatal("edited model accepted with old review fingerprint")
	}
	e, _, _, _ = fixture(t)
	e = approve(e)
	e.SettlementModel = nil
	if err := Validate(e); err == nil {
		t.Fatal("approval without model accepted")
	}
}
