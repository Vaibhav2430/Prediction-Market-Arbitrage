package models

import (
	"fmt"
	"time"
)

// Shares stores Polymarket share sizes in millionths of one share.
// Kalshi's separate Quantity type remains in hundredths of a contract.
type Shares int64

func (s Shares) String() string { return fmt.Sprintf("%d.%06d", s/1000000, s%1000000) }

type TokenLevel struct {
	Price Price
	Size  Shares
}

// TokenBook holds the actual bid and ask sides for one Polymarket asset.
// Bids are sorted descending, asks ascending. No complement is inferred.
type TokenBook struct {
	AssetID      string
	ConditionID  string
	Bids, Asks   []TokenLevel
	Timestamp    time.Time // API snapshot timestamp, distinct from local receipt.
	ReceivedAt   time.Time
	Hash         string
	TickSize     Price
	MinOrderSize Shares
	NegativeRisk bool
}

func (b TokenBook) BestBid() *TokenLevel {
	if len(b.Bids) == 0 {
		return nil
	}
	level := b.Bids[0]
	return &level
}
func (b TokenBook) BestAsk() *TokenLevel {
	if len(b.Asks) == 0 {
		return nil
	}
	level := b.Asks[0]
	return &level
}
