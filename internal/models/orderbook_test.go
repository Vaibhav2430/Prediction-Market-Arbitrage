package models

import "testing"

func TestTopComplementsAndQuantities(t *testing.T) {
	book := OrderBook{YesBids: []PriceLevel{{Price: 4001, Quantity: 1250}}, NoBids: []PriceLevel{{Price: 3500, Quantity: 205}}}
	top := book.Top()
	if top.YesBid.Price != 4001 || top.NoAsk.Price != 5999 || top.NoAsk.Quantity != 1250 || top.YesAsk.Price != 6500 || top.YesAsk.Quantity != 205 {
		t.Fatalf("incorrect complements: %+v", top)
	}
	if got := top.NoAsk.Price.String(); got != "$0.5999" {
		t.Fatal(got)
	}
	if got := top.NoAsk.Quantity.String(); got != "12.50" {
		t.Fatal(got)
	}
	top.YesBid.Price = 1
	if book.YesBids[0].Price != 4001 {
		t.Fatal("Top must not mutate stored book")
	}
}

func TestTopMissingSides(t *testing.T) {
	top := (OrderBook{}).Top()
	if top.YesBid != nil || top.NoBid != nil || top.YesAsk != nil || top.NoAsk != nil {
		t.Fatal("empty book has quotes")
	}
	top = (OrderBook{YesBids: []PriceLevel{{Price: 0, Quantity: 1}}}).Top()
	if top.YesBid == nil || top.YesBid.Price != 0 || top.NoAsk.Price != Dollar || top.YesAsk != nil || top.NoBid != nil {
		t.Fatal("zero bid or absent side mishandled")
	}
	if got := top.NoAsk.Price.String(); got != "$1.0000" {
		t.Fatal(got)
	}
}
