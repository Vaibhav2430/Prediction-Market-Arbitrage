package registry

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Vaibhav2430/Prediction-Market-Arbitrage/internal/comparison"
	"github.com/Vaibhav2430/Prediction-Market-Arbitrage/internal/kalshi"
	"github.com/Vaibhav2430/Prediction-Market-Arbitrage/internal/models"
)

type Gamma interface {
	FetchMarket(context.Context, string) (models.Market, error)
}
type Kalshi interface {
	FetchMarket(context.Context, string) (kalshi.Market, error)
	FetchRuleSources(context.Context, string) (kalshi.RuleSources, error)
	FetchFeeInfo(context.Context, string) (kalshi.FeeInfo, error)
	FetchOrderBook(context.Context, string, int) (models.OrderBook, error)
}
type Books interface {
	FetchOrderBook(context.Context, string) (models.TokenBook, error)
}
type Terms interface {
	Read(context.Context, string) ([]byte, error)
}
type TermsClient struct{ httpClient *http.Client }

func allowedTerms(u *url.URL) bool {
	return u.Scheme == "https" && u.Host == "assets.kalshi.com" && u.User == nil && strings.HasPrefix(u.Path, "/contract_terms/") && u.RawQuery == "" && u.Fragment == ""
}
func NewTermsClient() *TermsClient {
	return &TermsClient{httpClient: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 3 || !allowedTerms(req.URL) {
			return fmt.Errorf("untrusted terms redirect")
		}
		return nil
	}}}
}
func (c *TermsClient) Read(ctx context.Context, address string) ([]byte, error) {
	u, err := url.Parse(address)
	if err != nil || !allowedTerms(u) {
		return nil, fmt.Errorf("terms URL must be an HTTPS Kalshi contract_terms URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("terms HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxBytes || len(data) < 5 || string(data[:5]) != "%PDF-" {
		return nil, fmt.Errorf("terms must be a PDF under 4 MiB")
	}
	return data, nil
}
func Capture(ctx context.Context, gamma Gamma, kal Kalshi, terms Terms, polyID, ticker string) (Snapshot, models.Market, kalshi.Market, error) {
	var zero Snapshot
	p, err := gamma.FetchMarket(ctx, polyID)
	if err != nil {
		return zero, p, kalshi.Market{}, err
	}
	k, err := kal.FetchMarket(ctx, ticker)
	if err != nil {
		return zero, p, k, err
	}
	if p.ID != polyID || k.Ticker != ticker {
		return zero, p, k, fmt.Errorf("captured market identity differs")
	}
	sources, err := kal.FetchRuleSources(ctx, k.EventTicker)
	if err != nil {
		return zero, p, k, err
	}
	document, err := terms.Read(ctx, sources.TermsURL)
	if err != nil {
		return zero, p, k, err
	}
	snapshot, err := BuildSnapshot(p, k, sources, document)
	return snapshot, p, k, err
}

type RefreshResult struct {
	ID                 string                `json:"id"`
	Status             string                `json:"status"`
	CurrentFingerprint string                `json:"current_rules_fingerprint,omitempty"`
	Message            string                `json:"message"`
	Scenarios          []comparison.Scenario `json:"conditional_scenarios,omitempty"`
	Warnings           []string              `json:"warnings,omitempty"`
}

func Refresh(ctx context.Context, e Entry, gamma Gamma, kal Kalshi, books Books, terms Terms, quantity int64, maxAge time.Duration) (RefreshResult, error) {
	result := RefreshResult{ID: e.ID}
	if err := Validate(e); err != nil {
		return result, err
	}
	if quantity < 1 || quantity > 1000000 || maxAge <= 0 {
		return result, fmt.Errorf("invalid quantity or max age")
	}
	if e.Status == "rejected" {
		result.Status = "rejected"
		result.Message = "Rejected entry; quotes were not fetched."
		return result, nil
	}
	snapshot, p, k, err := Capture(ctx, gamma, kal, terms, e.Snapshot.Polymarket.ID, e.Snapshot.Kalshi.ID)
	if err != nil {
		result.Status = "unverifiable"
		result.Message = "Rules could not be refreshed; quotes withheld."
		return result, err
	}
	result.CurrentFingerprint = Fingerprint(snapshot, e.Relation)
	result.Status = State(e, snapshot)
	if result.Status != "approved_rules_current" {
		result.Message = "Quotes withheld: requires a completed review of the current rules and outcome mapping."
		return result, nil
	}
	fees, feeErr := kal.FetchFeeInfo(ctx, k.EventTicker)
	if feeErr != nil {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		result.Warnings = append(result.Warnings, "Fee metadata unavailable: "+feeErr.Error())
	}
	feeModel := comparison.ResolveFees(p, fees)
	result.Warnings = append(result.Warnings, feeModel.Warnings...)
	y, n, _ := p.YesNoOutcomes()
	var py, pn models.TokenBook
	var kb models.OrderBook
	var ey, en, ek error
	var wg sync.WaitGroup
	wg.Add(3)
	go func() { defer wg.Done(); py, ey = books.FetchOrderBook(ctx, y.TokenID) }()
	go func() { defer wg.Done(); pn, en = books.FetchOrderBook(ctx, n.TokenID) }()
	go func() { defer wg.Done(); kb, ek = kal.FetchOrderBook(ctx, k.Ticker, 1) }()
	wg.Wait()
	for _, err := range []error{ey, en, ek} {
		if err != nil {
			return result, err
		}
	}
	if py.AssetID != y.TokenID || pn.AssetID != n.TokenID || py.ConditionID != p.ConditionID || pn.ConditionID != p.ConditionID || kb.Ticker != k.Ticker {
		return result, fmt.Errorf("quote identity mismatch")
	}
	now := time.Now()
	for _, book := range []models.TokenBook{py, pn} {
		if book.Timestamp.IsZero() || now.Sub(book.Timestamp) > maxAge || book.Timestamp.Sub(now) > 5*time.Second {
			result.Status = "stale_quotes"
			result.Message = "Quote timestamps are stale or invalid; net comparison withheld."
			return result, nil
		}
	}
	top := kb.Top()
	ka, kbAsk := top.NoAsk, top.YesAsk
	sideA, sideB := "NO", "YES"
	if e.Relation == "inverted" {
		ka, kbAsk = top.YesAsk, top.NoAsk
		sideA, sideB = "YES", "NO"
	}
	for _, leg := range []struct {
		side, kSide string
		book        models.TokenBook
		ask         *models.PriceLevel
	}{{"YES", sideA, py, ka}, {"NO", sideB, pn, kbAsk}} {
		scenario, err := comparison.Price(leg.side, leg.kSide, leg.book.BestAsk(), leg.ask, leg.book.MinOrderSize, quantity, feeModel)
		if err != nil {
			return result, err
		}
		if scenario.Status == "conditional_only" {
			scenario.Reason = "Based on recorded manual outcome review; fills, fees, timing and settlement remain conditional."
		}
		result.Scenarios = append(result.Scenarios, scenario)
	}
	result.Message = "Recorded manual review is current. Price differences are estimates, not guaranteed profit or a trading instruction."
	result.Warnings = append(result.Warnings, "Separate snapshots; Kalshi has no exchange timestamp. Collateral parity, execution and actual fill-level fees remain risks.")
	return result, nil
}
