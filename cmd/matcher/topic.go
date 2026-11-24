package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Vaibhav2430/Prediction-Market-Arbitrage/internal/kalshi"
	"github.com/Vaibhav2430/Prediction-Market-Arbitrage/internal/matching"
	"github.com/Vaibhav2430/Prediction-Market-Arbitrage/internal/polymarket"
)

type topicGamma interface {
	SearchActiveMarkets(context.Context, string, int, int) (polymarket.SearchPage, error)
}
type topicKalshi interface {
	FindSeries(context.Context, string, string) (kalshi.SeriesResult, error)
	FetchOpenMarkets(context.Context, int, string, string) (kalshi.MarketPage, error)
}

const maxTopicMarkets = 1000

func topicCategory(topic string) string {
	// Convenience defaults only; callers can specify the API's case-sensitive
	// category or an exact series for every other topic.
	switch strings.ToLower(strings.TrimSpace(topic)) {
	case "bitcoin", "ethereum", "crypto", "dogecoin", "solana":
		return "Crypto"
	case "inflation", "unemployment", "gdp", "interest rates":
		return "Economics"
	case "nba", "nfl", "nhl", "mlb", "basketball", "football", "soccer":
		return "Sports"
	}
	return ""
}

func executeTopic(ctx context.Context, gamma topicGamma, kal topicKalshi, o options, out, errOut io.Writer) error {
	result := report{Status: "needs_review", Note: "Topic-focused bounded discovery only. Scores measure shared wording, not contract equivalence. All pairs require manual review; no pricing or trading.", Warnings: []string{}, Candidates: []matching.Candidate{}, Topic: o.topic, KalshiCategory: o.category, WindowAfter: o.after, WindowBefore: o.before}
	selected := []kalshi.Series{}
	if o.series != "" {
		selected = append(selected, kalshi.Series{Ticker: o.series, Title: "Explicit series " + o.series})
	} else {
		series, err := kal.FindSeries(ctx, o.topic, o.category)
		if err != nil {
			return fmt.Errorf("Kalshi topic-series discovery: %w", err)
		}
		for _, warning := range series.Warnings {
			result.Warnings = append(result.Warnings, "Kalshi: "+warning.Error())
		}
		selected = series.Series
		result.KalshiSeriesMatched = len(selected)
		if len(selected) > o.maxSeries {
			selected = selected[:o.maxSeries]
			result.Warnings = append(result.Warnings, "Kalshi series selection capped by -max-series; related series may be omitted. Prefer -kalshi-series for a specific event family.")
		}
	}
	if o.series != "" {
		result.KalshiSeriesMatched = 1
	}
	result.KalshiSeries = selected
	poly, ks := []matching.Contract{}, []matching.Contract{}
	seenP, seenK := map[string]bool{}, map[string]bool{}
	polyMore := true
	for page := o.searchPage; page < o.searchPage+o.pages; page++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		data, err := gamma.SearchActiveMarkets(ctx, o.topic, o.topicEvents, page)
		if err != nil {
			return fmt.Errorf("Polymarket topic search page %d: %w", page, err)
		}
		result.PolymarketPages++
		result.NextSearchPage = page + 1
		for _, warning := range data.Warnings {
			result.Warnings = append(result.Warnings, "Polymarket: "+warning.Error())
		}
		for _, m := range data.Markets {
			if seenP[m.ID] {
				continue
			}
			seenP[m.ID] = true
			c, ok := matching.FromPolymarket(m)
			if !ok {
				continue
			}
			if !withinWindow(c.Deadline, o.after, o.before) {
				result.WindowOmittedPolymarket++
				continue
			}
			if len(poly) >= maxTopicMarkets {
				result.PolymarketCapped = true
				break
			}
			poly = append(poly, c)
		}
		polyMore = data.HasMore
		if !polyMore || result.PolymarketCapped {
			break
		}
	}
	result.PolymarketSearchHasMore = polyMore
	// Cursors belong to their series and must not be reused across series.
	result.KalshiSeriesCursors = map[string]string{}
	for _, series := range selected {
		cursor := ""
		if o.series != "" {
			cursor = o.cursor
		}
		seenCursor := map[string]bool{}
		if cursor != "" {
			seenCursor[cursor] = true
		}
		for page := 0; page < o.pages; page++ {
			if err := ctx.Err(); err != nil {
				return err
			}
			data, err := kal.FetchOpenMarkets(ctx, o.size, cursor, series.Ticker)
			if err != nil {
				return fmt.Errorf("Kalshi series %s page %d: %w", series.Ticker, page+1, err)
			}
			result.KalshiPages++
			for _, warning := range data.Warnings {
				result.Warnings = append(result.Warnings, "Kalshi "+series.Ticker+": "+warning.Error())
			}
			for _, m := range data.Markets {
				if seenK[m.Ticker] {
					continue
				}
				seenK[m.Ticker] = true
				c := matching.FromKalshi(m)
				c.Event = series.Title + "; " + c.Event
				if !withinWindow(c.Deadline, o.after, o.before) {
					result.WindowOmittedKalshi++
					continue
				}
				if len(ks) >= maxTopicMarkets {
					result.KalshiCapped = true
					break
				}
				ks = append(ks, c)
			}
			cursor = data.Cursor
			result.KalshiSeriesCursors[series.Ticker] = cursor
			if cursor == "" || result.KalshiCapped {
				break
			}
			if seenCursor[cursor] {
				result.Warnings = append(result.Warnings, "Kalshi "+series.Ticker+" cursor repeated; pagination stopped.")
				break
			}
			seenCursor[cursor] = true
		}
		if result.KalshiCapped {
			break
		}
	}
	if len(selected) == 0 {
		result.Warnings = append(result.Warnings, "No related Kalshi series found in this category. Try another topic/category or specify -kalshi-series; the search did not fall back to unrelated markets.")
	}
	if result.PolymarketCapped || result.KalshiCapped {
		result.Warnings = append(result.Warnings, "Topic discovery reached the 1000-market per-venue cap. Narrow the topic or deadline window; continuation can skip the remaining nested records on the capped page.")
	}
	result.PolymarketMarkets, result.KalshiMarkets = len(poly), len(ks)
	return finishReport(ctx, result, poly, ks, o, out, errOut)
}

// These are filters on reported platform deadlines, not event-time equality.
// Missing or imprecise timestamps are excluded only when a window is active.
func withinWindow(deadline, after, before string) bool {
	if after == "" && before == "" {
		return true
	}
	d, err := time.Parse(time.RFC3339Nano, deadline)
	if err != nil {
		return false
	}
	if after != "" {
		a, _ := time.Parse(time.RFC3339Nano, after)
		if d.Before(a) {
			return false
		}
	}
	if before != "" {
		b, _ := time.Parse(time.RFC3339Nano, before)
		if d.After(b) {
			return false
		}
	}
	return true
}
