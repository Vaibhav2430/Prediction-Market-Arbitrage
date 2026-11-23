package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/Vaibhav2430/Prediction-Market-Arbitrage/internal/models"
)

func TestPrintMarkets(t *testing.T) {
	zero := 0.0
	markets := []models.Market{
		{ID: "123", Question: "Example?\nSecond line", Outcomes: []models.Outcome{{Name: "No"}, {Name: "Yes", IndicativePrice: &zero}}},
		{ID: "456", Question: "Direction?", Outcomes: []models.Outcome{{Name: "Up"}, {Name: "Down"}}},
	}
	var out bytes.Buffer
	printMarkets(&out, markets, 20, 40, 1)
	for _, want := range []string{"indicative YES/NO", "NOT executable", "YES indicative: $0.000000 | NO indicative: unavailable", "Example? Second line", "limit=20 offset=40", "1 non-YES/NO markets omitted", "1 invalid records skipped"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in %s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "Direction?") {
		t.Fatal("nonbinary market displayed as YES/NO")
	}
}

func TestPrintEmptyPage(t *testing.T) {
	var out bytes.Buffer
	printMarkets(&out, nil, 10, 0, 0)
	if !strings.Contains(out.String(), "No YES/NO markets") {
		t.Fatal(out.String())
	}
}

func TestRunInvalidArguments(t *testing.T) {
	for _, args := range [][]string{{"-limit", "0"}, {"-limit", "101"}, {"-offset", "-1"}, {"-timeout", "0s"}, {"-timeout", "bad"}, {"extra"}, {"-unknown"}} {
		var out bytes.Buffer
		if err := run(context.Background(), args, &out, &out); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}

func TestHelp(t *testing.T) {
	var out bytes.Buffer
	if err := run(context.Background(), []string{"-help"}, &out, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "-limit") {
		t.Fatal("missing help")
	}
}
