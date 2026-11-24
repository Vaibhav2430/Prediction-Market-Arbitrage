package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/Vaibhav2430/Prediction-Market-Arbitrage/internal/kalshi"
	"github.com/Vaibhav2430/Prediction-Market-Arbitrage/internal/models"
)

type fakeGamma struct{}

func (fakeGamma) FetchMarket(context.Context, string) (models.Market, error) {
	enabled := true
	exponent := 1
	return models.Market{ID: "123", Question: "Inflation 3.3% or less?", ConditionID: "cond", Description: "Resolves to previous month if late", FeesEnabled: &enabled, FeeRate: json.RawMessage(`0.01`), FeeExponent: &exponent, Outcomes: []models.Outcome{{Name: "No", TokenID: "222"}, {Name: "Yes", TokenID: "111"}}}, nil
}

type fakeKal struct{ feeError bool }

func (fakeKal) FetchMarket(context.Context, string) (kalshi.Market, error) {
	return kalshi.Market{Ticker: "K", EventTicker: "E", Title: "Inflation above 3.3%?", RulesSecondary: "government shutdown extends for six months"}, nil
}
func (f fakeKal) FetchFeeInfo(context.Context, string) (kalshi.FeeInfo, error) {
	if f.feeError {
		return kalshi.FeeInfo{}, errors.New("fee service unavailable")
	}
	return kalshi.FeeInfo{Type: "quadratic", Multiplier: json.RawMessage(`1`)}, nil
}
func (fakeKal) FetchOrderBook(context.Context, string, int) (models.OrderBook, error) {
	return models.OrderBook{Ticker: "K", YesBids: []models.PriceLevel{{Price: 3500, Quantity: 1000}}, NoBids: []models.PriceLevel{{Price: 4000, Quantity: 1000}}, ReceivedAt: time.Now()}, nil
}

type fakeBooks struct{ stale, mismatch bool }

func (f fakeBooks) FetchOrderBook(_ context.Context, id string) (models.TokenBook, error) {
	stamp := time.Now()
	if f.stale {
		stamp = stamp.Add(-time.Hour)
	}
	condition := "cond"
	if f.mismatch {
		condition = "wrong"
	}
	return models.TokenBook{AssetID: id, ConditionID: condition, Timestamp: stamp, ReceivedAt: time.Now(), MinOrderSize: 5000000, Asks: []models.TokenLevel{{Price: 3000, Size: 10000000}}}, nil
}
func opts() options {
	return options{polyID: "123", ticker: "K", relation: "inverted", format: "json", quantity: 5, maxAge: time.Minute}
}
func TestInvertedPairUsesSameNamedLegsButNeverClaimsArbitrage(t *testing.T) {
	var out bytes.Buffer
	if err := execute(context.Background(), fakeGamma{}, fakeKal{}, fakeBooks{}, opts(), &out); err != nil {
		t.Fatal(err)
	}
	var r report
	if err := json.Unmarshal(out.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	if r.Status != "incompatible_exception_rules" || len(r.ReviewFindings) != 1 || r.Scenarios[0].KalshiSide != "YES" || r.Scenarios[1].KalshiSide != "NO" || r.Scenarios[0].Status != "conditional_only" {
		t.Fatalf("report=%+v", r)
	}
}
func TestSameRelationUsesOppositeNamedLegs(t *testing.T) {
	o := opts()
	o.relation = "same"
	var out bytes.Buffer
	if err := execute(context.Background(), fakeGamma{}, fakeKal{}, fakeBooks{}, o, &out); err != nil {
		t.Fatal(err)
	}
	var r report
	json.Unmarshal(out.Bytes(), &r)
	if r.Scenarios[0].KalshiSide != "NO" || r.Scenarios[1].KalshiSide != "YES" {
		t.Fatal(r.Scenarios)
	}
}
func TestStaleIdentityAndMissingFeeHandling(t *testing.T) {
	var out bytes.Buffer
	if err := execute(context.Background(), fakeGamma{}, fakeKal{}, fakeBooks{mismatch: true}, opts(), &out); err == nil {
		t.Fatal("identity mismatch accepted")
	}
	if err := execute(context.Background(), fakeGamma{}, fakeKal{}, fakeBooks{stale: true}, opts(), &out); err != nil {
		t.Fatal(err)
	}
	var r report
	json.Unmarshal(out.Bytes(), &r)
	if r.Scenarios[0].Status != "stale_snapshot" || r.Scenarios[0].ConditionalDifference != "" {
		t.Fatal(r.Scenarios)
	}
	out.Reset()
	if err := execute(context.Background(), fakeGamma{}, fakeKal{feeError: true}, fakeBooks{}, opts(), &out); err != nil {
		t.Fatal(err)
	}
	r = report{}
	json.Unmarshal(out.Bytes(), &r)
	if r.Scenarios[0].Total != "" || r.Scenarios[0].ConditionalDifference != "" {
		t.Fatal("net result emitted with missing fees")
	}
}
func TestInvalidArgumentsAndHelp(t *testing.T) {
	for _, args := range [][]string{nil, {"-polymarket-id", "123", "-kalshi-ticker", "K", "-relation", "bad"}, {"-quantity", "0"}} {
		if err := run(context.Background(), args, io.Discard, io.Discard); err == nil {
			t.Fatal("invalid flags accepted")
		}
	}
	if err := run(context.Background(), []string{"-help"}, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
}
