// Package matching ranks potential contract pairs for human review only.
// Text similarity does not establish settlement equivalence or direction.
package matching

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/Vaibhav2430/Prediction-Market-Arbitrage/internal/kalshi"
	"github.com/Vaibhav2430/Prediction-Market-Arbitrage/internal/models"
)

// Contract preserves original wording and deadline semantics for comparison.
type Contract struct {
	Venue               string `json:"venue"`
	ID                  string `json:"id"`
	Title               string `json:"title"`
	Event               string `json:"event"`
	YesLabel            string `json:"yes_label"`
	NoLabel             string `json:"no_label"`
	Deadline            string `json:"deadline"`
	DeadlineKind        string `json:"deadline_kind"`
	ExpectedExpiration  string `json:"expected_expiration,omitempty"`
	LatestExpiration    string `json:"latest_expiration,omitempty"`
	EarlyCloseCondition string `json:"early_close_condition,omitempty"`
	RulesPrimary        string `json:"rules_primary"`
	RulesSecondary      string `json:"rules_secondary,omitempty"`
	ResolutionSource    string `json:"resolution_source,omitempty"`
}

func FromPolymarket(m models.Market) (Contract, bool) {
	yes, no, ok := m.YesNoOutcomes()
	if !ok {
		return Contract{}, false
	}
	return Contract{Venue: "polymarket", ID: m.ID, Title: m.Question, Event: strings.Join(m.EventTitles, "; "), YesLabel: yes.Name, NoLabel: no.Name, Deadline: m.EndDate, DeadlineKind: "Gamma endDate", RulesPrimary: m.Description, ResolutionSource: m.ResolutionSource}, true
}
func FromKalshi(m kalshi.Market) Contract {
	deadline := ""
	if !m.CloseTime.IsZero() {
		deadline = m.CloseTime.UTC().Format(time.RFC3339Nano)
	}
	return Contract{Venue: "kalshi", ID: m.Ticker, Title: m.Title, Event: m.EventTicker, YesLabel: m.YesSubtitle, NoLabel: m.NoSubtitle, Deadline: deadline, DeadlineKind: "trading close_time", ExpectedExpiration: m.ExpectedExpiration, LatestExpiration: m.LatestExpiration, EarlyCloseCondition: m.EarlyCloseCondition, RulesPrimary: m.RulesPrimary, RulesSecondary: m.RulesSecondary}
}

type Candidate struct {
	Status          string   `json:"status"`
	Similarity      float64  `json:"text_similarity"`
	SharedTerms     []string `json:"shared_terms"`
	Polymarket      Contract `json:"polymarket"`
	Kalshi          Contract `json:"kalshi"`
	Warnings        []string `json:"review_warnings"`
	PolymarketFacts Facts    `json:"polymarket_facts"`
	KalshiFacts     Facts    `json:"kalshi_facts"`
}

type Options struct {
	Threshold float64
	Top       int
	Query     string
}

type prepared struct {
	contract       Contract
	title, context map[string]bool
	facts          Facts
}

// Find returns the best bounded set and the total number above the threshold.
// Rankings use lexical Dice overlap after rejecting recognized structured
// conflicts. Unparsed details remain warnings; no pair is automatically approved.
type FilterStats struct {
	TextCandidates   int            `json:"text_candidates"`
	RejectedPairs    int            `json:"rejected_pairs"`
	RejectionReasons map[string]int `json:"rejection_reasons"`
}

func Find(ctx context.Context, poly, kal []Contract, options Options) ([]Candidate, int, error) {
	candidates, total, _, err := FindDetailed(ctx, poly, kal, options)
	return candidates, total, err
}

func FindDetailed(ctx context.Context, poly, kal []Contract, options Options) ([]Candidate, int, FilterStats, error) {
	stats := FilterStats{RejectionReasons: map[string]int{}}
	if math.IsNaN(options.Threshold) || math.IsInf(options.Threshold, 0) || options.Threshold <= 0 || options.Threshold > 1 || options.Top < 1 || options.Top > 50 {
		return nil, 0, stats, fmt.Errorf("threshold must be in (0,1] and top must be 1..50")
	}
	prepare := func(contracts []Contract) []prepared {
		result := make([]prepared, 0, len(contracts))
		for _, c := range contracts {
			// Query filters sampled metadata; it is not a remote full-catalog search.
			if !strings.Contains(strings.ToLower(c.Title+" "+c.Event+" "+c.YesLabel), strings.ToLower(options.Query)) {
				continue
			}
			result = append(result, prepared{c, terms(c.Title), terms(c.Title + " " + c.YesLabel + " " + c.Event), Extract(c)})
		}
		return result
	}
	p, k := prepare(poly), prepare(kal)
	best := make([]Candidate, 0, options.Top)
	total := 0
	for _, a := range p {
		for _, b := range k {
			if err := ctx.Err(); err != nil {
				return nil, 0, stats, err
			}
			titleScore, titleShared := similarity(a.title, b.title)
			contextScore, contextShared := similarity(a.context, b.context)
			score, shared := titleScore, titleShared
			if contextScore > score {
				score, shared = contextScore, contextShared
			}
			if score < options.Threshold || len(shared) < 2 {
				continue
			}
			stats.TextCandidates++
			conflicts := Conflicts(a.facts, b.facts)
			if len(conflicts) > 0 {
				stats.RejectedPairs++
				for _, reason := range conflicts {
					stats.RejectionReasons[reason]++
				}
				continue
			}
			total++
			candidate := Candidate{Status: "needs_review", Similarity: score, SharedTerms: shared, Polymarket: a.contract, Kalshi: b.contract, PolymarketFacts: a.facts, KalshiFacts: b.facts}
			// Keep ranking memory bounded even if many pairs are similar.
			if len(best) < options.Top {
				best = append(best, candidate)
			} else {
				worst := 0
				for i := 1; i < len(best); i++ {
					if better(best[worst], best[i]) {
						worst = i
					}
				}
				if better(candidate, best[worst]) {
					best[worst] = candidate
				}
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, 0, stats, err
	}
	sort.Slice(best, func(i, j int) bool { return better(best[i], best[j]) })
	for i := range best {
		best[i].Warnings = reviewWarnings(best[i].Polymarket, best[i].Kalshi)
		for _, side := range []struct {
			name  string
			facts Facts
		}{{"Polymarket", best[i].PolymarketFacts}, {"Kalshi", best[i].KalshiFacts}} {
			if len(side.facts.Unknown) > 0 {
				best[i].Warnings = append(best[i].Warnings, side.name+" unparsed details: "+strings.Join(side.facts.Unknown, ", ")+". Missing details are not evidence of agreement.")
			}
		}
	}
	return best, total, stats, nil
}

func better(a, b Candidate) bool {
	if a.Similarity != b.Similarity {
		return a.Similarity > b.Similarity
	}
	if a.Polymarket.ID != b.Polymarket.ID {
		return a.Polymarket.ID < b.Polymarket.ID
	}
	return a.Kalshi.ID < b.Kalshi.ID
}

var stop = func() map[string]bool {
	m := make(map[string]bool)
	for _, s := range strings.Fields("a an the will would be is are was were do does did to of in on at for by with this that market markets contract contracts yes no if then and or") {
		m[s] = true
	}
	return m
}()

func terms(text string) map[string]bool {
	text = strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return ' '
	}, text)
	result := make(map[string]bool)
	for _, term := range strings.Fields(text) {
		// Numbers are compared separately; shared years/thresholds are not evidence
		// of the same event. Negations and directional words are retained.
		hasLetter := false
		for _, r := range term {
			if unicode.IsLetter(r) {
				hasLetter = true
				break
			}
		}
		if hasLetter && !stop[term] {
			result[term] = true
		}
	}
	return result
}
func similarity(a, b map[string]bool) (float64, []string) {
	if len(a)+len(b) == 0 {
		return 0, nil
	}
	shared := []string{}
	for term := range a {
		if b[term] {
			shared = append(shared, term)
		}
	}
	sort.Strings(shared)
	return 2 * float64(len(shared)) / float64(len(a)+len(b)), shared
}

var numbers = regexp.MustCompile(`[-+]?\d+(?:\.\d+)?`)

func numericTerms(c Contract) string {
	set := make(map[string]bool)
	for _, n := range numbers.FindAllString(c.Title+" "+c.YesLabel, -1) {
		set[n] = true
	}
	sorted := make([]string, 0, len(set))
	for n := range set {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)
	return strings.Join(sorted, ",")
}
func directions(c Contract) string {
	set := terms(c.Title + " " + c.YesLabel)
	result := []string{}
	for _, word := range []string{"not", "never", "before", "after", "above", "below", "over", "under", "least", "most", "less", "more"} {
		if set[word] {
			result = append(result, word)
		}
	}
	return strings.Join(result, ",")
}
func reviewWarnings(p, k Contract) []string {
	warnings := []string{
		"Manual review required: text similarity does not establish contract equivalence.",
		"YES/NO direction is unverified; confirm that both YES outcomes describe the same predicate.",
		"Gamma endDate and Kalshi close_time have different meanings; verify event cutoff, time zone, and settlement timing.",
		"Review resolution sources, thresholds, cancellation/postponement rules, and exceptional settlement conditions in full.",
	}
	if numericTerms(p) != numericTerms(k) {
		warnings = append(warnings, "Numbers in titles/outcome labels differ or are missing; verify thresholds and event dates.")
	}
	if directions(p) != directions(k) {
		warnings = append(warnings, "Directional or negative wording differs; outcomes may be inverted or describe different predicates.")
	}
	for _, c := range []Contract{p, k} {
		if strings.TrimSpace(c.RulesPrimary) == "" {
			warnings = append(warnings, c.Venue+" primary rules are missing.")
		}
		if c.Deadline == "" {
			warnings = append(warnings, c.Venue+" deadline is missing.")
		} else if _, err := time.Parse(time.RFC3339Nano, c.Deadline); err != nil {
			warnings = append(warnings, c.Venue+" deadline lacks a valid precise RFC3339 timestamp; verify original text.")
		}
	}
	pt, pe := time.Parse(time.RFC3339Nano, p.Deadline)
	kt, ke := time.Parse(time.RFC3339Nano, k.Deadline)
	if pe == nil && ke == nil && !pt.Equal(kt) {
		warnings = append(warnings, "Displayed deadlines differ; this pair is not verified for a common event window.")
	}
	if strings.TrimSpace(p.ResolutionSource) == "" {
		warnings = append(warnings, "Polymarket resolution source is missing; consult the original contract.")
	}
	return warnings
}
