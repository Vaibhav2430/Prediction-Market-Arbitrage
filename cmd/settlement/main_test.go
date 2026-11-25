package main

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/Vaibhav2430/Prediction-Market-Arbitrage/internal/registry"
)

func TestSavedNewsomModelKeepsUnresolvedCases(t *testing.T) {
	file, err := registry.Load("../../data/reviewed-pairs.json")
	if err != nil {
		t.Fatal(err)
	}
	result, err := registry.AssessSettlement(file.Entries[0])
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "needs_review" || result.Aligned != 4 || result.Unresolved != 4 || result.PayoutFloorKnown {
		t.Fatalf("report=%+v", result)
	}
	var out bytes.Buffer
	if err := render(&out, result); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"no-acceptance", "acceptance-between-cutoffs", "Overall payout floor: unknown", "No registry status changed"} {
		if !strings.Contains(out.String(), text) {
			t.Fatalf("missing %q", text)
		}
	}
}
func TestOfflineCLIAndArgumentValidation(t *testing.T) {
	if err := run(context.Background(), []string{"-offline", "-registry", "../../data/reviewed-pairs.json", "-format", "json"}, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"-format", "bad"}, {"-timeout", "0s"}, {"extra"}, {"-offline", "-registry", "../../data/reviewed-pairs.json", "-pair-id", "missing"}} {
		if err := run(context.Background(), args, io.Discard, io.Discard); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	if err := run(context.Background(), []string{"-help"}, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
}
