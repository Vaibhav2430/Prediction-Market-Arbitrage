// Reviewed captures rule snapshots and refreshes a local manual-review registry.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Vaibhav2430/Prediction-Market-Arbitrage/internal/kalshi"
	"github.com/Vaibhav2430/Prediction-Market-Arbitrage/internal/polymarket"
	"github.com/Vaibhav2430/Prediction-Market-Arbitrage/internal/registry"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "reviewed:", err)
		os.Exit(1)
	}
}
func run(ctx context.Context, args []string, out, errOut io.Writer) error {
	flags := flag.NewFlagSet("reviewed", flag.ContinueOnError)
	flags.SetOutput(errOut)
	path := flags.String("registry", "data/reviewed-pairs.json", "manual pair registry")
	capture := flags.Bool("capture", false, "emit one needs_review entry; never writes registry or approves")
	id := flags.String("pair-id", "", "entry ID (capture) or existing ID to refresh")
	poly := flags.String("polymarket-id", "", "Gamma ID for capture")
	ticker := flags.String("kalshi-ticker", "", "Kalshi ticker for capture")
	relation := flags.String("relation", "", "same/inverted YES mapping for capture")
	quantity := flags.Int64("quantity", 5, "whole matched units for approved pairs")
	maxAge := flags.Duration("max-age", time.Minute, "maximum Polymarket API snapshot age")
	timeout := flags.Duration("timeout", 30*time.Second, "overall timeout")
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 || *quantity < 1 || *quantity > 1000000 || *maxAge <= 0 || *timeout <= 0 {
		return fmt.Errorf("invalid quantity/timeouts/arguments")
	}
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	gamma, kal, terms := polymarket.NewClient(), kalshi.NewClient(), registry.NewTermsClient()
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	if *capture {
		if *id == "" || *poly == "" || *ticker == "" || (*relation != "same" && *relation != "inverted") {
			return fmt.Errorf("capture requires pair-id, market IDs and same/inverted relation")
		}
		snapshot, _, _, err := registry.Capture(ctx, gamma, kal, terms, *poly, *ticker)
		if err != nil {
			return err
		}
		entry := registry.Entry{ID: *id, Status: "needs_review", Relation: *relation, Snapshot: snapshot, Fingerprint: registry.Fingerprint(snapshot, *relation), Review: registry.Review{Findings: []string{"Complete outcome, timing, source and exception review before approval."}}}
		return encoder.Encode(entry)
	}
	if *poly != "" || *ticker != "" || *relation != "" {
		return fmt.Errorf("market IDs/relation require -capture")
	}
	file, err := registry.Load(*path)
	if err != nil {
		return err
	}
	results := []registry.RefreshResult{}
	failed := false
	found := *id == ""
	for _, entry := range file.Entries {
		if *id != "" && entry.ID != *id {
			continue
		}
		found = true
		result, err := registry.Refresh(ctx, entry, gamma, kal, polymarket.NewBookClient(), terms, *quantity, *maxAge)
		if err != nil {
			failed = true
			result.Status = "unverifiable"
			result.Message = err.Error()
		}
		results = append(results, result)
	}
	if !found {
		return fmt.Errorf("pair ID not found")
	}
	if err := encoder.Encode(struct {
		Mode    string                   `json:"mode"`
		Results []registry.RefreshResult `json:"results"`
	}{"read_only_review_registry", results}); err != nil {
		return err
	}
	if failed {
		return fmt.Errorf("one or more registry entries could not be refreshed")
	}
	return nil
}
