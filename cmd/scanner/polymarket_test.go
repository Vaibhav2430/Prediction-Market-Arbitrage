package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Vaibhav2430/Prediction-Market-Arbitrage/internal/models"
	"github.com/Vaibhav2430/Prediction-Market-Arbitrage/internal/polymarket"
)

type fakeGamma struct {
	result polymarket.Result
	err    error
}

func (f fakeGamma) FetchActiveMarkets(context.Context, int, int) (polymarket.Result, error) {
	return f.result, f.err
}

type fakePolyBooks struct {
	calls    []string
	fail     bool
	mismatch bool
}

func (f *fakePolyBooks) FetchOrderBook(ctx context.Context, id string) (models.TokenBook, error) {
	f.calls = append(f.calls, id)
	if f.fail && id == "222" {
		return models.TokenBook{}, errors.New("HTTP 404")
	}
	condition := "cond"
	if f.mismatch {
		condition = "different"
	}
	return models.TokenBook{AssetID: id, ConditionID: condition, Bids: []models.TokenLevel{{Price: 3000, Size: 1234567}}, Asks: []models.TokenLevel{{Price: 4000, Size: 2000000}}, Timestamp: time.UnixMilli(1795478687525), ReceivedAt: time.UnixMilli(1795478687625), TickSize: 10, MinOrderSize: 5000000}, nil
}
func polyFixture() fakeGamma {
	return fakeGamma{result: polymarket.Result{Markets: []models.Market{{ID: "123", Question: "Question?", ConditionID: "cond", Outcomes: []models.Outcome{{Name: "No", TokenID: "222"}, {Name: "Yes", TokenID: "111"}}}}}}
}

func TestScanPolymarketUsesLabelsAndActualBooks(t *testing.T) {
	books := &fakePolyBooks{}
	var out, errOut bytes.Buffer
	if err := scanPolymarket(context.Background(), polyFixture(), books, 3, 7, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	if strings.Join(books.calls, ",") != "111,222" {
		t.Fatalf("wrong asset mapping: %v", books.calls)
	}
	for _, want := range []string{"Polymarket CLOB", "YES bid: $0.3000 x 1.234567 shares", "YES ask: $0.4000 x 2.000000 shares", "NO bid: $0.3000", "minimum order: 5.000000 shares", "100ms", "1 complete YES/NO markets", "offset=7", "Gamma indicative prices are not used as quotes"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in %s", want, out.String())
		}
	}
}

func TestScanPolymarketFailures(t *testing.T) {
	for _, books := range []*fakePolyBooks{{fail: true}, {mismatch: true}} {
		var out, errOut bytes.Buffer
		if err := scanPolymarket(context.Background(), polyFixture(), books, 3, 0, &out, &errOut); err == nil {
			t.Fatal("incomplete run must fail")
		}
		if !strings.Contains(out.String(), "unavailable") || errOut.Len() == 0 {
			t.Fatal("missing warning")
		}
	}
	gamma := polyFixture()
	gamma.result.Markets[0].Outcomes[0].TokenID = ""
	var out bytes.Buffer
	books := &fakePolyBooks{}
	if err := scanPolymarket(context.Background(), gamma, books, 3, 0, &out, &out); err == nil || len(books.calls) != 0 {
		t.Fatal("invalid IDs must fail before book fetch")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := scanPolymarket(ctx, polyFixture(), books, 3, 0, &out, &out); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestScanPolymarketOmittedAndEmptyPages(t *testing.T) {
	var out bytes.Buffer
	gamma := fakeGamma{result: polymarket.Result{Markets: []models.Market{{Outcomes: []models.Outcome{{Name: "Up"}, {Name: "Down"}}}}, Warnings: []error{errors.New("invalid market")}}}
	books := &fakePolyBooks{}
	if err := scanPolymarket(context.Background(), gamma, books, 3, 0, &out, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "1 non-YES/NO omitted") || !strings.Contains(out.String(), "1 invalid Gamma records skipped") || len(books.calls) != 0 {
		t.Fatal(out.String())
	}
	if tokenLevelText(nil) != "unavailable" {
		t.Fatal("empty side must be unavailable")
	}
	gamma.err = errors.New("Gamma HTTP 500")
	if err := scanPolymarket(context.Background(), gamma, books, 3, 0, &out, &out); err == nil {
		t.Fatal("discovery errors must fail")
	}
}

func TestIndicativeRequiresPolymarket(t *testing.T) {
	var out bytes.Buffer
	if err := run(context.Background(), []string{"-venue", "kalshi", "-indicative"}, &out, &out); err == nil {
		t.Fatal("invalid combination accepted")
	}
}
