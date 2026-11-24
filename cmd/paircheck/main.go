// Paircheck compares explicitly selected contracts without authenticating or trading.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"

	"github.com/Vaibhav2430/Prediction-Market-Arbitrage/internal/comparison"
	"github.com/Vaibhav2430/Prediction-Market-Arbitrage/internal/kalshi"
	"github.com/Vaibhav2430/Prediction-Market-Arbitrage/internal/matching"
	"github.com/Vaibhav2430/Prediction-Market-Arbitrage/internal/models"
	"github.com/Vaibhav2430/Prediction-Market-Arbitrage/internal/polymarket"
)

type gammaReader interface {
	FetchMarket(context.Context, string) (models.Market, error)
}
type kalReader interface {
	FetchMarket(context.Context, string) (kalshi.Market, error)
	FetchFeeInfo(context.Context, string) (kalshi.FeeInfo, error)
	FetchOrderBook(context.Context, string, int) (models.OrderBook, error)
}
type bookReader interface {
	FetchOrderBook(context.Context, string) (models.TokenBook, error)
}
type options struct {
	polyID, ticker, relation, format string
	quantity                         int64
	timeout, maxAge                  time.Duration
}
type report struct {
	Status          string                `json:"status"`
	OutcomeRelation string                `json:"assumed_outcome_relation"`
	ReviewFindings  []string              `json:"review_findings"`
	Warnings        []string              `json:"warnings"`
	Polymarket      matching.Contract     `json:"polymarket"`
	Kalshi          matching.Contract     `json:"kalshi"`
	PolyFeesEnabled *bool                 `json:"polymarket_fees_enabled"`
	PolyFeeRate     json.RawMessage       `json:"polymarket_fee_rate"`
	PolyFeeExponent *int                  `json:"polymarket_fee_exponent"`
	KalshiFees      kalshi.FeeInfo        `json:"kalshi_fees"`
	SnapshotStart   time.Time             `json:"snapshot_fetch_started"`
	SnapshotEnd     time.Time             `json:"snapshot_fetch_completed"`
	PolyYes         models.TokenBook      `json:"polymarket_yes_book"`
	PolyNo          models.TokenBook      `json:"polymarket_no_book"`
	KalshiBook      models.OrderBook      `json:"kalshi_book"`
	Scenarios       []comparison.Scenario `json:"conditional_scenarios"`
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "paircheck:", err)
		os.Exit(1)
	}
}
func run(ctx context.Context, args []string, out, errOut io.Writer) error {
	var o options
	flags := flag.NewFlagSet("paircheck", flag.ContinueOnError)
	flags.SetOutput(errOut)
	flags.StringVar(&o.polyID, "polymarket-id", "", "Gamma market ID")
	flags.StringVar(&o.ticker, "kalshi-ticker", "", "Kalshi market ticker")
	flags.StringVar(&o.relation, "relation", "", "assumed YES predicate relation: same or inverted (not verification)")
	flags.Int64Var(&o.quantity, "quantity", 5, "whole matched units to cost (1..1000000)")
	flags.StringVar(&o.format, "format", "text", "text or json")
	flags.DurationVar(&o.timeout, "timeout", 30*time.Second, "overall deadline")
	flags.DurationVar(&o.maxAge, "max-age", 60*time.Second, "maximum allowed Polymarket API snapshot age")
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 || o.polyID == "" || o.ticker == "" || (o.relation != "same" && o.relation != "inverted") || o.quantity < 1 || o.quantity > 1000000 || o.timeout <= 0 || o.maxAge <= 0 || (o.format != "text" && o.format != "json") {
		return fmt.Errorf("supply market IDs, explicit relation, valid quantity/timeouts and format; see -help")
	}
	ctx, cancel := context.WithTimeout(ctx, o.timeout)
	defer cancel()
	return execute(ctx, polymarket.NewClient(), kalshi.NewClient(), polymarket.NewBookClient(), o, out)
}
func execute(ctx context.Context, gamma gammaReader, kal kalReader, clob bookReader, o options, out io.Writer) error {
	p, err := gamma.FetchMarket(ctx, o.polyID)
	if err != nil {
		return err
	}
	k, err := kal.FetchMarket(ctx, o.ticker)
	if err != nil {
		return err
	}
	y, n, binary := p.YesNoOutcomes()
	if !binary || y.TokenID == "" || n.TokenID == "" || y.TokenID == n.TokenID || p.ConditionID == "" {
		return fmt.Errorf("requires a binary Polymarket market with condition ID and distinct YES/NO asset IDs")
	}
	pc, _ := matching.FromPolymarket(p)
	kc := matching.FromKalshi(k)
	result := report{Status: "needs_review", OutcomeRelation: o.relation, Polymarket: pc, Kalshi: kc, Warnings: []string{
		"No contract equivalence or guaranteed payout has been verified. The supplied relation is an assumption.",
		"Quotes are separate snapshots, not an atomic or executable cross-platform package; fills are not guaranteed.",
		"Fee estimates exclude rebates, funding, withdrawal and currency-conversion costs; fill-level rounding may differ.",
		"Nominal dollar comparisons assume collateral-to-USD parity; conversion and depeg risk are not modeled.",
		"Kalshi supplies no exchange timestamp in this REST book: local receipt cannot establish exchange freshness.",
	}, Scenarios: []comparison.Scenario{}, PolyFeesEnabled: p.FeesEnabled, PolyFeeRate: p.FeeRate, PolyFeeExponent: p.FeeExponent}
	result.ReviewFindings = review(pc, kc)
	if len(result.ReviewFindings) > 0 {
		result.Status = "incompatible_exception_rules"
	}
	fees, feeErr := kal.FetchFeeInfo(ctx, k.EventTicker)
	if feeErr != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		result.Warnings = append(result.Warnings, "Kalshi fee metadata unavailable: "+feeErr.Error())
	}
	result.KalshiFees = fees
	feeModel := comparison.ResolveFees(p, fees)
	result.Warnings = append(result.Warnings, feeModel.Warnings...)
	result.SnapshotStart = time.Now().UTC()
	var py, pn models.TokenBook
	var kb models.OrderBook
	var ey, en, ek error
	var wg sync.WaitGroup
	wg.Add(3)
	go func() { defer wg.Done(); py, ey = clob.FetchOrderBook(ctx, y.TokenID) }()
	go func() { defer wg.Done(); pn, en = clob.FetchOrderBook(ctx, n.TokenID) }()
	go func() { defer wg.Done(); kb, ek = kal.FetchOrderBook(ctx, k.Ticker, 1) }()
	wg.Wait()
	result.SnapshotEnd = time.Now().UTC()
	for _, e := range []error{ey, en, ek} {
		if e != nil {
			return e
		}
	}
	if py.AssetID != y.TokenID || pn.AssetID != n.TokenID || py.ConditionID != p.ConditionID || pn.ConditionID != p.ConditionID || kb.Ticker != k.Ticker {
		return fmt.Errorf("book identity does not match selected contract")
	}
	result.PolyYes, result.PolyNo, result.KalshiBook = py, pn, kb
	stale := false
	for _, book := range []models.TokenBook{py, pn} {
		age := result.SnapshotEnd.Sub(book.Timestamp)
		if book.Timestamp.IsZero() || age > o.maxAge || age < -5*time.Second {
			stale = true
			result.Warnings = append(result.Warnings, "Polymarket snapshot is stale or has an invalid/future timestamp; conditional differences are withheld.")
		}
	}
	top := kb.Top()
	yesK, noK := top.NoAsk, top.YesAsk
	yesName, noName := "NO", "YES"
	if o.relation == "inverted" {
		yesK, noK = top.YesAsk, top.NoAsk
		yesName, noName = "YES", "NO"
	}
	for _, leg := range []struct {
		name, kalSide string
		book          models.TokenBook
		ask           *models.PriceLevel
	}{{"YES", yesName, py, yesK}, {"NO", noName, pn, noK}} {
		scenario, err := comparison.Price(leg.name, leg.kalSide, leg.book.BestAsk(), leg.ask, leg.book.MinOrderSize, o.quantity, feeModel)
		if err != nil {
			return err
		}
		if stale {
			scenario.Status = "stale_snapshot"
			scenario.ConditionalDifference = ""
			scenario.Reason = "Stale/invalid Polymarket snapshot; no net comparison presented"
		}
		result.Scenarios = append(result.Scenarios, scenario)
	}
	if o.format == "json" {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}
	return printReport(out, result)
}

// The known CPI delay-policy conflict is detected without declaring any pair
// verified. Generic review still requires complete original rules and terms.
func review(p, k matching.Contract) []string {
	normalize := func(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }
	pr, kr := normalize(p.RulesPrimary+" "+p.RulesSecondary), normalize(k.RulesPrimary+" "+k.RulesSecondary)
	findings := []string{}
	if strings.Contains(pr, "previous month") && strings.Contains(kr, "government shutdown") && strings.Contains(kr, "six months") {
		findings = append(findings, "Delayed-release policy differs: Polymarket can resolve using a previous month's figures while Kalshi can extend settlement during a shutdown. These contracts are not verified equivalents.")
	}
	if strings.Contains(pr, "next scheduled meeting") && strings.Contains(kr, "meeting is canceled") {
		findings = append(findings, "Canceled/delayed meeting policies differ; a common ordinary outcome does not establish a guaranteed hedge.")
	}
	return findings
}
func clean(s string) string {
	return strings.Join(strings.Fields(strings.Map(func(ch rune) rune {
		if unicode.IsControl(ch) {
			return ' '
		}
		return ch
	}, s)), " ")
}
func printReport(out io.Writer, r report) error {
	var b strings.Builder
	fmt.Fprintln(&b, "Read-only pair check —", r.Status)
	fmt.Fprintf(&b, "Polymarket %s: %s\nKalshi %s: %s\n", clean(r.Polymarket.ID), clean(r.Polymarket.Title), clean(r.Kalshi.ID), clean(r.Kalshi.Title))
	fmt.Fprintln(&b, "Assumed YES relation:", r.OutcomeRelation, "(not verified)")
	for _, f := range r.ReviewFindings {
		fmt.Fprintln(&b, "Rule conflict:", clean(f))
	}
	fmt.Fprintf(&b, "Book fetch window: %s to %s\n", r.SnapshotStart.Format(time.RFC3339Nano), r.SnapshotEnd.Format(time.RFC3339Nano))
	for _, s := range r.Scenarios {
		fmt.Fprintf(&b, "\nBuy Polymarket %s + Kalshi %s — %s\n", s.PolymarketSide, s.KalshiSide, s.Status)
		fmt.Fprintf(&b, "  Asks: %s + %s | requested %d; top-of-book capacity %d whole units\n", s.PolyAsk, s.KalshiAsk, s.Requested, s.Available)
		if s.Premium != "" {
			fmt.Fprintf(&b, "  Premium: $%s\n", s.Premium)
		}
		if s.Total != "" {
			fmt.Fprintf(&b, "  Fee estimates: Polymarket $%s, Kalshi $%s | total $%s\n", s.PolyFee, s.KalshiFee, s.Total)
		} else {
			fmt.Fprintln(&b, "  Fee-adjusted total: unavailable")
		}

		if s.ConditionalDifference != "" {
			fmt.Fprintf(&b, "  Conditional payout $%s minus estimated cost: $%s (NOT guaranteed profit)\n", s.ConditionalPayout, s.ConditionalDifference)
		}
		fmt.Fprintln(&b, " ", s.Reason)
	}
	for _, w := range r.Warnings {
		fmt.Fprintln(&b, "Warning:", clean(w))
	}
	fmt.Fprintln(&b, "No verified arbitrage conclusion. Use -format json for full rules, books, timestamps and fee parameters.")
	_, err := io.WriteString(out, b.String())
	return err
}
