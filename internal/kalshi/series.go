package kalshi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
)

type Series struct {
	Ticker   string   `json:"ticker"`
	Title    string   `json:"title"`
	Category string   `json:"category"`
	Tags     []string `json:"tags"`
}
type SeriesResult struct {
	Series   []Series
	Warnings []error
}

// FindSeries searches one category's public series metadata locally. The API
// supports category filtering, not arbitrary text search on this endpoint.
// No unfiltered catalog download or guessed market tickers is performed.
func (c *Client) FindSeries(ctx context.Context, topic, category string) (SeriesResult, error) {
	var result SeriesResult
	topic, category = strings.TrimSpace(topic), strings.TrimSpace(category)
	if topic == "" || len(topic) > 256 || category == "" || len(category) > 128 {
		return result, fmt.Errorf("Kalshi topic and category are required (max 256/128 bytes)")
	}
	var wire struct {
		Series json.RawMessage `json:"series"`
	}
	if err := c.get(ctx, "/series", url.Values{"category": {category}}, &wire); err != nil {
		return result, err
	}
	if !bytes.HasPrefix(bytes.TrimSpace(wire.Series), []byte("[")) {
		return result, fmt.Errorf("Kalshi series response requires series array")
	}
	var records []json.RawMessage
	if err := json.Unmarshal(wire.Series, &records); err != nil {
		return result, fmt.Errorf("decode Kalshi series: %w", err)
	}
	seen := map[string]bool{}
	for i, raw := range records {
		var series Series
		if err := json.Unmarshal(raw, &series); err != nil {
			result.Warnings = append(result.Warnings, fmt.Errorf("series %d: %w", i, err))
			continue
		}
		if err := validateTicker(series.Ticker); err != nil || strings.TrimSpace(series.Title) == "" {
			result.Warnings = append(result.Warnings, fmt.Errorf("series %d missing valid ticker/title", i))
			continue
		}
		// Do not enforce primary Category equality: Kalshi series may have multiple
		// discovery categories and a primary category differing from the filter.
		if !strings.Contains(strings.ToLower(series.Title+" "+strings.Join(series.Tags, " ")), strings.ToLower(topic)) || seen[series.Ticker] {
			continue
		}
		seen[series.Ticker] = true
		result.Series = append(result.Series, series)
	}
	// Favor title evidence over tag-only matches, exact topic tags over compound
	// tags (e.g. Bitcoin Cash), and broad price series over short legacy names.
	// This still does not assert that any series has open contracts.
	priority := func(s Series) int {
		title := strings.ToLower(s.Title)
		needle := strings.ToLower(topic)
		score := 0
		if strings.Contains(title, needle) {
			score += 16
		}
		for _, tag := range s.Tags {
			if strings.EqualFold(strings.TrimSpace(tag), topic) {
				score += 8
				break
			}
		}
		if strings.HasPrefix(s.Ticker, "KX") {
			score += 4
		}
		for _, word := range []string{"price", "range", "above", "below", "high", "low"} {
			if strings.Contains(title, word) {
				score += 2
				break
			}
		}
		if strings.HasPrefix(title, needle) {
			score++
		}
		return score
	}
	sort.Slice(result.Series, func(i, j int) bool {
		a, b := result.Series[i], result.Series[j]
		ap, bp := priority(a), priority(b)
		if ap != bp {
			return ap > bp
		}
		if len(a.Title) != len(b.Title) {
			return len(a.Title) < len(b.Title)
		}
		return a.Ticker < b.Ticker
	})
	return result, nil
}
