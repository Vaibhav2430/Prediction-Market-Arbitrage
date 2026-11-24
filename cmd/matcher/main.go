// Matcher discovers potential cross-platform contract pairs for review.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"
	"unicode"

	"github.com/Vaibhav2430/Prediction-Market-Arbitrage/internal/kalshi"
	"github.com/Vaibhav2430/Prediction-Market-Arbitrage/internal/matching"
	"github.com/Vaibhav2430/Prediction-Market-Arbitrage/internal/polymarket"
)

type gammaReader interface {
	FetchActiveMarkets(context.Context, int, int) (polymarket.Result, error)
}
type kalshiReader interface {
	FetchOpenMarkets(context.Context, int, string, string) (kalshi.MarketPage, error)
}
type options struct {
	pages, size, offset, top           int
	threshold                          float64
	query, series, cursor, format      string
	timeout                            time.Duration
	topic, category, after, before     string
	maxSeries, topicEvents, searchPage int
}
type report struct {
	Topic                   string               `json:"topic,omitempty"`
	KalshiCategory          string               `json:"kalshi_category,omitempty"`
	KalshiSeries            []kalshi.Series      `json:"kalshi_series,omitempty"`
	KalshiSeriesMatched     int                  `json:"kalshi_series_matched,omitempty"`
	KalshiSeriesCursors     map[string]string    `json:"kalshi_series_cursors,omitempty"`
	NextSearchPage          int                  `json:"next_polymarket_search_page,omitempty"`
	PolymarketSearchHasMore bool                 `json:"polymarket_search_has_more"`
	WindowAfter             string               `json:"deadline_after,omitempty"`
	WindowBefore            string               `json:"deadline_before,omitempty"`
	WindowOmittedPolymarket int                  `json:"window_omitted_polymarket"`
	WindowOmittedKalshi     int                  `json:"window_omitted_kalshi"`
	PolymarketCapped        bool                 `json:"polymarket_capped"`
	KalshiCapped            bool                 `json:"kalshi_capped"`
	Status                  string               `json:"status"`
	Note                    string               `json:"note"`
	PolymarketMarkets       int                  `json:"polymarket_markets"`
	KalshiMarkets           int                  `json:"kalshi_markets"`
	PolymarketPages         int                  `json:"polymarket_pages"`
	KalshiPages             int                  `json:"kalshi_pages"`
	NextPolymarketOffset    int                  `json:"next_polymarket_offset"`
	NextKalshiCursor        string               `json:"next_kalshi_cursor"`
	Warnings                []string             `json:"warnings"`
	CandidateCount          int                  `json:"candidate_count"`
	Compatibility           matching.FilterStats `json:"compatibility_filters"`
	Candidates              []matching.Candidate `json:"candidates"`
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "matcher:", err)
		os.Exit(1)
	}
}
func run(ctx context.Context, args []string, out, errOut io.Writer) error {
	o := options{}
	flags := flag.NewFlagSet("matcher", flag.ContinueOnError)
	flags.SetOutput(errOut)
	flags.IntVar(&o.pages, "pages", 3, "maximum pages per venue (1..10)")
	flags.IntVar(&o.size, "page-size", 100, "API records per page per venue (1..100)")
	flags.IntVar(&o.offset, "polymarket-offset", 0, "first Gamma record offset")
	flags.StringVar(&o.cursor, "kalshi-cursor", "", "first Kalshi page cursor")
	flags.StringVar(&o.series, "kalshi-series", "", "optional Kalshi series filter")
	flags.StringVar(&o.topic, "topic", "", "remote Polymarket topic search plus related Kalshi series discovery")
	flags.StringVar(&o.category, "kalshi-category", "", "case-sensitive Kalshi category (inferred for common topics)")
	flags.IntVar(&o.maxSeries, "max-series", 5, "maximum related Kalshi series to search (1..10)")
	flags.IntVar(&o.topicEvents, "topic-events", 5, "Polymarket search events per page (1..20; nested markets can be numerous)")
	flags.IntVar(&o.searchPage, "search-page", 1, "first Polymarket topic-search page (1..90)")
	flags.StringVar(&o.after, "deadline-after", "", "inclusive reported-deadline lower bound (RFC3339; topic mode only)")
	flags.StringVar(&o.before, "deadline-before", "", "inclusive reported-deadline upper bound (RFC3339; topic mode only)")
	flags.StringVar(&o.query, "query", "", "case-insensitive substring filter on sampled title/event/YES-label metadata")
	flags.Float64Var(&o.threshold, "min-score", 0.45, "minimum lexical similarity (0 < score <= 1; not a probability)")
	flags.IntVar(&o.top, "top", 5, "maximum candidate pairs to display (1..50)")
	flags.StringVar(&o.format, "format", "text", "output format: text or json")
	flags.DurationVar(&o.timeout, "timeout", 30*time.Second, "overall timeout; each HTTP request also has a 10s timeout")
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments")
	}
	if o.pages < 1 || o.pages > 10 || o.size < 1 || o.size > 100 || o.offset < 0 || o.offset > math.MaxInt-o.pages*o.size || o.top < 1 || o.top > 50 || math.IsNaN(o.threshold) || math.IsInf(o.threshold, 0) || o.threshold <= 0 || o.threshold > 1 || o.timeout <= 0 {
		return fmt.Errorf("invalid page, offset, top, score, or timeout value; see -help")
	}
	if o.format != "text" && o.format != "json" {
		return fmt.Errorf("format must be text or json")
	}
	o.topic = strings.TrimSpace(o.topic)
	if o.maxSeries < 1 || o.maxSeries > 10 || o.topicEvents < 1 || o.topicEvents > 20 || o.searchPage < 1 || o.searchPage > 90 || len(o.topic) > 256 {
		return fmt.Errorf("invalid topic discovery bounds; see -help")
	}
	if o.topic != "" {
		if o.offset != 0 || (o.cursor != "" && o.series == "") {
			return fmt.Errorf("topic mode uses -search-page; a Kalshi cursor requires an explicit -kalshi-series")
		}
		if o.category == "" {
			o.category = topicCategory(o.topic)
		}
		if o.series == "" && o.category == "" {
			return fmt.Errorf("specify -kalshi-category or -kalshi-series for this topic")
		}
	} else if o.after != "" || o.before != "" || o.category != "" {
		return fmt.Errorf("deadline/category filters require -topic")
	}
	for _, value := range []string{o.after, o.before} {
		if value != "" {
			if _, err := time.Parse(time.RFC3339Nano, value); err != nil {
				return fmt.Errorf("deadline bounds must be precise RFC3339 timestamps")
			}
		}
	}
	if o.after != "" && o.before != "" {
		a, _ := time.Parse(time.RFC3339Nano, o.after)
		b, _ := time.Parse(time.RFC3339Nano, o.before)
		if a.After(b) {
			return fmt.Errorf("deadline-after must not follow deadline-before")
		}
	}
	ctx, cancel := context.WithTimeout(ctx, o.timeout)
	defer cancel()
	if o.topic != "" {
		return executeTopic(ctx, polymarket.NewClient(), kalshi.NewClient(), o, out, errOut)
	}
	return execute(ctx, polymarket.NewClient(), kalshi.NewClient(), o, out, errOut)
}

func execute(ctx context.Context, gamma gammaReader, kal kalshiReader, o options, out, errOut io.Writer) error {
	result := report{Status: "needs_review", Note: "Bounded catalog sample only. Scores measure shared wording, not equivalence or probability. Every pair requires manual rule and outcome-direction review. No pricing, arbitrage calculation, or trading.", Warnings: []string{}, Candidates: []matching.Candidate{}, NextPolymarketOffset: o.offset}
	poly, ks := []matching.Contract{}, []matching.Contract{}
	seenP, seenK := map[string]bool{}, map[string]bool{}
	for page := 0; page < o.pages; page++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		offset := o.offset + page*o.size
		data, err := gamma.FetchActiveMarkets(ctx, o.size, offset)
		if err != nil {
			return fmt.Errorf("Polymarket discovery offset %d: %w", offset, err)
		}
		result.PolymarketPages++
		result.NextPolymarketOffset = offset + o.size
		for _, warning := range data.Warnings {
			result.Warnings = append(result.Warnings, "Polymarket: "+warning.Error())
		}
		for _, m := range data.Markets {
			if seenP[m.ID] {
				continue
			}
			seenP[m.ID] = true
			if c, ok := matching.FromPolymarket(m); ok {
				poly = append(poly, c)
			}
		}
		// Client filtering can shorten a page, so do not infer end-of-catalog from
		// retained record count. Requests are bounded by -pages even for empty pages.
	}
	cursor := o.cursor
	seenCursors := map[string]bool{}
	if cursor != "" {
		seenCursors[cursor] = true
	}
	for page := 0; page < o.pages; page++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		data, err := kal.FetchOpenMarkets(ctx, o.size, cursor, o.series)
		if err != nil {
			return fmt.Errorf("Kalshi discovery page %d: %w", page+1, err)
		}
		result.KalshiPages++
		for _, warning := range data.Warnings {
			result.Warnings = append(result.Warnings, "Kalshi: "+warning.Error())
		}
		for _, m := range data.Markets {
			if seenK[m.Ticker] {
				continue
			}
			seenK[m.Ticker] = true
			ks = append(ks, matching.FromKalshi(m))
		}
		cursor = data.Cursor
		result.NextKalshiCursor = cursor
		if cursor == "" {
			break
		}
		if seenCursors[cursor] {
			result.Warnings = append(result.Warnings, "Kalshi cursor repeated; pagination stopped to avoid repeating pages.")
			break
		}
		seenCursors[cursor] = true
	}
	result.PolymarketMarkets, result.KalshiMarkets = len(poly), len(ks)
	return finishReport(ctx, result, poly, ks, o, out, errOut)
}

func finishReport(ctx context.Context, result report, poly, ks []matching.Contract, o options, out, errOut io.Writer) error {

	candidates, total, stats, err := matching.FindDetailed(ctx, poly, ks, matching.Options{Threshold: o.threshold, Top: o.top, Query: o.query})
	if err != nil {
		return err
	}
	result.Candidates, result.CandidateCount, result.Compatibility = candidates, total, stats
	for _, warning := range result.Warnings {
		fmt.Fprintln(errOut, "warning:", clean(warning))
	}
	if o.format == "json" {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}
	return printReport(out, result)
}

func clean(s string) string {
	return strings.Join(strings.Fields(strings.Map(func(ch rune) rune {
		if unicode.IsControl(ch) {
			return ' '
		}
		return ch
	}, s)), " ")
}
func present(s string) string {
	if strings.TrimSpace(s) == "" {
		return "unavailable"
	}
	return clean(s)
}
func printReport(out io.Writer, r report) error {
	var b strings.Builder
	fmt.Fprintln(&b, "Cross-platform candidate matcher — NEEDS MANUAL REVIEW")
	fmt.Fprintln(&b, r.Note)
	if r.Topic != "" {
		fmt.Fprintf(&b, "Topic: %s | Kalshi category: %s\n", clean(r.Topic), present(r.KalshiCategory))
		fmt.Fprintf(&b, "Kalshi related series: %d matched; %d selected\n", r.KalshiSeriesMatched, len(r.KalshiSeries))
		for _, series := range r.KalshiSeries {
			fmt.Fprintf(&b, "  %s — %s\n", clean(series.Ticker), clean(series.Title))
		}
		if r.WindowAfter != "" || r.WindowBefore != "" {
			fmt.Fprintf(&b, "Reported deadline window: %s to %s (platform deadline meanings differ)\n", present(r.WindowAfter), present(r.WindowBefore))
		}
		fmt.Fprintf(&b, "Window omitted: %d Polymarket, %d Kalshi (includes missing/imprecise deadlines)\n", r.WindowOmittedPolymarket, r.WindowOmittedKalshi)
	}
	fmt.Fprintf(&b, "Sample: %d Polymarket YES/NO markets (%d pages), %d Kalshi markets (%d pages).\n", r.PolymarketMarkets, r.PolymarketPages, r.KalshiMarkets, r.KalshiPages)
	fmt.Fprintf(&b, "%d candidate pairs after compatibility filters; showing %d.\n", r.CandidateCount, len(r.Candidates))
	fmt.Fprintf(&b, "Compatibility: %d text candidates; %d rejected for recognized conflicts.\n", r.Compatibility.TextCandidates, r.Compatibility.RejectedPairs)
	reasons := make([]string, 0, len(r.Compatibility.RejectionReasons))
	for reason := range r.Compatibility.RejectionReasons {
		reasons = append(reasons, reason)
	}
	sort.Strings(reasons)
	for _, reason := range reasons {
		fmt.Fprintf(&b, "  Rejected for %s: %d pairs\n", reason, r.Compatibility.RejectionReasons[reason])
	}
	if len(reasons) > 0 {
		fmt.Fprintln(&b, "A pair may have multiple rejection reasons.")
	}
	for i, c := range r.Candidates {
		fmt.Fprintf(&b, "\nCandidate %d — needs_review — text similarity %.3f (not confidence)\nShared terms: %s\n", i+1, c.Similarity, strings.Join(c.SharedTerms, ", "))
		p, k := c.Polymarket, c.Kalshi
		compareRow(&b, "Field", "Polymarket", "Kalshi")
		for _, row := range [][3]string{
			{"ID", p.ID, k.ID}, {"Title", p.Title, k.Title}, {"Event", p.Event, k.Event},
			{"YES API label", p.YesLabel, k.YesLabel}, {"NO API label", p.NoLabel, k.NoLabel},
			{"Deadline kind", p.DeadlineKind, k.DeadlineKind}, {"Deadline", p.Deadline, k.Deadline},
			{"Expected expiry", p.ExpectedExpiration, k.ExpectedExpiration}, {"Latest expiry", p.LatestExpiration, k.LatestExpiration},
			{"Early close", p.EarlyCloseCondition, k.EarlyCloseCondition}, {"Resolution source", p.ResolutionSource, k.ResolutionSource},
			{"Parsed type", c.PolymarketFacts.Kind, c.KalshiFacts.Kind},
			{"Parsed direction", c.PolymarketFacts.Direction, c.KalshiFacts.Direction},
			{"Parsed lower", c.PolymarketFacts.Lower, c.KalshiFacts.Lower},
			{"Parsed upper", c.PolymarketFacts.Upper, c.KalshiFacts.Upper},
			{"Parsed source", c.PolymarketFacts.Source, c.KalshiFacts.Source},
			{"Measurement", c.PolymarketFacts.Measurement, c.KalshiFacts.Measurement},
			{"Observation", fmt.Sprintf("%+v", c.PolymarketFacts.Observation), fmt.Sprintf("%+v", c.KalshiFacts.Observation)},
			{"Primary rules", p.RulesPrimary, k.RulesPrimary}, {"Secondary rules", p.RulesSecondary, k.RulesSecondary},
		} {
			compareRow(&b, row[0], present(row[1]), present(row[2]))
		}
		for _, warning := range c.Warnings {
			fmt.Fprintln(&b, "  Review:", warning)
		}
	}
	if len(r.Candidates) == 0 {
		fmt.Fprintln(&b, "No candidate pairs in this sample. This does not establish that the full catalogs have no overlapping markets.")
	}
	if r.Topic != "" {
		fmt.Fprintf(&b, "\nNext Polymarket search page: %d | API has more: %t\n", r.NextSearchPage, r.PolymarketSearchHasMore)
		for _, series := range r.KalshiSeries {
			fmt.Fprintf(&b, "Kalshi %s next cursor: %s\n", clean(series.Ticker), present(r.KalshiSeriesCursors[series.Ticker]))
		}
	} else {
		fmt.Fprintf(&b, "\nNext Polymarket offset: %d\nNext Kalshi cursor: %s\n", r.NextPolymarketOffset, present(r.NextKalshiCursor))
	}
	_, err := io.WriteString(out, b.String())
	return err
}

// Wrap both contract texts side by side, without truncating settlement rules.
func compareRow(out io.Writer, label, left, right string) {
	a, b := wrap(left, 54), wrap(right, 54)
	n := len(a)
	if len(b) > n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		l, r := "", ""
		if i < len(a) {
			l = a[i]
		}
		if i < len(b) {
			r = b[i]
		}
		heading := ""
		if i == 0 {
			heading = label
		}
		fmt.Fprintf(out, "%-18s | %-54s | %s\n", heading, l, r)
	}
}
func wrap(s string, width int) []string {
	chars := []rune(clean(s))
	if len(chars) == 0 {
		return []string{""}
	}
	lines := []string{}
	for len(chars) > width {
		cut := width
		for i := width; i > 0; i-- {
			if chars[i] == ' ' {
				cut = i
				break
			}
		}
		lines = append(lines, string(chars[:cut]))
		chars = chars[cut:]
		for len(chars) > 0 && chars[0] == ' ' {
			chars = chars[1:]
		}
	}
	if len(chars) > 0 {
		lines = append(lines, string(chars))
	}
	return lines
}
