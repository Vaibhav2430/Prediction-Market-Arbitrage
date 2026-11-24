// Scanner prints read-only market data from Polymarket or Kalshi.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Vaibhav2430/Prediction-Market-Arbitrage/internal/kalshi"
	"github.com/Vaibhav2430/Prediction-Market-Arbitrage/internal/models"
	"github.com/Vaibhav2430/Prediction-Market-Arbitrage/internal/polymarket"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "scanner:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, out, errOut io.Writer) error {
	flags := flag.NewFlagSet("scanner", flag.ContinueOnError)
	flags.SetOutput(errOut)
	limit := flags.Int("limit", 20, "API records per page (Polymarket 1..100; Kalshi 1..1000)")
	indicative := flags.Bool("indicative", false, "Polymarket: show Gamma indicative prices instead of CLOB order books")
	venue := flags.String("venue", "polymarket", "data source: polymarket or kalshi")
	cursor := flags.String("cursor", "", "Kalshi next-page cursor")
	series := flags.String("series", "", "Kalshi series ticker filter")
	ticker := flags.String("ticker", "", "Kalshi single market ticker (instead of discovery)")
	depth := flags.Int("depth", 5, "Kalshi order-book levels per side (0=all; 1..100)")
	offset := flags.Int("offset", 0, "API record offset (one page per run)")
	timeout := flags.Duration("timeout", 30*time.Second, "overall timeout (each HTTP request also has a 10s timeout)")
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments")
	}
	if *timeout <= 0 {
		return fmt.Errorf("timeout must be positive")
	}
	if *venue != "polymarket" && *venue != "kalshi" {
		return fmt.Errorf("venue must be polymarket or kalshi")
	}
	if *venue == "polymarket" && (*cursor != "" || *series != "" || *ticker != "") {
		return fmt.Errorf("cursor, series, and ticker require -venue kalshi")
	}
	if *venue == "kalshi" {
		if *indicative {
			return fmt.Errorf("indicative requires -venue polymarket")
		}
		if *offset != 0 {
			return fmt.Errorf("Kalshi uses -cursor rather than -offset")
		}
		if *limit < 1 || *limit > 1000 || *depth < 0 || *depth > 100 {
			return fmt.Errorf("Kalshi limit must be 1..1000 and depth must be 0..100")
		}
		if *ticker != "" && (*cursor != "" || *series != "") {
			return fmt.Errorf("ticker cannot be combined with cursor or series")
		}
	}
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	if *venue == "kalshi" {
		return scanKalshi(ctx, kalshi.NewClient(), kalshiOptions{limit: *limit, depth: *depth, cursor: *cursor, series: *series, ticker: *ticker}, out, errOut)
	}
	if !*indicative {
		return scanPolymarket(ctx, polymarket.NewClient(), polymarket.NewBookClient(), *limit, *offset, out, errOut)
	}
	result, err := polymarket.NewClient().FetchActiveMarkets(ctx, *limit, *offset)
	if err != nil {
		return err
	}
	for _, warning := range result.Warnings {
		fmt.Fprintln(errOut, "warning: skipped", warning)
	}
	printMarkets(out, result.Markets, *limit, *offset, len(result.Warnings))
	return nil
}

func priceText(price *float64) string {
	if price == nil {
		return "unavailable"
	}
	return fmt.Sprintf("$%.6f", *price)
}

func printMarkets(out io.Writer, markets []models.Market, limit, offset, invalid int) {
	fmt.Fprintln(out, "Polymarket Gamma — indicative YES/NO prices (USD per share)")
	fmt.Fprintln(out, "These are Gamma outcomePrices, NOT executable bid/ask quotes or arbitrage signals.")
	fmt.Fprintln(out, "No order-book depth, fees, slippage, or freshness guarantee is included.")
	shown, nonbinary := 0, 0
	for _, market := range markets {
		yes, no, binary := market.YesNo()
		if !binary {
			nonbinary++
			continue
		}
		// Collapse whitespace/control characters in public text for terminal output.
		question := strings.Join(strings.Fields(market.Question), " ")
		fmt.Fprintf(out, "\n%s [ID %s]\n  YES indicative: %s | NO indicative: %s\n", question, strings.Join(strings.Fields(market.ID), " "), priceText(yes), priceText(no))
		shown++
	}
	fmt.Fprintf(out, "\nPage limit=%d offset=%d: %d YES/NO markets displayed; %d non-YES/NO markets omitted; %d invalid records skipped.\n", limit, offset, shown, nonbinary, invalid)
	if shown == 0 {
		fmt.Fprintln(out, "No YES/NO markets to display on this page.")
	}
}
