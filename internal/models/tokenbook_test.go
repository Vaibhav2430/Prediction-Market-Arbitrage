package models

import "testing"

func TestTokenBookMissingSides(t *testing.T) {
	book := TokenBook{}
	if book.BestBid() != nil || book.BestAsk() != nil {
		t.Fatal("empty sides have quotes")
	}
	book.Bids = []TokenLevel{{Price: 0, Size: 1}}
	if book.BestBid() == nil || book.BestBid().Price != 0 || book.BestAsk() != nil {
		t.Fatal("must not infer asks from bids")
	}
	book.BestBid().Price = 9000
	if book.Bids[0].Price != 0 {
		t.Fatal("getter mutated book")
	}
	if got := (Shares(1234567)).String(); got != "1.234567" {
		t.Fatal(got)
	}
}
