package models

import "testing"

func TestYesNo(t *testing.T) {
	zero, one := 0.0, 1.0
	m := Market{Outcomes: []Outcome{{Name: " no ", IndicativePrice: &one}, {Name: "YES", IndicativePrice: &zero}}}
	yes, no, ok := m.YesNo()
	if !ok || yes == nil || *yes != 0 || no == nil || *no != 1 {
		t.Fatal("prices must follow labels and preserve zero")
	}
	m.Outcomes[1].IndicativePrice = nil
	yes, no, ok = m.YesNo()
	if !ok || yes != nil || no == nil {
		t.Fatal("missing price must remain unavailable")
	}
	for _, outcomes := range [][]Outcome{nil, {{Name: "Yes"}}, {{Name: "Yes"}, {Name: "Yes"}}, {{Name: "Up"}, {Name: "Down"}}, {{Name: "Yes"}, {Name: "No"}, {Name: "Other"}}} {
		if _, _, ok := (Market{Outcomes: outcomes}).YesNo(); ok {
			t.Fatalf("accepted nonbinary outcomes: %+v", outcomes)
		}
	}
}
