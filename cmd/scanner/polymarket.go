package main

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/Vaibhav2430/Prediction-Market-Arbitrage/internal/models"
	"github.com/Vaibhav2430/Prediction-Market-Arbitrage/internal/polymarket"
)

type gammaReader interface {
	FetchActiveMarkets(context.Context, int, int) (polymarket.Result, error)
}
type polyBookReader interface {
	FetchOrderBook(context.Context, string) (models.TokenBook, error)
}

func scanPolymarket(ctx context.Context, gamma gammaReader, clob polyBookReader, limit, offset int, out, errOut io.Writer) error {
	result, err := gamma.FetchActiveMarkets(ctx, limit, offset)
	if err != nil {
		return err
	}
	fmt.Fprintln(out, "Polymarket CLOB — public YES/NO order-book snapshots (USD per share)")
	fmt.Fprintln(out, "Bids and asks come directly from each outcome's book. Gamma indicative prices are not used as quotes.")
	fmt.Fprintln(out, "Fees are excluded; fills and freshness are not guaranteed. YES/NO snapshots are fetched separately. No arbitrage signal or trading.")
	for _, warning := range result.Warnings {
		fmt.Fprintln(errOut, "warning: skipped", terminalText(warning.Error()))
	}
	shown, omitted, failed := 0, 0, 0
	for _, market := range result.Markets {
		yes, no, binary := market.YesNoOutcomes()
		if !binary {
			omitted++
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if market.ConditionID == "" || yes.TokenID == "" || no.TokenID == "" || yes.TokenID == no.TokenID {
			fmt.Fprintf(errOut, "warning: market %s missing condition ID or distinct YES/NO asset IDs\n", terminalText(market.ID))
			failed++
			continue
		}
		fmt.Fprintf(out, "\n%s [ID %s]\n", terminalText(market.Question), terminalText(market.ID))
		complete := true
		for _, side := range []struct {
			name    string
			outcome models.Outcome
		}{{"YES", yes}, {"NO", no}} {
			book, err := clob.FetchOrderBook(ctx, side.outcome.TokenID)
			if err == nil && (book.AssetID != side.outcome.TokenID || book.ConditionID != market.ConditionID) {
				err = fmt.Errorf("order book does not match Gamma asset/condition ID")
			}
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				fmt.Fprintf(errOut, "warning: market %s %s book unavailable: %s\n", terminalText(market.ID), side.name, terminalText(err.Error()))
				fmt.Fprintf(out, "  %s order book: unavailable (request or validation failed)\n", side.name)
				complete = false
				continue
			}
			fmt.Fprintf(out, "  %s bid: %s | %s ask: %s\n", side.name, tokenLevelText(book.BestBid()), side.name, tokenLevelText(book.BestAsk()))
			age := book.ReceivedAt.Sub(book.Timestamp).Round(time.Millisecond)
			fmt.Fprintf(out, "    Snapshot: %s | received locally: %s | snapshot age at receipt: %s\n", book.Timestamp.Format(time.RFC3339Nano), book.ReceivedAt.Format(time.RFC3339Nano), age)
			fmt.Fprintf(out, "    Tick: %s | minimum order: %s shares\n", book.TickSize, book.MinOrderSize)
			if len(book.Bids) == 0 && len(book.Asks) == 0 {
				fmt.Fprintln(out, "    Empty order book; no displayed liquidity.")
			}
		}
		if complete {
			shown++
		} else {
			failed++
		}
	}
	fmt.Fprintf(out, "\nPage limit=%d offset=%d: %d complete YES/NO markets; %d non-YES/NO omitted; %d invalid Gamma records skipped; %d markets with unavailable books.\n", limit, offset, shown, omitted, len(result.Warnings), failed)
	if failed > 0 {
		return fmt.Errorf("%d Polymarket markets have unavailable books; output is incomplete", failed)
	}
	if shown == 0 {
		fmt.Fprintln(out, "No YES/NO order books to display on this page.")
	}
	return nil
}

func tokenLevelText(level *models.TokenLevel) string {
	if level == nil {
		return "unavailable"
	}
	return fmt.Sprintf("%s x %s shares", level.Price, level.Size)
}
