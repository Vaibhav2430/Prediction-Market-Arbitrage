package matching

import (
	"context"
	"strings"
	"testing"
)

func pastedPair() (Contract, Contract) {
	return Contract{Venue: "polymarket", ID: "7121510", Title: "Will the price of Bitcoin be between $54,000 and $56,000 on November 24?", YesLabel: "Yes", Deadline: "2026-11-24T17:00:00Z", ResolutionSource: "https://www.binance.com/en/trade/BTC_USDT", RulesPrimary: `This market will resolve according to the final "Close" price of the Binance 1 minute candle for BTC/USDT closing at 12:00 in the ET timezone (noon) on the date specified in the title.`},
		Contract{Venue: "kalshi", ID: "KXBTCD-26NOV2320-T51099.99", Title: "Bitcoin price on Nov 23, 2026?", YesLabel: "$51,100 or above", Deadline: "2026-11-24T01:00:00Z", RulesPrimary: `If the simple average of the sixty seconds of CF Benchmarks' Bitcoin Real-Time Index (BRTI) before 8 PM EST is above 51099.99 at 8 PM EST on Nov 23, 2026, then the market resolves to Yes.`}
}

func TestPastedFalsePositiveRejectedBeforeRanking(t *testing.T) {
	p, k := pastedPair()
	pf, kf := Extract(p), Extract(k)
	if pf.Kind != "range" || pf.Lower != "54000" || pf.Upper != "56000" || kf.Kind != "threshold" || kf.Lower != "51099.99" || kf.Direction != "gt" {
		t.Fatalf("wrong predicates: %+v %+v", pf, kf)
	}
	if pf.Observation.Day != 24 || pf.Observation.Year != 0 || pf.Observation.Clock != "12:00" || kf.Observation.Day != 23 || kf.Observation.Year != 2026 || kf.Observation.Clock != "20:00" {
		t.Fatalf("wrong observation: %+v %+v", pf.Observation, kf.Observation)
	}
	pairs, total, stats, err := FindDetailed(context.Background(), []Contract{p}, []Contract{k}, Options{Threshold: .4, Top: 5})
	if err != nil || len(pairs) != 0 || total != 0 || stats.TextCandidates != 1 || stats.RejectedPairs != 1 {
		t.Fatalf("pairs=%+v stats=%+v err=%v", pairs, stats, err)
	}
	for _, reason := range []string{"contract_type", "observation_date", "resolution_source", "measurement"} {
		if stats.RejectionReasons[reason] != 1 {
			t.Fatalf("missing %s in %+v", reason, stats)
		}
	}
}

func TestAmountsAreExactAndNormalizeFormatting(t *testing.T) {
	for _, tt := range []struct{ input, want string }{{"54,000", "54000"}, {"54k", "54000"}, {"0.054M", "54000"}, {"51099.9900", "51099.99"}, {"0.000001", "0.000001"}, {"9007199254740993", "9007199254740993"}} {
		got, ok := normalizedAmount(tt.input)
		if !ok || got != tt.want {
			t.Fatalf("%s = %s want %s", tt.input, got, tt.want)
		}
	}
}

func TestPredicateTypesAndBoundaryDirections(t *testing.T) {
	for _, tt := range []struct{ text, kind, direction, lo, hi string }{
		{"Bitcoin between $54,000 and $56,000", "range", "between", "54000", "56000"},
		{"$54,000 to $56,000", "range", "between", "54000", "56000"},
		{"Bitcoin above $54k", "threshold", "gt", "54000", ""},
		{"Bitcoin below $54,000", "threshold", "lt", "54000", ""},
		{"$54,000 or above", "threshold", "gte", "54000", ""},
		{"$54,000 or BELOW", "threshold", "lte", "54000", ""},
		{"Bitcoin at least 54000", "threshold", "gte", "54000", ""},
		{"Bitcoin at most 54000", "threshold", "lte", "54000", ""},
		{"Bitcoin hits an unspecified price", "", "", "", ""},
		{"Bitcoin not above $54000", "", "", "", ""},
		{"above $54000 or below $50000", "", "", "", ""},
	} {
		kind, direction, lo, hi, _ := predicate(tt.text)
		if kind != tt.kind || direction != tt.direction || lo != tt.lo || hi != tt.hi {
			t.Fatalf("%q => %s %s %s %s", tt.text, kind, direction, lo, hi)
		}
	}
}

func TestIndependentKnownConflicts(t *testing.T) {
	base := Facts{Subject: "bitcoin", Kind: "threshold", Direction: "gte", Lower: "54000", Mode: "point", Source: "binance", Measurement: "one_minute_candle_close", Observation: Observation{Year: 2026, Month: 11, Day: 24, Clock: "12:00", Zone: "ET"}}
	for _, tt := range []struct {
		name   string
		change func(*Facts)
	}{
		{"subject", func(f *Facts) { f.Subject = "ethereum" }},
		{"contract_type", func(f *Facts) { f.Kind = "range" }},
		{"direction", func(f *Facts) { f.Direction = "gt" }},
		{"threshold", func(f *Facts) { f.Lower = "54000.01" }},
		{"observation_mode", func(f *Facts) { f.Mode = "touch" }},
		{"resolution_source", func(f *Facts) { f.Source = "cf_benchmarks" }},
		{"measurement", func(f *Facts) { f.Measurement = "sixty_second_index_average" }},
		{"observation_date", func(f *Facts) { f.Observation.Day = 25 }},
		{"observation_time", func(f *Facts) { f.Observation.Clock = "20:00" }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			other := base
			tt.change(&other)
			if !contains(Conflicts(base, other), tt.name) {
				t.Fatalf("expected %s in %v", tt.name, Conflicts(base, other))
			}
		})
	}
	other := base
	other.Kind = "range"
	base.Kind = "range"
	base.Upper = "56000"
	other.Upper = "57000"
	if !contains(Conflicts(base, other), "range_upper") {
		t.Fatal("different upper bounds accepted")
	}
}
func contains(items []string, want string) bool {
	for _, v := range items {
		if v == want {
			return true
		}
	}
	return false
}

func TestSameInstantAcrossExplicitZones(t *testing.T) {
	a := Facts{Observation: Observation{Year: 2026, Month: 11, Day: 24, Clock: "12:00", Zone: "ET"}}
	b := Facts{Observation: Observation{Year: 2026, Month: 11, Day: 24, Clock: "17:00", Zone: "UTC"}}
	if contains(Conflicts(a, b), "observation_time") {
		t.Fatal("equivalent winter instants rejected")
	}
	a.Observation.Month = 7
	b.Observation.Month = 7
	b.Observation.Clock = "16:00"
	if contains(Conflicts(a, b), "observation_time") {
		t.Fatal("DST not applied")
	}
}

func TestUnknownDetailsRemainUnverifiedRatherThanAgreement(t *testing.T) {
	p := fixture("polymarket", "p", "Alice Smith wins election")
	k := fixture("kalshi", "k", p.Title)
	pairs, _, _, err := FindDetailed(context.Background(), []Contract{p}, []Contract{k}, Options{Threshold: .4, Top: 5})
	if err != nil || len(pairs) != 1 || pairs[0].Status != "needs_review" {
		t.Fatalf("pairs=%+v err=%v", pairs, err)
	}
	if !strings.Contains(strings.Join(pairs[0].Warnings, " "), "Missing details are not evidence of agreement") {
		t.Fatal(pairs[0].Warnings)
	}
	// Differing platform deadlines alone are not observation-time evidence.
	k.Deadline = "2029-01-01T00:00:00Z"
	if len(Conflicts(Extract(p), Extract(k))) != 0 {
		t.Fatal("trading close used as observation time")
	}
}

func TestMatchingFactsNeverAutoApprove(t *testing.T) {
	p := fixture("polymarket", "p", "Bitcoin above $54,000 on November 24, 2026")
	p.RulesPrimary = "Binance final Close of a 1 minute candle closing at 12:00 UTC on November 24, 2026"
	p.ResolutionSource = "https://binance.com"
	k := p
	k.ID = "k"
	k.Venue = "kalshi"
	pairs, total, stats, err := FindDetailed(context.Background(), []Contract{p}, []Contract{k}, Options{Threshold: .4, Top: 5})
	if err != nil || total != 1 || stats.RejectedPairs != 0 || pairs[0].Status != "needs_review" {
		t.Fatalf("pairs=%+v stats=%+v err=%v", pairs, stats, err)
	}
}

func TestAmbiguousDateAndSourcesStayUnknown(t *testing.T) {
	c := Contract{Title: "Bitcoin price from November 23, 2026 to November 24, 2026", RulesPrimary: "Compare Binance and CF Benchmarks prices; source unspecified"}
	f := Extract(c)
	if f.Source != "" || f.Observation.Day != 0 {
		t.Fatalf("ambiguous details must stay unknown: %+v", f)
	}
	if o, ok := observationDate("February 31, 2026"); ok {
		t.Fatal(o)
	}
	if _, _, ok := observationClock("8 ET"); ok {
		t.Fatal("ambiguous 12/24h clock accepted")
	}
}

func TestSameInstantAcrossCalendarBoundary(t *testing.T) {
	a := Facts{Observation: Observation{Year: 2026, Month: 11, Day: 23, Clock: "20:00", Zone: "EST"}}
	b := Facts{Observation: Observation{Year: 2026, Month: 11, Day: 24, Clock: "01:00", Zone: "UTC"}}
	if reasons := Conflicts(a, b); len(reasons) != 0 {
		t.Fatalf("same instant rejected: %v", reasons)
	}
}
