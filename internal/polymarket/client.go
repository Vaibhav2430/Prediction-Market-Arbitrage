// Package polymarket reads public market metadata from the Gamma API.
package polymarket

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Vaibhav2430/Prediction-Market-Arbitrage/internal/models"
)

const (
	GammaURL         = "https://gamma-api.polymarket.com"
	maxResponseBytes = 4 << 20
)

type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient requires no credentials and exposes no trading operations.
func NewClient() *Client {
	return &Client{baseURL: GammaURL, httpClient: &http.Client{Timeout: 10 * time.Second}}
}

// Result reports invalid records separately rather than hiding incomplete data.
type Result struct {
	Markets  []models.Market
	Warnings []error
}

// FetchActiveMarkets fetches one bounded page. Offset counts API records,
// including records subsequently filtered or rejected by this client.
func (c *Client) FetchActiveMarkets(ctx context.Context, limit, offset int) (Result, error) {
	var result Result
	if limit < 1 || limit > 100 || offset < 0 {
		return result, fmt.Errorf("limit must be 1..100 and offset must be nonnegative")
	}
	u, err := url.Parse(c.baseURL + "/markets")
	if err != nil {
		return result, fmt.Errorf("Gamma URL: %w", err)
	}
	q := u.Query()
	q.Set("active", "true")
	q.Set("closed", "false")
	q.Set("limit", strconv.Itoa(limit))
	q.Set("offset", strconv.Itoa(offset))
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return result, fmt.Errorf("Gamma request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return result, fmt.Errorf("fetch Gamma markets: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return result, fmt.Errorf("Gamma HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return result, fmt.Errorf("read Gamma response: %w", err)
	}
	if len(body) > maxResponseBytes {
		return result, fmt.Errorf("Gamma response exceeds %d bytes", maxResponseBytes)
	}
	if !bytes.HasPrefix(bytes.TrimSpace(body), []byte("[")) {
		return result, fmt.Errorf("Gamma response must be a market array")
	}
	var records []json.RawMessage
	if err := json.Unmarshal(body, &records); err != nil {
		return result, fmt.Errorf("decode Gamma response: %w", err)
	}
	for i, record := range records {
		market, include, err := parseMarket(record)
		if err != nil {
			result.Warnings = append(result.Warnings, fmt.Errorf("record %d (offset %d): %w", i, offset, err))
			continue
		}
		if include {
			result.Markets = append(result.Markets, market)
		}
	}
	return result, nil
}

type gammaMarket struct {
	ID               string          `json:"id"`
	Question         string          `json:"question"`
	Slug             string          `json:"slug"`
	ConditionID      string          `json:"conditionId"`
	Active           *bool           `json:"active"`
	Closed           *bool           `json:"closed"`
	Outcomes         json.RawMessage `json:"outcomes"`
	Prices           json.RawMessage `json:"outcomePrices"`
	TokenIDs         json.RawMessage `json:"clobTokenIds"`
	PositionIDs      json.RawMessage `json:"positionIds"`
	Version          string          `json:"version"`
	Description      string          `json:"description"`
	EndDate          string          `json:"endDate"`
	ResolutionSource string          `json:"resolutionSource"`
	FeesEnabled      *bool           `json:"feesEnabled"`
	FeeSchedule      *struct {
		Rate     json.RawMessage `json:"rate"`
		Exponent *int            `json:"exponent"`
	} `json:"feeSchedule"`
	Events []struct {
		Title string `json:"title"`
	} `json:"events"`
}

// arrayField supports Gamma's JSON-encoded strings and native JSON arrays.
func arrayField(raw json.RawMessage, target any) error {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil
	}
	if raw[0] == '"' {
		var encoded string
		if err := json.Unmarshal(raw, &encoded); err != nil {
			return err
		}
		raw = bytes.TrimSpace([]byte(encoded))
	}
	if !bytes.HasPrefix(raw, []byte("[")) {
		return fmt.Errorf("expected array or JSON-encoded array")
	}
	return json.Unmarshal(raw, target)
}

func parseMarket(record json.RawMessage) (models.Market, bool, error) {
	var wire gammaMarket
	var market models.Market
	if err := json.Unmarshal(record, &wire); err != nil {
		return market, false, err
	}
	if wire.Active == nil || wire.Closed == nil {
		return market, false, fmt.Errorf("missing active/closed status")
	}
	if !*wire.Active || *wire.Closed {
		return market, false, nil
	}
	if strings.TrimSpace(wire.ID) == "" || strings.TrimSpace(wire.Question) == "" {
		return market, false, fmt.Errorf("missing market ID or question")
	}
	var labels, tokens []string
	var prices []json.RawMessage
	if err := arrayField(wire.Outcomes, &labels); err != nil {
		return market, false, fmt.Errorf("market %s outcomes: %w", wire.ID, err)
	}
	if err := arrayField(wire.Prices, &prices); err != nil {
		return market, false, fmt.Errorf("market %s outcomePrices: %w", wire.ID, err)
	}
	assetIDs := wire.TokenIDs
	if strings.EqualFold(wire.Version, "v2") {
		assetIDs = wire.PositionIDs
	}
	if err := arrayField(assetIDs, &tokens); err != nil {
		return market, false, fmt.Errorf("market %s trading asset IDs: %w", wire.ID, err)
	}
	if len(labels) < 2 || (len(prices) != 0 && len(prices) != len(labels)) || (len(tokens) != 0 && len(tokens) != len(labels)) {
		return market, false, fmt.Errorf("market %s missing outcomes or mismatched outcome/price/token lengths", wire.ID)
	}
	market = models.Market{ID: wire.ID, Question: wire.Question, Slug: wire.Slug, ConditionID: wire.ConditionID, Description: wire.Description, EndDate: wire.EndDate, ResolutionSource: wire.ResolutionSource}
	market.FeesEnabled = wire.FeesEnabled
	if wire.FeeSchedule != nil {
		market.FeeRate = wire.FeeSchedule.Rate
		market.FeeExponent = wire.FeeSchedule.Exponent
	}
	for _, event := range wire.Events {
		if strings.TrimSpace(event.Title) != "" {
			market.EventTitles = append(market.EventTitles, event.Title)
		}
	}
	seen := make(map[string]bool)
	for i, label := range labels {
		key := strings.ToLower(strings.TrimSpace(label))
		if key == "" || seen[key] {
			return models.Market{}, false, fmt.Errorf("market %s empty or duplicate outcome", wire.ID)
		}
		seen[key] = true
		outcome := models.Outcome{Name: label}
		if len(prices) != 0 {
			price, err := parsePrice(prices[i])
			if err != nil {
				return models.Market{}, false, fmt.Errorf("market %s price for %q: %w", wire.ID, label, err)
			}
			outcome.IndicativePrice = price
		}
		if len(tokens) != 0 {
			outcome.TokenID = tokens[i]
		}
		market.Outcomes = append(market.Outcomes, outcome)
	}
	return market, true, nil
}

func parsePrice(raw json.RawMessage) (*float64, error) {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	value := string(raw)
	if len(raw) > 0 && raw[0] == '"' {
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, err
		}
	} else if len(raw) == 0 || (raw[0] != '-' && (raw[0] < '0' || raw[0] > '9')) {
		return nil, fmt.Errorf("expected numeric price")
	}
	price, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(price) || math.IsInf(price, 0) || price < 0 || price > 1 {
		return nil, fmt.Errorf("expected finite price in [0,1]")
	}
	return &price, nil
}
