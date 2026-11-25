// Package settlement validates manually documented settlement scenarios.
// It checks coverage, evidence references and payout arithmetic, not legal
// interpretation, scenario truth, or the completeness of all possible worlds.
package settlement

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Vaibhav2430/Prediction-Market-Arbitrage/internal/matching"
)

var required = []string{"named_candidate_accepts", "other_candidate_accepts", "replacement_after_acceptance", "no_accepted_nominee", "late_acceptance", "cancellation", "disputed_outcome"}

func RequiredCategories() []string { return append([]string{}, required...) }

type Evidence struct {
	ID             string `json:"id"`
	Venue          string `json:"venue"`
	Field          string `json:"field"`
	Excerpt        string `json:"excerpt"`
	Explanation    string `json:"explanation"`
	URL            string `json:"url,omitempty"`
	DocumentSHA256 string `json:"document_sha256,omitempty"`
}
type Assertion struct {
	Outcome  string   `json:"outcome"` // yes, no, unknown, void
	Evidence []string `json:"evidence"`
	Reason   string   `json:"reason"`
}
type Case struct {
	ID          string    `json:"id"`
	Category    string    `json:"category"`
	Description string    `json:"description"`
	Polymarket  Assertion `json:"polymarket"`
	Kalshi      Assertion `json:"kalshi"`
}
type Model struct {
	Version          int        `json:"version"`
	Profile          string     `json:"profile"`
	PairID           string     `json:"pair_id"`
	RulesFingerprint string     `json:"rules_fingerprint"`
	Relation         string     `json:"yes_relation"`
	Scope            string     `json:"scope"`
	Sources          []Evidence `json:"sources"`
	Cases            []Case     `json:"cases"`
}
type Binding struct {
	PairID, RulesFingerprint, Relation string
	Polymarket, Kalshi                 matching.Contract
	TermsURL, TermsSHA256              string
}
type CaseResult struct {
	ID                 string   `json:"id"`
	Category           string   `json:"category"`
	Description        string   `json:"description"`
	PolymarketOutcome  string   `json:"polymarket_yes_outcome"`
	KalshiOutcome      string   `json:"kalshi_yes_outcome"`
	Status             string   `json:"status"`
	FirstPackage       string   `json:"first_package"`
	SecondPackage      string   `json:"second_package"`
	FirstPayout        string   `json:"first_nominal_payout,omitempty"`
	SecondPayout       string   `json:"second_nominal_payout,omitempty"`
	PolymarketEvidence []string `json:"polymarket_evidence"`
	KalshiEvidence     []string `json:"kalshi_evidence"`
	Reasons            []string `json:"reasons"`
}
type Report struct {
	Profile                 string       `json:"profile"`
	Scope                   string       `json:"scope"`
	EvidenceSources         []Evidence   `json:"evidence_sources"`
	PairID                  string       `json:"pair_id"`
	Status                  string       `json:"status"`
	RulesFingerprint        string       `json:"rules_fingerprint"`
	CurrentRulesFingerprint string       `json:"current_rules_fingerprint,omitempty"`
	Mode                    string       `json:"mode"`
	MissingCategories       []string     `json:"missing_categories"`
	Aligned                 int          `json:"aligned_cases"`
	Conflicts               int          `json:"conflicting_cases"`
	Unresolved              int          `json:"unresolved_cases"`
	PayoutFloorKnown        bool         `json:"modeled_payout_floor_known"`
	FirstMinimum            string       `json:"first_modeled_minimum,omitempty"`
	SecondMinimum           string       `json:"second_modeled_minimum,omitempty"`
	Cases                   []CaseResult `json:"cases"`
	Notes                   []string     `json:"notes"`
}

func Fingerprint(m Model) string {
	b, _ := json.Marshal(m)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func normalized(s string) string { return strings.Join(strings.Fields(s), " ") }
func field(c matching.Contract, name string) string {
	switch name {
	case "rules_primary":
		return c.RulesPrimary
	case "rules_secondary":
		return c.RulesSecondary
	case "deadline":
		return c.Deadline
	case "early_close_condition":
		return c.EarlyCloseCondition
	case "latest_expiration":
		return c.LatestExpiration
	case "expected_expiration":
		return c.ExpectedExpiration
	case "title":
		return c.Title
	}
	return ""
}
func Assess(m Model, b Binding) (Report, error) {
	report := Report{Profile: m.Profile, Scope: m.Scope, EvidenceSources: m.Sources, PairID: b.PairID, RulesFingerprint: b.RulesFingerprint, Status: "needs_review", Mode: "saved_rules_only", MissingCategories: []string{}, Cases: []CaseResult{}, Notes: []string{
		"Payout assertions and scenario descriptions are manual interpretations; this validates their structure and arithmetic, not their truth or exhaustiveness.",
		"PDF evidence is bound to document identity and hash; quoted PDF text is a manual transcription, not machine-verified semantic evidence.",
		"Amounts are nominal payouts for one unit per venue, before purchase cost, fees, currency differences or execution risk. No trading or automatic approval.",
	}}
	if m.Profile != "presidential_nomination" {
		return report, fmt.Errorf("unsupported scenario profile; expected presidential_nomination")
	}
	if hash, err := hex.DecodeString(b.RulesFingerprint); err != nil || len(hash) != sha256.Size {
		return report, fmt.Errorf("invalid rule binding fingerprint")
	}
	if strings.TrimSpace(b.PairID) == "" {
		return report, fmt.Errorf("missing bound pair ID")
	}
	if m.Version != 1 || m.PairID != b.PairID || m.RulesFingerprint != b.RulesFingerprint || m.Relation != b.Relation || (m.Relation != "same" && m.Relation != "inverted") || strings.TrimSpace(m.Scope) == "" {
		return report, fmt.Errorf("scenario version, identity, mapping, scope or rule fingerprint is invalid")
	}
	if len(m.Cases) < 1 || len(m.Cases) > 100 || len(m.Sources) > 100 {
		return report, fmt.Errorf("scenario model requires 1..100 cases and at most 100 evidence sources")
	}
	sources := map[string]Evidence{}
	for _, s := range m.Sources {
		if strings.TrimSpace(s.ID) == "" || strings.TrimSpace(s.Excerpt) == "" || strings.TrimSpace(s.Explanation) == "" || (s.Venue != "polymarket" && s.Venue != "kalshi") {
			return report, fmt.Errorf("incomplete evidence source")
		}
		if _, exists := sources[s.ID]; exists {
			return report, fmt.Errorf("duplicate evidence ID %s", s.ID)
		}
		if s.Field == "terms" {
			if s.Venue != "kalshi" || s.URL == "" || s.URL != b.TermsURL || s.DocumentSHA256 != b.TermsSHA256 {
				return report, fmt.Errorf("terms evidence is not bound to reviewed document")
			}
		} else {
			contract := b.Polymarket
			if s.Venue == "kalshi" {
				contract = b.Kalshi
			}
			text := field(contract, s.Field)
			if text == "" || !strings.Contains(normalized(text), normalized(s.Excerpt)) {
				return report, fmt.Errorf("evidence %s excerpt absent from reviewed %s.%s", s.ID, s.Venue, s.Field)
			}
		}
		sources[s.ID] = s
	}
	ids, covered := map[string]bool{}, map[string]bool{}
	minFirst, minSecond := 2, 2
	for _, c := range m.Cases {
		if strings.TrimSpace(c.ID) == "" || ids[c.ID] || strings.TrimSpace(c.Description) == "" {
			return report, fmt.Errorf("scenario requires unique ID and description")
		}
		ids[c.ID] = true
		validCategory := false
		for _, category := range required {
			if c.Category == category {
				validCategory = true
			}
		}
		if !validCategory {
			return report, fmt.Errorf("unsupported scenario category %q", c.Category)
		}
		covered[c.Category] = true
		for _, side := range []struct {
			venue     string
			assertion Assertion
		}{{"polymarket", c.Polymarket}, {"kalshi", c.Kalshi}} {
			a := side.assertion
			if a.Outcome != "yes" && a.Outcome != "no" && a.Outcome != "unknown" && a.Outcome != "void" {
				return report, fmt.Errorf("invalid outcome in %s", c.ID)
			}
			if strings.TrimSpace(a.Reason) == "" {
				return report, fmt.Errorf("scenario %s lacks reasoning", c.ID)
			}
			if (a.Outcome == "yes" || a.Outcome == "no") && len(a.Evidence) == 0 {
				return report, fmt.Errorf("known payout in %s lacks evidence", c.ID)
			}
			for _, id := range a.Evidence {
				s, ok := sources[id]
				if !ok || s.Venue != side.venue {
					return report, fmt.Errorf("scenario %s references missing/wrong-venue evidence", c.ID)
				}
			}
		}
		first, second := "Polymarket YES + Kalshi NO", "Polymarket NO + Kalshi YES"
		if m.Relation == "inverted" {
			first, second = "Polymarket YES + Kalshi YES", "Polymarket NO + Kalshi NO"
		}
		result := CaseResult{ID: c.ID, Category: c.Category, Description: c.Description, PolymarketOutcome: c.Polymarket.Outcome, KalshiOutcome: c.Kalshi.Outcome, FirstPackage: first, SecondPackage: second, PolymarketEvidence: c.Polymarket.Evidence, KalshiEvidence: c.Kalshi.Evidence, Reasons: []string{c.Polymarket.Reason, c.Kalshi.Reason}}
		p, k := c.Polymarket.Outcome, c.Kalshi.Outcome
		known := func(s string) bool { return s == "yes" || s == "no" }
		if !known(p) || !known(k) {
			result.Status = "unresolved"
			report.Unresolved++
		} else {
			py, ky := 0, 0
			if p == "yes" {
				py = 1
			}
			if k == "yes" {
				ky = 1
			}
			a, z := py+(1-ky), (1-py)+ky
			if m.Relation == "inverted" {
				a, z = py+ky, (1-py)+(1-ky)
			}
			result.FirstPayout, result.SecondPayout = fmt.Sprintf("%d.0000", a), fmt.Sprintf("%d.0000", z)
			if a < minFirst {
				minFirst = a
			}
			if z < minSecond {
				minSecond = z
			}
			if a != 1 || z != 1 {
				result.Status = "incompatible_in_model"
				report.Conflicts++
			} else {
				result.Status = "aligned_in_model"
				report.Aligned++
			}
		}
		report.Cases = append(report.Cases, result)
	}
	for _, category := range required {
		if !covered[category] {
			report.MissingCategories = append(report.MissingCategories, category)
		}
	}
	if report.Conflicts > 0 {
		report.Status = "incompatible_in_model"
	} else if report.Unresolved == 0 && len(report.MissingCategories) == 0 {
		report.Status = "compatible_in_model"
	}
	if report.Unresolved == 0 && len(report.MissingCategories) == 0 {
		report.PayoutFloorKnown = true
		report.FirstMinimum = fmt.Sprintf("%d.0000", minFirst)
		report.SecondMinimum = fmt.Sprintf("%d.0000", minSecond)
	}
	return report, nil
}
