package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"unicode"

	"github.com/Vaibhav2430/Prediction-Market-Arbitrage/internal/kalshi"
	"github.com/Vaibhav2430/Prediction-Market-Arbitrage/internal/models"
)

type kalshiReader interface {
	FetchOpenMarkets(context.Context, int, string, string) (kalshi.MarketPage, error)
	FetchMarket(context.Context, string) (kalshi.Market, error)
	FetchOrderBook(context.Context, string, int) (models.OrderBook, error)
}

type kalshiOptions struct {
	limit, depth           int
	cursor, series, ticker string
}

func scanKalshi(ctx context.Context, client kalshiReader, options kalshiOptions, out, errOut io.Writer) error {
	var page kalshi.MarketPage
	if options.ticker != "" {
		market, err := client.FetchMarket(ctx, options.ticker)
		if err != nil {
			return err
		}
		page.Markets = []kalshi.Market{market}
	} else {
		var err error
		page, err = client.FetchOpenMarkets(ctx, options.limit, options.cursor, options.series)
		if err != nil {
			return err
		}
	}
	fmt.Fprintln(out, "Kalshi — public order-book snapshot (USD per $1 binary contract)")
	fmt.Fprintln(out, "Asks are derived from opposite-side bids: $1 - bid, with the same quantity.")
	fmt.Fprintln(out, "Displayed liquidity can change; fills are not guaranteed. Fees are excluded. No arbitrage signal or trading.")
	for _, warning := range page.Warnings {
		fmt.Fprintln(errOut, "warning: skipped", terminalText(warning.Error()))
	}
	shown, failed := 0, 0
	for _, market := range page.Markets {
		if err := ctx.Err(); err != nil {
			return err
		}
		book, err := client.FetchOrderBook(ctx, market.Ticker, options.depth)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			fmt.Fprintf(errOut, "warning: order book %s unavailable: %s\n", terminalText(market.Ticker), terminalText(err.Error()))
			failed++
			continue
		}
		fmt.Fprintf(out, "\n%s [ticker %s]\n", terminalText(market.Title), terminalText(market.Ticker))
		fmt.Fprintf(out, "  YES: %s | NO: %s\n", terminalText(market.YesSubtitle), terminalText(market.NoSubtitle))
		fmt.Fprintf(out, "  Closes: %s | received locally: %s\n", market.CloseTime.UTC().Format("2006-01-02T15:04:05Z"), book.ReceivedAt.Format("2006-01-02T15:04:05.000Z"))
		top := book.Top()
		fmt.Fprintf(out, "  YES bid: %s | YES ask (derived): %s\n", levelText(top.YesBid), levelText(top.YesAsk))
		fmt.Fprintf(out, "  NO bid: %s | NO ask (derived): %s\n", levelText(top.NoBid), levelText(top.NoAsk))
		if len(book.YesBids) == 0 && len(book.NoBids) == 0 {
			fmt.Fprintln(out, "  Empty order book; no displayed liquidity.")
		}
		shown++
	}
	fmt.Fprintf(out, "\n%d markets displayed; %d invalid records skipped; %d order-book requests failed.\n", shown, len(page.Warnings), failed)
	if page.Cursor != "" {
		fmt.Fprintf(out, "Next cursor: %s\n", terminalText(page.Cursor))
	}
	if len(page.Markets) == 0 {
		fmt.Fprintln(out, "No active $1 binary markets on this page.")
	}
	if failed > 0 {
		return fmt.Errorf("%d Kalshi order-book requests failed; output is incomplete", failed)
	}
	return nil
}

func levelText(level *models.PriceLevel) string {
	if level == nil {
		return "unavailable"
	}
	return fmt.Sprintf("%s x %s contracts", level.Price, level.Quantity)
}

func terminalText(value string) string {
	value = strings.Map(func(ch rune) rune {
		if unicode.IsControl(ch) {
			return ' '
		}
		return ch
	}, value)
	return strings.Join(strings.Fields(value), " ")
}
