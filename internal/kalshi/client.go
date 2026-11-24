// Package kalshi reads public Kalshi metadata and order-book snapshots.
// It has no authentication, account access, or order placement operations.
package kalshi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Vaibhav2430/Prediction-Market-Arbitrage/internal/models"
)

const BaseURL = "https://external-api.kalshi.com/trade-api/v2"
const maxResponseBytes = 4 << 20

type Client struct {
	baseURL    string
	httpClient *http.Client
}

func NewClient() *Client {
	return &Client{baseURL: BaseURL, httpClient: &http.Client{Timeout: 10 * time.Second}}
}

// Market retains settlement context for future manual contract matching.
// Listing quote fields are deliberately not treated as order-book liquidity.
type Market struct {
	Ticker         string          `json:"ticker"`
	EventTicker    string          `json:"event_ticker"`
	Title          string          `json:"title"`
	YesSubtitle    string          `json:"yes_sub_title"`
	NoSubtitle     string          `json:"no_sub_title"`
	Status         string          `json:"status"`
	Type           string          `json:"market_type"`
	CloseTime      time.Time       `json:"close_time"`
	RulesPrimary   string          `json:"rules_primary"`
	RulesSecondary string          `json:"rules_secondary"`
	Notional       json.RawMessage `json:"notional_value_dollars"`
}

type MarketPage struct {
	Markets  []Market
	Cursor   string
	Warnings []error
}

// FetchOpenMarkets reads one bounded page, excluding multivariate combos.
// The API query uses status=open; returned market status is normally active.
func (c *Client) FetchOpenMarkets(ctx context.Context, limit int, cursor, series string) (MarketPage, error) {
	var page MarketPage
	if limit < 1 || limit > 1000 {
		return page, fmt.Errorf("Kalshi limit must be 1..1000")
	}
	q := url.Values{"status": {"open"}, "limit": {strconv.Itoa(limit)}, "mve_filter": {"exclude"}}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	if series != "" {
		q.Set("series_ticker", series)
	}
	var wire struct {
		Markets json.RawMessage `json:"markets"`
		Cursor  *string         `json:"cursor"`
	}
	if err := c.get(ctx, "/markets", q, &wire); err != nil {
		return page, err
	}
	if !bytes.HasPrefix(bytes.TrimSpace(wire.Markets), []byte("[")) || wire.Cursor == nil {
		return page, fmt.Errorf("Kalshi response requires markets array and cursor")
	}
	var records []json.RawMessage
	if err := json.Unmarshal(wire.Markets, &records); err != nil {
		return page, fmt.Errorf("Kalshi markets: %w", err)
	}
	page.Cursor = *wire.Cursor
	for i, record := range records {
		market, include, err := parseMarket(record)
		if err != nil {
			page.Warnings = append(page.Warnings, fmt.Errorf("record %d: %w", i, err))
			continue
		}
		if include {
			page.Markets = append(page.Markets, market)
		}
	}
	return page, nil
}

// FetchMarket verifies a ticker before requesting its order book. Kalshi can
// return an empty book even for nonexistent tickers, so a book is not proof
// that a contract exists or is open.
func (c *Client) FetchMarket(ctx context.Context, ticker string) (Market, error) {
	var market Market
	if err := validateTicker(ticker); err != nil {
		return market, err
	}
	var wire struct {
		Market json.RawMessage `json:"market"`
	}
	if err := c.get(ctx, "/markets/"+url.PathEscape(ticker), nil, &wire); err != nil {
		return market, err
	}
	market, include, err := parseMarket(wire.Market)
	if err != nil {
		return Market{}, err
	}
	if !include {
		return Market{}, fmt.Errorf("Kalshi market is not an active $1 binary contract")
	}
	if market.Ticker != ticker {
		return Market{}, fmt.Errorf("Kalshi returned a different ticker")
	}
	return market, nil
}

func parseMarket(raw json.RawMessage) (Market, bool, error) {
	var market Market
	if err := json.Unmarshal(raw, &market); err != nil {
		return market, false, fmt.Errorf("Kalshi market: %w", err)
	}
	if err := validateTicker(market.Ticker); err != nil {
		return market, false, err
	}
	if market.Status == "" || market.Type == "" {
		return market, false, fmt.Errorf("market %q missing status/type", market.Ticker)
	}
	if market.Status != "active" && market.Status != "open" {
		return market, false, nil
	}
	if market.Type != "binary" {
		return market, false, nil
	}
	if strings.TrimSpace(market.Title) == "" || market.CloseTime.IsZero() {
		return market, false, fmt.Errorf("market %q missing title/close time", market.Ticker)
	}
	notional, err := parseFixed(market.Notional, 4)
	if err != nil || notional != int64(models.Dollar) {
		return market, false, fmt.Errorf("market %q requires a $1 notional", market.Ticker)
	}
	return market, true, nil
}

func validateTicker(ticker string) error {
	if ticker == "" || ticker == "." || ticker == ".." {
		return fmt.Errorf("invalid Kalshi ticker")
	}
	for _, ch := range ticker {
		if !(ch >= 'A' && ch <= 'Z' || ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '-' || ch == '_' || ch == '.') {
			return fmt.Errorf("invalid Kalshi ticker")
		}
	}
	return nil
}

// FetchOrderBook reads fixed-point dollar bid levels. Depth 0 fetches all
// levels; 1..100 requests that many best levels per side. Missing/null sides
// are empty books, whereas missing orderbook_fp is a schema error.
func (c *Client) FetchOrderBook(ctx context.Context, ticker string, depth int) (models.OrderBook, error) {
	book := models.OrderBook{Ticker: ticker}
	if err := validateTicker(ticker); err != nil {
		return book, err
	}
	if depth < 0 || depth > 100 {
		return book, fmt.Errorf("Kalshi depth must be 0..100")
	}
	var wire struct {
		Book *struct {
			Yes json.RawMessage `json:"yes_dollars"`
			No  json.RawMessage `json:"no_dollars"`
		} `json:"orderbook_fp"`
	}
	if err := c.get(ctx, "/markets/"+url.PathEscape(ticker)+"/orderbook", url.Values{"depth": {strconv.Itoa(depth)}}, &wire); err != nil {
		return book, err
	}
	received := time.Now().UTC()
	if wire.Book == nil {
		return book, fmt.Errorf("Kalshi response missing orderbook_fp")
	}
	yes, err := parseLevels(wire.Book.Yes)
	if err != nil {
		return book, fmt.Errorf("Kalshi YES bids: %w", err)
	}
	no, err := parseLevels(wire.Book.No)
	if err != nil {
		return book, fmt.Errorf("Kalshi NO bids: %w", err)
	}
	if len(yes) > 0 && len(no) > 0 && yes[0].Price+no[0].Price > models.Dollar {
		return book, fmt.Errorf("Kalshi crossed order book")
	}
	book.YesBids, book.NoBids, book.ReceivedAt = yes, no, received
	return book, nil
}

func parseLevels(raw json.RawMessage) ([]models.PriceLevel, error) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	var rows [][]json.RawMessage
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, fmt.Errorf("expected price/quantity array: %w", err)
	}
	levels := make([]models.PriceLevel, 0, len(rows))
	seen := make(map[int64]bool)
	for i, row := range rows {
		if len(row) != 2 {
			return nil, fmt.Errorf("level %d requires price and quantity", i)
		}
		price, err := parseFixed(row[0], 4)
		if err != nil || price > int64(models.Dollar) {
			return nil, fmt.Errorf("level %d requires price in [0,1] with at most 4 decimals", i)
		}
		quantity, err := parseFixed(row[1], 2)
		if err != nil || quantity <= 0 {
			return nil, fmt.Errorf("level %d requires positive quantity with at most 2 decimals", i)
		}
		if seen[price] {
			return nil, fmt.Errorf("duplicate price level %d", i)
		}
		seen[price] = true
		levels = append(levels, models.PriceLevel{Price: models.Price(price), Quantity: models.Quantity(quantity)})
	}
	sort.Slice(levels, func(i, j int) bool { return levels[i].Price > levels[j].Price })
	return levels, nil
}

// parseFixed parses decimal strings or JSON numbers without floating point.
// It rejects negative, nonfinite, overflowing, and over-precision values.
func parseFixed(raw json.RawMessage, decimals int) (int64, error) {
	raw = bytes.TrimSpace(raw)
	value := string(raw)
	if len(raw) > 0 && raw[0] == '"' {
		if err := json.Unmarshal(raw, &value); err != nil {
			return 0, err
		}
	}
	parts := strings.Split(value, ".")
	if len(parts) > 2 || parts[0] == "" {
		return 0, fmt.Errorf("invalid decimal")
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
		if fraction == "" {
			return 0, fmt.Errorf("invalid decimal")
		}
	}
	if len(fraction) > decimals {
		return 0, fmt.Errorf("too many decimal places")
	}
	for _, ch := range parts[0] + fraction {
		if ch < '0' || ch > '9' {
			return 0, fmt.Errorf("invalid decimal")
		}
	}
	digits := parts[0] + fraction + strings.Repeat("0", decimals-len(fraction))
	n, err := strconv.ParseInt(digits, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("decimal overflow: %w", err)
	}
	return n, nil
}

func (c *Client) get(ctx context.Context, path string, q url.Values, target any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("Kalshi request: %w", err)
	}
	req.URL.RawQuery = q.Encode()
	req.Header.Set("Accept", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("fetch Kalshi: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Kalshi HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return fmt.Errorf("read Kalshi response: %w", err)
	}
	if len(body) > maxResponseBytes {
		return fmt.Errorf("Kalshi response exceeds %d bytes", maxResponseBytes)
	}
	if !bytes.HasPrefix(bytes.TrimSpace(body), []byte("{")) {
		return fmt.Errorf("Kalshi response must be an object")
	}
	if err := json.Unmarshal(body, target); err != nil {
		return fmt.Errorf("decode Kalshi response: %w", err)
	}
	return nil
}
