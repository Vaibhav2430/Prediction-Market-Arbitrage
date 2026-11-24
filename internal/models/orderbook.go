package models

import (
	"fmt"
	"time"
)

// Price is an exact USD amount in ten-thousandths of a dollar.
// Binary contract prices range from 0 to Dollar inclusive.
type Price int64

const Dollar Price = 10000

func (p Price) String() string { return fmt.Sprintf("$%d.%04d", p/10000, p%10000) }

// Quantity is an exact contract count in hundredths of a contract.
type Quantity int64

func (q Quantity) String() string { return fmt.Sprintf("%d.%02d", q/100, q%100) }

type PriceLevel struct {
	Price    Price
	Quantity Quantity
}

// OrderBook holds validated bid levels sorted best first. ReceivedAt records
// local receipt, not exchange publication time or a freshness guarantee.
type OrderBook struct {
	Ticker     string
	YesBids    []PriceLevel
	NoBids     []PriceLevel
	ReceivedAt time.Time
}

type TopOfBook struct {
	YesBid, YesAsk, NoBid, NoAsk *PriceLevel
}

// Top derives asks from opposite-side bids for $1 binary contracts, preserving
// the available contract count. A missing side stays unavailable.
func (b OrderBook) Top() TopOfBook {
	var top TopOfBook
	if len(b.YesBids) > 0 {
		bid := b.YesBids[0]
		ask := PriceLevel{Price: Dollar - bid.Price, Quantity: bid.Quantity}
		top.YesBid, top.NoAsk = &bid, &ask
	}
	if len(b.NoBids) > 0 {
		bid := b.NoBids[0]
		ask := PriceLevel{Price: Dollar - bid.Price, Quantity: bid.Quantity}
		top.NoBid, top.YesAsk = &bid, &ask
	}
	return top
}
