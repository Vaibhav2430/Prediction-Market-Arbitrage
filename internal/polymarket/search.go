package polymarket

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// SearchPage contains all valid open markets nested in one search event page.
// Limit counts events, not markets; HasMore follows the API pagination object.
type SearchPage struct {
	Result
	Events  int
	HasMore bool
}

func (c *Client) SearchActiveMarkets(ctx context.Context, topic string, limit, page int) (SearchPage, error) {
	var result SearchPage
	topic = strings.TrimSpace(topic)
	if topic == "" || len(topic) > 256 || limit < 1 || limit > 20 || page < 1 || page > 100 {
		return result, fmt.Errorf("topic must be 1..256 bytes, event limit 1..20, page 1..100")
	}
	q := url.Values{"q": {topic}, "events_status": {"active"}, "limit_per_type": {strconv.Itoa(limit)}, "page": {strconv.Itoa(page)}, "search_tags": {"false"}, "search_profiles": {"false"}, "keep_closed_markets": {"0"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/public-search?"+q.Encode(), nil)
	if err != nil {
		return result, fmt.Errorf("Gamma search request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return result, fmt.Errorf("Gamma search: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return result, fmt.Errorf("Gamma search HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return result, fmt.Errorf("read Gamma search: %w", err)
	}
	if len(body) > maxResponseBytes {
		return result, fmt.Errorf("Gamma search response exceeds %d bytes; reduce -topic-events", maxResponseBytes)
	}
	if !bytes.HasPrefix(bytes.TrimSpace(body), []byte("{")) {
		return result, fmt.Errorf("Gamma search must return an object")
	}
	var wire struct {
		Events     json.RawMessage `json:"events"`
		Pagination *struct {
			HasMore *bool `json:"hasMore"`
		} `json:"pagination"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		return result, fmt.Errorf("decode Gamma search: %w", err)
	}
	if len(wire.Events) == 0 || wire.Pagination == nil || wire.Pagination.HasMore == nil {
		return result, fmt.Errorf("Gamma search missing events or pagination.hasMore")
	}
	var events []json.RawMessage
	if err := json.Unmarshal(wire.Events, &events); err != nil {
		return result, fmt.Errorf("Gamma search events: %w", err)
	}
	result.Events, result.HasMore = len(events), *wire.Pagination.HasMore
	seen := map[string]bool{}
	for i, raw := range events {
		var event struct {
			Title   string            `json:"title"`
			Markets []json.RawMessage `json:"markets"`
		}
		if err := json.Unmarshal(raw, &event); err != nil {
			result.Warnings = append(result.Warnings, fmt.Errorf("search event %d: %w", i, err))
			continue
		}
		if event.Markets == nil {
			result.Warnings = append(result.Warnings, fmt.Errorf("search event %d has no nested markets", i))
			continue
		}
		for j, record := range event.Markets {
			market, include, err := parseMarket(record)
			if err != nil {
				result.Warnings = append(result.Warnings, fmt.Errorf("search event %d market %d: %w", i, j, err))
				continue
			}
			// Even active events can include closed markets. Validate each record.
			if !include || seen[market.ID] {
				continue
			}
			seen[market.ID] = true
			if strings.TrimSpace(event.Title) != "" {
				market.EventTitles = append(market.EventTitles, event.Title)
			}
			result.Markets = append(result.Markets, market)
		}
	}
	return result, nil
}
