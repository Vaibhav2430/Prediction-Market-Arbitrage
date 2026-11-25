package settlement

import (
	"strings"
	"testing"

	"github.com/Vaibhav2430/Prediction-Market-Arbitrage/internal/matching"
)

func fixture() (Model, Binding) {
	b := Binding{PairID: "pair", RulesFingerprint: strings.Repeat("a", 64), Relation: "same", Polymarket: matching.Contract{RulesPrimary: "Official rule"}, Kalshi: matching.Contract{RulesPrimary: "Official rule"}, TermsURL: "https://assets.kalshi.com/contract_terms/T.pdf", TermsSHA256: strings.Repeat("b", 64)}
	m := Model{Version: 1, Profile: "presidential_nomination", PairID: b.PairID, RulesFingerprint: b.RulesFingerprint, Relation: b.Relation, Scope: "Synthetic complete test model", Sources: []Evidence{{ID: "p", Venue: "polymarket", Field: "rules_primary", Excerpt: "Official rule", Explanation: "Test source"}, {ID: "k", Venue: "kalshi", Field: "rules_primary", Excerpt: "Official rule", Explanation: "Test source"}}}
	for _, category := range RequiredCategories() {
		m.Cases = append(m.Cases, Case{ID: category, Category: category, Description: "Synthetic supported case", Polymarket: Assertion{Outcome: "yes", Evidence: []string{"p"}, Reason: "Synthetic fixture"}, Kalshi: Assertion{Outcome: "yes", Evidence: []string{"k"}, Reason: "Synthetic fixture"}})
	}
	return m, b
}
func TestAlignedModelRemainsAManualModel(t *testing.T) {
	m, b := fixture()
	r, err := Assess(m, b)
	if err != nil || r.Status != "compatible_in_model" || !r.PayoutFloorKnown || r.FirstMinimum != "1.0000" || r.SecondMinimum != "1.0000" {
		t.Fatalf("report=%+v err=%v", r, err)
	}
	if !strings.Contains(strings.Join(r.Notes, " "), "not their truth") {
		t.Fatal("model limit missing")
	}
}
func TestBothLoseScenarioRejectsSameMapping(t *testing.T) {
	m, b := fixture()
	m.Cases[0].Polymarket.Outcome = "no"
	m.Cases[0].Kalshi.Outcome = "yes"
	r, err := Assess(m, b)
	if err != nil || r.Status != "incompatible_in_model" || r.Conflicts != 1 || r.FirstMinimum != "0.0000" || r.Cases[0].SecondPayout != "2.0000" {
		t.Fatalf("report=%+v err=%v", r, err)
	}
}
func TestInvertedMappingArithmetic(t *testing.T) {
	m, b := fixture()
	m.Relation = "inverted"
	b.Relation = "inverted"
	for i := range m.Cases {
		m.Cases[i].Kalshi.Outcome = "no"
	}
	r, err := Assess(m, b)
	if err != nil || r.Status != "compatible_in_model" || r.FirstMinimum != "1.0000" {
		t.Fatalf("report=%+v err=%v", r, err)
	}
	m.Cases[0].Kalshi.Outcome = "yes"
	r, err = Assess(m, b)
	if err != nil || r.Status != "incompatible_in_model" || r.Cases[0].SecondPayout != "0.0000" {
		t.Fatalf("report=%+v err=%v", r, err)
	}
}
func TestUnknownAndVoidNeverProduceFloor(t *testing.T) {
	for _, outcome := range []string{"unknown", "void"} {
		m, b := fixture()
		m.Cases[0].Polymarket.Outcome = outcome
		r, err := Assess(m, b)
		if err != nil || r.Status != "needs_review" || r.PayoutFloorKnown || r.FirstMinimum != "" || r.Cases[0].FirstPayout != "" || r.Unresolved != 1 {
			t.Fatalf("report=%+v err=%v", r, err)
		}
	}
}
func TestMissingCoverageBlocksFloor(t *testing.T) {
	m, b := fixture()
	m.Cases = m.Cases[:2]
	r, err := Assess(m, b)
	if err != nil || r.Status != "needs_review" || len(r.MissingCategories) != 5 || r.PayoutFloorKnown {
		t.Fatalf("report=%+v err=%v", r, err)
	}
}
func TestEvidenceAndBindingValidation(t *testing.T) {
	for _, change := range []func(*Model){
		func(m *Model) { m.RulesFingerprint = "stale" }, func(m *Model) { m.PairID = "different" }, func(m *Model) { m.Relation = "inverted" },
		func(m *Model) { m.Sources[0].Excerpt = "not present" }, func(m *Model) { m.Cases[0].Polymarket.Evidence = []string{"k"} },
		func(m *Model) { m.Cases[0].Polymarket.Evidence = nil }, func(m *Model) { m.Cases[0].Kalshi.Outcome = "maybe" },
		func(m *Model) { m.Cases[0].ID = m.Cases[1].ID }, func(m *Model) { m.Cases[0].Category = "other" },
	} {
		m, b := fixture()
		change(&m)
		if _, err := Assess(m, b); err == nil {
			t.Fatal("invalid model accepted")
		}
	}
	m, _ := fixture()
	old := Fingerprint(m)
	m.Cases[0].Description += " revised"
	if old == Fingerprint(m) {
		t.Fatal("scenario edit not fingerprinted")
	}
}
func TestTermsIdentityCannotBeSubstituted(t *testing.T) {
	m, b := fixture()
	m.Sources[1].Field = "terms"
	m.Sources[1].URL = b.TermsURL
	m.Sources[1].DocumentSHA256 = b.TermsSHA256
	if _, err := Assess(m, b); err != nil {
		t.Fatal(err)
	}
	m.Sources[1].DocumentSHA256 = "different"
	if _, err := Assess(m, b); err == nil {
		t.Fatal("terms change accepted")
	}
}

func TestUnsupportedProfileAndBlankEvidence(t *testing.T) {
	m, b := fixture()
	m.Profile = "unsupported"
	if _, err := Assess(m, b); err == nil {
		t.Fatal("unsupported profile accepted")
	}
	m, b = fixture()
	m.Sources[0].Excerpt = "   "
	if _, err := Assess(m, b); err == nil {
		t.Fatal("blank evidence accepted")
	}
}
