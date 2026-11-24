package polymarket

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/Vaibhav2430/Prediction-Market-Arbitrage/internal/models"
)

// FetchMarket reads a current active market directly by Gamma ID.
func (c *Client) FetchMarket(ctx context.Context, id string) (models.Market, error) {
	var zero models.Market
	if id == "" || len(id) > 30 {
		return zero, fmt.Errorf("invalid Gamma market ID")
	}
	for _, ch := range id {
		if ch < '0' || ch > '9' {
			return zero, fmt.Errorf("invalid Gamma market ID")
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/markets/"+id, nil)
	if err != nil {
		return zero, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return zero, fmt.Errorf("Gamma market: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return zero, fmt.Errorf("Gamma market HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return zero, err
	}
	if len(body) > maxResponseBytes {
		return zero, fmt.Errorf("Gamma market response too large")
	}
	m, include, err := parseMarket(body)
	if err != nil {
		return zero, err
	}
	if !include || m.ID != id {
		return zero, fmt.Errorf("Gamma market is not active or ID differs")
	}
	return m, nil
}
