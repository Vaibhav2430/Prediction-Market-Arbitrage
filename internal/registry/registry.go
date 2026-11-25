// Package registry records manual pair reviews and detects changes to rules.
// A fingerprint detects changes, not correctness or a human reviewer's identity.
package registry

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Vaibhav2430/Prediction-Market-Arbitrage/internal/kalshi"
	"github.com/Vaibhav2430/Prediction-Market-Arbitrage/internal/matching"
	"github.com/Vaibhav2430/Prediction-Market-Arbitrage/internal/models"
	"github.com/Vaibhav2430/Prediction-Market-Arbitrage/internal/settlement"
)

const Version = 1
const maxBytes = 4 << 20

type Outcome struct {
	Name    string `json:"name"`
	AssetID string `json:"asset_id"`
}
type Snapshot struct {
	Polymarket  matching.Contract  `json:"polymarket"`
	ConditionID string             `json:"polymarket_condition_id"`
	Outcomes    []Outcome          `json:"polymarket_outcomes"`
	Kalshi      matching.Contract  `json:"kalshi"`
	Sources     kalshi.RuleSources `json:"kalshi_sources"`
	TermsSHA256 string             `json:"terms_sha256"`
}
type Review struct {
	Reviewer              string    `json:"reviewer"`
	ReviewedAt            time.Time `json:"reviewed_at"`
	OutcomeEvidence       string    `json:"outcome_evidence"`
	TimingEvidence        string    `json:"timing_evidence"`
	ExceptionEvidence     string    `json:"exception_evidence"`
	SourceEvidence        string    `json:"source_evidence"`
	Findings              []string  `json:"findings"`
	ResidualRisks         []string  `json:"residual_risks"`
	SettlementFingerprint string    `json:"settlement_fingerprint,omitempty"`
}
type Entry struct {
	ID              string            `json:"id"`
	Status          string            `json:"status"`
	Relation        string            `json:"yes_relation"`
	Fingerprint     string            `json:"rules_fingerprint"`
	Snapshot        Snapshot          `json:"reviewed_snapshot"`
	Review          Review            `json:"review"`
	SettlementModel *settlement.Model `json:"settlement_model,omitempty"`
}
type File struct {
	Version int     `json:"version"`
	Entries []Entry `json:"entries"`
}

// BuildSnapshot deliberately excludes fees, indicative prices and liquidity.
func BuildSnapshot(p models.Market, k kalshi.Market, sources kalshi.RuleSources, terms []byte) (Snapshot, error) {
	pc, ok := matching.FromPolymarket(p)
	if !ok || p.ID == "" || p.ConditionID == "" || k.Ticker == "" || k.EventTicker != sources.EventTicker || sources.SeriesTicker == "" || sources.TermsURL == "" || len(terms) == 0 {
		return Snapshot{}, fmt.Errorf("incomplete rule snapshot")
	}
	y, n, _ := p.YesNoOutcomes()
	if y.TokenID == "" || n.TokenID == "" || y.TokenID == n.TokenID {
		return Snapshot{}, fmt.Errorf("missing/distinct YES/NO asset IDs required")
	}
	sum := sha256.Sum256(terms)
	return Snapshot{Polymarket: pc, ConditionID: p.ConditionID, Outcomes: []Outcome{{Name: y.Name, AssetID: y.TokenID}, {Name: n.Name, AssetID: n.TokenID}}, Kalshi: matching.FromKalshi(k), Sources: sources, TermsSHA256: hex.EncodeToString(sum[:])}, nil
}
func Fingerprint(snapshot Snapshot, relation string) string {
	data, _ := json.Marshal(struct {
		Version  int
		Relation string
		Snapshot Snapshot
	}{Version, relation, snapshot})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
func Load(path string) (File, error) {
	f, err := os.Open(path)
	if err != nil {
		return File{}, err
	}
	defer f.Close()
	return Read(f)
}
func Read(r io.Reader) (File, error) {
	var f File
	data, err := io.ReadAll(io.LimitReader(r, maxBytes+1))
	if err != nil {
		return f, err
	}
	if len(data) > maxBytes {
		return f, fmt.Errorf("registry exceeds 4 MiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&f); err != nil {
		return f, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return f, fmt.Errorf("registry must contain exactly one JSON object")
	}
	if f.Version != Version || f.Entries == nil || len(f.Entries) > 100 {
		return f, fmt.Errorf("registry requires version 1 and entries array (max 100)")
	}
	seen := map[string]bool{}
	for _, entry := range f.Entries {
		if seen[entry.ID] {
			return f, fmt.Errorf("duplicate registry ID %q", entry.ID)
		}
		seen[entry.ID] = true
		if err := Validate(entry); err != nil {
			return f, fmt.Errorf("registry entry %q: %w", entry.ID, err)
		}
	}
	return f, nil
}
func Validate(e Entry) error {
	if strings.TrimSpace(e.ID) == "" || (e.Relation != "same" && e.Relation != "inverted") {
		return fmt.Errorf("requires ID and same/inverted relation")
	}
	if e.Status != "needs_review" && e.Status != "rejected" && e.Status != "approved" {
		return fmt.Errorf("status must be needs_review, rejected or approved")
	}
	s := e.Snapshot
	if s.Polymarket.ID == "" || s.Kalshi.ID == "" || s.ConditionID == "" || len(s.Outcomes) != 2 || s.Outcomes[0].AssetID == "" || s.Outcomes[1].AssetID == "" || s.Outcomes[0].AssetID == s.Outcomes[1].AssetID || s.Sources.TermsURL == "" || s.Sources.EventTicker == "" || s.Sources.SeriesTicker == "" {
		return fmt.Errorf("incomplete snapshot")
	}
	if hash, err := hex.DecodeString(s.TermsSHA256); err != nil || len(hash) != sha256.Size {
		return fmt.Errorf("invalid terms SHA-256")
	}
	if e.Fingerprint != Fingerprint(s, e.Relation) {
		return fmt.Errorf("saved fingerprint does not match saved rules/mapping; recapture and review")
	}
	var assessment settlement.Report
	if e.SettlementModel != nil {
		var err error
		assessment, err = AssessSettlement(e)
		if err != nil {
			return fmt.Errorf("invalid settlement model: %w", err)
		}
	}
	if e.Status == "approved" {
		if e.SettlementModel == nil || assessment.Status != "compatible_in_model" {
			return fmt.Errorf("approval requires a complete compatible settlement model; unresolved/conflicting cases block pricing")
		}
		if e.Review.SettlementFingerprint != settlement.Fingerprint(*e.SettlementModel) {
			return fmt.Errorf("scenario model changed or lacks a recorded review fingerprint")
		}
		review := e.Review
		for _, text := range []string{review.Reviewer, review.OutcomeEvidence, review.TimingEvidence, review.ExceptionEvidence, review.SourceEvidence} {
			if strings.TrimSpace(text) == "" {
				return fmt.Errorf("approved entries require reviewer and outcome/timing/exception/source evidence")
			}
		}
		if review.ReviewedAt.IsZero() || review.ReviewedAt.After(time.Now().Add(5*time.Minute)) {
			return fmt.Errorf("approved entries require a valid review timestamp")
		}
		if len(review.Findings) != 0 {
			return fmt.Errorf("unresolved findings cannot be approved")
		}
	}
	return nil
}
func State(e Entry, current Snapshot) string {
	if e.Status == "rejected" {
		return "rejected"
	}
	if e.Fingerprint != Fingerprint(current, e.Relation) {
		return "rules_changed"
	}
	if e.Status != "approved" {
		return "needs_review"
	}
	return "approved_rules_current"
}

// AssessSettlement binds a manual scenario model to the exact reviewed rules.
func AssessSettlement(e Entry) (settlement.Report, error) {
	if e.SettlementModel == nil {
		return settlement.Report{}, fmt.Errorf("no settlement model")
	}
	return settlement.Assess(*e.SettlementModel, settlement.Binding{PairID: e.ID, RulesFingerprint: e.Fingerprint, Relation: e.Relation, Polymarket: e.Snapshot.Polymarket, Kalshi: e.Snapshot.Kalshi, TermsURL: e.Snapshot.Sources.TermsURL, TermsSHA256: e.Snapshot.TermsSHA256})
}
