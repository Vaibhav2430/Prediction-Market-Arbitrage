package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Vaibhav2430/Prediction-Market-Arbitrage/internal/kalshi"
	"github.com/Vaibhav2430/Prediction-Market-Arbitrage/internal/models"
)

type fakeKalshi struct {
	page                   kalshi.MarketPage
	book                   models.OrderBook
	err                    error
	limit, depth           int
	cursor, series, ticker string
	verified               bool
}

func (f *fakeKalshi) FetchOpenMarkets(_ context.Context, limit int, cursor, series string) (kalshi.MarketPage, error) {
	f.limit, f.cursor, f.series = limit, cursor, series
	return f.page, nil
}
func (f *fakeKalshi) FetchMarket(_ context.Context, ticker string) (kalshi.Market, error) {
	f.verified = true
	return kalshi.Market{Ticker: ticker, Title: "Single market"}, nil
}
func (f *fakeKalshi) FetchOrderBook(_ context.Context, ticker string, depth int) (models.OrderBook, error) {
	f.ticker, f.depth = ticker, depth
	return f.book, f.err
}

func TestScanKalshiOutput(t *testing.T) {
	fake := &fakeKalshi{
		page: kalshi.MarketPage{Markets: []kalshi.Market{{Ticker: "TEST", Title: "Question?\nSecond line", YesSubtitle: "Above", NoSubtitle: "Below", CloseTime: time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)}}, Cursor: "next+/=", Warnings: []error{errors.New("invalid record")}},
		book: models.OrderBook{NoBids: []models.PriceLevel{{Price: 3500, Quantity: 1250}}, ReceivedAt: time.Date(2026, 11, 23, 1, 2, 3, 0, time.UTC)},
	}
	var out, errOut bytes.Buffer
	err := scanKalshi(context.Background(), fake, kalshiOptions{limit: 3, depth: 5, cursor: "input+/=", series: "SERIES"}, &out, &errOut)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Question? Second line", "YES bid: unavailable", "YES ask (derived): $0.6500 x 12.50 contracts", "NO bid: $0.3500 x 12.50 contracts", "NO ask (derived): unavailable", "Next cursor: next+/=", "1 invalid records skipped", "Fees are excluded"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in %s", want, out.String())
		}
	}
	if fake.limit != 3 || fake.depth != 5 || fake.cursor != "input+/=" || fake.series != "SERIES" {
		t.Fatal("options not passed to client")
	}
	if !strings.Contains(errOut.String(), "invalid record") {
		t.Fatal("missing warning")
	}
}

func TestScanKalshiDirectTickerAndEmptyBook(t *testing.T) {
	fake := &fakeKalshi{}
	var out, errOut bytes.Buffer
	if err := scanKalshi(context.Background(), fake, kalshiOptions{ticker: "TEST", depth: 1}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	if !fake.verified || fake.ticker != "TEST" {
		t.Fatal("direct ticker must be verified")
	}
	if !strings.Contains(out.String(), "Empty order book") || !strings.Contains(out.String(), "YES ask (derived): unavailable") {
		t.Fatal(out.String())
	}
}

func TestScanKalshiFailures(t *testing.T) {
	fake := &fakeKalshi{page: kalshi.MarketPage{Markets: []kalshi.Market{{Ticker: "TEST"}}}, err: errors.New("HTTP 429")}
	var out, errOut bytes.Buffer
	if err := scanKalshi(context.Background(), fake, kalshiOptions{}, &out, &errOut); err == nil {
		t.Fatal("partial failure must fail run")
	}
	if !strings.Contains(errOut.String(), "HTTP 429") || !strings.Contains(out.String(), "1 order-book requests failed") {
		t.Fatal("missing failure details")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := scanKalshi(ctx, fake, kalshiOptions{}, &out, &errOut); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestKalshiInvalidFlags(t *testing.T) {
	for _, args := range [][]string{
		{"-venue", "invalid"}, {"-ticker", "TEST"}, {"-cursor", "abc"},
		{"-venue", "kalshi", "-offset", "1"}, {"-venue", "kalshi", "-limit", "1001"},
		{"-venue", "kalshi", "-depth", "101"}, {"-venue", "kalshi", "-depth", "-1"},
		{"-venue", "kalshi", "-ticker", "TEST", "-series", "SERIES"},
		{"-venue", "kalshi", "-ticker", "TEST", "-cursor", "CURSOR"},
	} {
		var out bytes.Buffer
		if err := run(context.Background(), args, &out, &out); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}

func TestTerminalText(t *testing.T) {
	if got := terminalText("text\x1b\nmore\r\twords"); got != "text more words" {
		t.Fatal(got)
	}
}
