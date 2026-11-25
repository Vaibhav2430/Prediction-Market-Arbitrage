// Settlement validates documented payout scenarios; it never places orders.
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
	"syscall"
	"time"
	"unicode"

	"github.com/Vaibhav2430/Prediction-Market-Arbitrage/internal/kalshi"
	"github.com/Vaibhav2430/Prediction-Market-Arbitrage/internal/polymarket"
	"github.com/Vaibhav2430/Prediction-Market-Arbitrage/internal/registry"
	"github.com/Vaibhav2430/Prediction-Market-Arbitrage/internal/settlement"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "settlement:", err)
		os.Exit(1)
	}
}
func run(ctx context.Context, args []string, out, errOut io.Writer) error {
	f := flag.NewFlagSet("settlement", flag.ContinueOnError)
	f.SetOutput(errOut)
	path := f.String("registry", "data/reviewed-pairs.json", "reviewed pair registry")
	id := f.String("pair-id", "newsom-democratic-nominee-2028", "pair to assess")
	offline := f.Bool("offline", false, "use saved rules only; no current-rule verification")
	format := f.String("format", "text", "text or json")
	timeout := f.Duration("timeout", 30*time.Second, "overall deadline")
	if err := f.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if f.NArg() != 0 || *id == "" || *timeout <= 0 || (*format != "text" && *format != "json") {
		return fmt.Errorf("invalid arguments; see -help")
	}
	file, err := registry.Load(*path)
	if err != nil {
		return err
	}
	var entry *registry.Entry
	for i := range file.Entries {
		if file.Entries[i].ID == *id {
			entry = &file.Entries[i]
			break
		}
	}
	if entry == nil {
		return fmt.Errorf("pair not found")
	}
	if entry.SettlementModel == nil {
		return fmt.Errorf("pair has no documented settlement model")
	}
	result, err := registry.AssessSettlement(*entry)
	if err != nil {
		return err
	}
	if !*offline {
		ctx, cancel := context.WithTimeout(ctx, *timeout)
		defer cancel()
		current, _, _, err := registry.Capture(ctx, polymarket.NewClient(), kalshi.NewClient(), registry.NewTermsClient(), entry.Snapshot.Polymarket.ID, entry.Snapshot.Kalshi.ID)
		if err != nil {
			return fmt.Errorf("could not verify current rules: %w", err)
		}
		result.CurrentRulesFingerprint = registry.Fingerprint(current, entry.Relation)
		result.Mode = "live_rules_check"
		if result.CurrentRulesFingerprint != entry.Fingerprint {
			result.Status = "rules_changed"
			result.Cases = nil
			result.PayoutFloorKnown = false
			result.FirstMinimum, result.SecondMinimum = "", ""
			result.Aligned, result.Conflicts, result.Unresolved = 0, 0, 0
			result.Notes = append(result.Notes, "Saved model is stale; payout table withheld. Recapture and review current rules.")
		}
	}
	if *format == "json" {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}
	return render(out, result)
}
func clean(s string) string {
	return strings.Join(strings.Fields(strings.Map(func(ch rune) rune {
		if unicode.IsControl(ch) {
			return ' '
		}
		return ch
	}, s)), " ")
}
func render(out io.Writer, r settlement.Report) error {
	var b strings.Builder
	fmt.Fprintf(&b, "Settlement scenario validator — %s\nPair: %s | mode: %s\n", r.Status, clean(r.PairID), r.Mode)
	fmt.Fprintf(&b, "%d aligned, %d conflicting, %d unresolved cases; %d missing categories.\n", r.Aligned, r.Conflicts, r.Unresolved, len(r.MissingCategories))
	for _, c := range r.Cases {
		fmt.Fprintf(&b, "\n%s — %s\n  %s\n", clean(c.ID), c.Status, clean(c.Description))
		fmt.Fprintf(&b, "  YES outcome: Polymarket=%s | Kalshi=%s\n", c.PolymarketOutcome, c.KalshiOutcome)
		if c.FirstPayout != "" {
			fmt.Fprintf(&b, "  %s pays $%s | %s pays $%s (nominal, per one unit each)\n", c.FirstPackage, c.FirstPayout, c.SecondPackage, c.SecondPayout)
		} else {
			fmt.Fprintln(&b, "  Payout unknown; void/discretionary outcomes are not treated as binary complements.")
		}
		fmt.Fprintf(&b, "  Evidence: Polymarket [%s] | Kalshi [%s]\n", clean(strings.Join(c.PolymarketEvidence, ", ")), clean(strings.Join(c.KalshiEvidence, ", ")))
		for _, reason := range c.Reasons {
			fmt.Fprintln(&b, "  Reason:", clean(reason))
		}
	}
	if r.PayoutFloorKnown {
		fmt.Fprintf(&b, "\nMinimum across documented cases: $%s / $%s. This is a manually specified model, not proof of a guaranteed hedge.\n", r.FirstMinimum, r.SecondMinimum)
	} else {
		fmt.Fprintln(&b, "\nOverall payout floor: unknown; approval remains blocked.")
	}
	if len(r.MissingCategories) > 0 {
		fmt.Fprintln(&b, "Missing categories:", strings.Join(r.MissingCategories, ", "))
	}
	for _, note := range r.Notes {
		fmt.Fprintln(&b, "Note:", note)
	}
	fmt.Fprintln(&b, "Evidence excerpts and provenance: use -format json or inspect the registry entry.")
	fmt.Fprintln(&b, "No registry status changed. No pricing or trading performed.")
	_, err := io.WriteString(out, b.String())
	return err
}
