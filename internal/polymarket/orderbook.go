package polymarket

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

const CLOBURL = "https://clob.polymarket.com"

// BookClient reads only public CLOB snapshots. It does not authenticate or trade.
type BookClient struct {
	baseURL    string
	httpClient *http.Client
}

func NewBookClient() *BookClient {
	return &BookClient{baseURL: CLOBURL, httpClient: &http.Client{Timeout: 10 * time.Second}}
}

func (c *BookClient) FetchOrderBook(ctx context.Context, assetID string) (models.TokenBook, error) {
	var book models.TokenBook
	if assetID == "" || len(assetID) > 78 {
		return book, fmt.Errorf("Polymarket asset ID must be an unsigned decimal identifier")
	}
	for _, ch := range assetID {
		if ch < '0' || ch > '9' {
			return book, fmt.Errorf("Polymarket asset ID must be an unsigned decimal identifier")
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/book", nil)
	if err != nil {
		return book, fmt.Errorf("CLOB request: %w", err)
	}
	req.URL.RawQuery = url.Values{"token_id": {assetID}}.Encode()
	req.Header.Set("Accept", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return book, fmt.Errorf("fetch CLOB order book: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return book, fmt.Errorf("CLOB HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return book, fmt.Errorf("read CLOB response: %w", err)
	}
	if len(body) > maxResponseBytes {
		return book, fmt.Errorf("CLOB response exceeds %d bytes", maxResponseBytes)
	}
	received := time.Now().UTC()
	book, err = parseOrderBook(body, assetID)
	if err != nil {
		return models.TokenBook{}, err
	}
	book.ReceivedAt = received
	return book, nil
}

type wireBook struct {
	AssetID      string          `json:"asset_id"`
	Market       string          `json:"market"`
	Timestamp    json.RawMessage `json:"timestamp"`
	Hash         string          `json:"hash"`
	Bids         json.RawMessage `json:"bids"`
	Asks         json.RawMessage `json:"asks"`
	TickSize     json.RawMessage `json:"tick_size"`
	MinSize      json.RawMessage `json:"min_order_size"`
	NegativeRisk *bool           `json:"neg_risk"`
}

func parseOrderBook(body []byte, assetID string) (models.TokenBook, error) {
	var book models.TokenBook
	if !bytes.HasPrefix(bytes.TrimSpace(body), []byte("{")) {
		return book, fmt.Errorf("CLOB response must be an object")
	}
	var wire wireBook
	if err := json.Unmarshal(body, &wire); err != nil {
		return book, fmt.Errorf("decode CLOB response: %w", err)
	}
	if wire.AssetID != assetID {
		return book, fmt.Errorf("CLOB asset ID does not match request")
	}
	if strings.TrimSpace(wire.Market) == "" || strings.TrimSpace(wire.Hash) == "" || wire.NegativeRisk == nil {
		return book, fmt.Errorf("CLOB response missing market, hash, or neg_risk")
	}
	timestamp, err := bookDecimal(wire.Timestamp, 0)
	if err != nil || timestamp <= 0 || timestamp > 253402300799999 {
		return book, fmt.Errorf("CLOB timestamp must be positive Unix milliseconds")
	}
	tick, err := bookDecimal(wire.TickSize, 4)
	if err != nil || tick <= 0 || tick > int64(models.Dollar) {
		return book, fmt.Errorf("CLOB tick_size must be in (0,1] with at most 4 decimals")
	}
	minSize, err := bookDecimal(wire.MinSize, 6)
	if err != nil || minSize <= 0 {
		return book, fmt.Errorf("CLOB min_order_size must be positive")
	}
	bids, err := bookLevels(wire.Bids, true, models.Price(tick))
	if err != nil {
		return book, fmt.Errorf("CLOB bids: %w", err)
	}
	asks, err := bookLevels(wire.Asks, false, models.Price(tick))
	if err != nil {
		return book, fmt.Errorf("CLOB asks: %w", err)
	}
	if len(bids) > 0 && len(asks) > 0 && bids[0].Price > asks[0].Price {
		return book, fmt.Errorf("CLOB crossed order book")
	}
	return models.TokenBook{AssetID: wire.AssetID, ConditionID: wire.Market, Timestamp: time.UnixMilli(timestamp).UTC(), Hash: wire.Hash, TickSize: models.Price(tick), MinOrderSize: models.Shares(minSize), NegativeRisk: *wire.NegativeRisk, Bids: bids, Asks: asks}, nil
}

func bookLevels(raw json.RawMessage, bids bool, tick models.Price) ([]models.TokenLevel, error) {
	// An explicit empty array is a valid empty side. Missing/null sides are schema
	// errors and must not silently become absent liquidity.
	if !bytes.HasPrefix(bytes.TrimSpace(raw), []byte("[")) {
		return nil, fmt.Errorf("expected levels array")
	}
	var rows []struct {
		Price json.RawMessage `json:"price"`
		Size  json.RawMessage `json:"size"`
	}
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, err
	}
	levels := make([]models.TokenLevel, 0, len(rows))
	seen := make(map[int64]bool)
	for i, row := range rows {
		price, err := bookDecimal(row.Price, 4)
		if err != nil || price > int64(models.Dollar) {
			return nil, fmt.Errorf("level %d invalid price", i)
		}
		size, err := bookDecimal(row.Size, 6)
		if err != nil || size <= 0 {
			return nil, fmt.Errorf("level %d invalid size", i)
		}
		if price%int64(tick) != 0 {
			return nil, fmt.Errorf("level %d price off tick", i)
		}
		if seen[price] {
			return nil, fmt.Errorf("level %d duplicate price", i)
		}
		seen[price] = true
		levels = append(levels, models.TokenLevel{Price: models.Price(price), Size: models.Shares(size)})
	}
	sort.Slice(levels, func(i, j int) bool {
		if bids {
			return levels[i].Price > levels[j].Price
		}
		return levels[i].Price < levels[j].Price
	})
	return levels, nil
}

// bookDecimal accepts decimal strings or native JSON numbers and returns exact
// scaled integers. Extra trailing zeroes are harmless; significant precision
// beyond the scale is rejected, never rounded. Signs/exponents are rejected.
func bookDecimal(raw json.RawMessage, decimals int) (int64, error) {
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
		if parts[1] == "" {
			return 0, fmt.Errorf("invalid decimal")
		}
		fraction = parts[1]
	}
	for _, ch := range parts[0] + fraction {
		if ch < '0' || ch > '9' {
			return 0, fmt.Errorf("invalid decimal")
		}
	}
	fraction = strings.TrimRight(fraction, "0")
	if len(fraction) > decimals {
		return 0, fmt.Errorf("unsupported decimal precision")
	}
	value = parts[0] + fraction + strings.Repeat("0", decimals-len(fraction))
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("decimal overflow: %w", err)
	}
	return n, nil
}
