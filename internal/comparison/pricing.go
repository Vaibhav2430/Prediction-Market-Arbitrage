// Package comparison calculates conditional top-of-book costs, never trading
// signals. It does not prove outcome equivalence or guarantee fills/payouts.
package comparison

import (
	"encoding/json"
	"fmt"
	"math/big"
	"strings"

	"github.com/Vaibhav2430/Prediction-Market-Arbitrage/internal/kalshi"
	"github.com/Vaibhav2430/Prediction-Market-Arbitrage/internal/models"
)

type Fees struct {
	PolyRate, KalshiRate *big.Rat
	Warnings             []string
}

func rate(raw json.RawMessage) (*big.Rat, error) {
	s := strings.TrimSpace(string(raw))
	if strings.HasPrefix(s, "\"") {
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, err
		}
	}
	if len(s) == 0 || len(s) > 40 {
		return nil, fmt.Errorf("missing fee parameter")
	}
	for _, ch := range s {
		if (ch < '0' || ch > '9') && ch != '.' {
			return nil, fmt.Errorf("invalid fee parameter")
		}
	}
	n, ok := new(big.Rat).SetString(s)
	if !ok || n.Sign() < 0 || n.Cmp(big.NewRat(100, 1)) > 0 {
		return nil, fmt.Errorf("invalid fee parameter")
	}
	return n, nil
}
func ResolveFees(m models.Market, k kalshi.FeeInfo) Fees {
	f := Fees{Warnings: []string{}}
	if m.FeesEnabled == nil {
		f.Warnings = append(f.Warnings, "Polymarket feesEnabled missing; net cost unavailable.")
	} else if !*m.FeesEnabled {
		f.PolyRate = new(big.Rat)
	} else if m.FeeExponent == nil || *m.FeeExponent != 1 {
		f.Warnings = append(f.Warnings, "Unsupported/missing Polymarket fee exponent; net cost unavailable.")
	} else {
		r, err := rate(m.FeeRate)
		if err != nil || r.Cmp(big.NewRat(1, 1)) > 0 {
			f.Warnings = append(f.Warnings, "Polymarket fee rate unavailable/unsupported.")
		} else {
			f.PolyRate = r
		}
	}
	if k.Type != "quadratic" && k.Type != "quadratic_with_maker_fees" {
		f.Warnings = append(f.Warnings, "Unsupported/missing Kalshi fee type; net cost unavailable.")
	} else {
		multiplier, err := rate(k.Multiplier)
		if err != nil {
			f.Warnings = append(f.Warnings, "Kalshi fee multiplier missing/invalid.")
		} else {
			f.KalshiRate = new(big.Rat).Mul(big.NewRat(7, 100), multiplier)
		}
	}
	return f
}

type Scenario struct {
	PolymarketSide        string `json:"polymarket_side"`
	KalshiSide            string `json:"kalshi_side"`
	Status                string `json:"status"`
	Requested             int64  `json:"requested_whole_units"`
	Available             int64  `json:"max_whole_units_at_top"`
	PolyAsk               string `json:"polymarket_ask"`
	KalshiAsk             string `json:"kalshi_ask"`
	Premium               string `json:"premium_usd,omitempty"`
	PolyFee               string `json:"polymarket_fee_estimate_usd,omitempty"`
	KalshiFee             string `json:"kalshi_fee_estimate_usd,omitempty"`
	Total                 string `json:"total_estimate_usd,omitempty"`
	ConditionalPayout     string `json:"conditional_payout_usd,omitempty"`
	ConditionalDifference string `json:"conditional_payout_minus_cost_usd,omitempty"`
	Reason                string `json:"reason,omitempty"`
}

// Price uses only displayed best asks and whole matched units. Estimated fees
// are rounded upward (Polymarket 0.00001 USD, Kalshi 0.0001 USD) per aggregate
// leg. Actual fill splitting/rounding may differ. No rebates are assumed.
func Price(polySide, kalSide string, p *models.TokenLevel, k *models.PriceLevel, minSize models.Shares, quantity int64, fees Fees) (Scenario, error) {
	s := Scenario{PolymarketSide: polySide, KalshiSide: kalSide, Requested: quantity, Status: "unavailable", PolyAsk: "unavailable", KalshiAsk: "unavailable"}
	if quantity < 1 || quantity > 1000000 {
		return s, fmt.Errorf("quantity must be 1..1000000 whole units")
	}
	if p != nil {
		s.PolyAsk = p.Price.String()
	}
	if k != nil {
		s.KalshiAsk = k.Price.String()
	}
	if p == nil || k == nil {
		s.Reason = "Missing ask liquidity"
		return s, nil
	}
	if p.Price < 0 || p.Price > models.Dollar || k.Price < 0 || k.Price > models.Dollar || p.Size <= 0 || k.Quantity <= 0 || minSize <= 0 {
		return s, fmt.Errorf("invalid quote or minimum size")
	}
	s.PolyAsk, s.KalshiAsk = p.Price.String(), k.Price.String()
	s.Available = int64(p.Size) / 1000000
	if q := int64(k.Quantity) / 100; q < s.Available {
		s.Available = q
	}
	if quantity > s.Available {
		s.Reason = "Requested size exceeds displayed top-of-book quantity; depth is not consumed"
		return s, nil
	}
	if quantity*1000000 < int64(minSize) {
		s.Reason = "Requested Polymarket size is below the minimum order size"
		return s, nil
	}
	premium := big.NewRat(quantity*(int64(p.Price)+int64(k.Price)), 10000)
	s.Premium = premium.FloatString(5)
	s.ConditionalPayout = big.NewRat(quantity, 1).FloatString(5)
	if fees.PolyRate == nil || fees.KalshiRate == nil {
		s.Reason = "Fee parameters incomplete; net result withheld"
		return s, nil
	}
	fee := func(price models.Price, rate *big.Rat, scale int64) *big.Rat {
		v := big.NewRat(quantity*int64(price)*(10000-int64(price)), 100000000)
		v.Mul(v, rate)
		v.Mul(v, big.NewRat(scale, 1))
		ticks := new(big.Int).Quo(v.Num(), v.Denom())
		if new(big.Int).Mod(v.Num(), v.Denom()).Sign() != 0 {
			ticks.Add(ticks, big.NewInt(1))
		}
		return new(big.Rat).SetFrac(ticks, big.NewInt(scale))
	}
	pf, kf := fee(p.Price, fees.PolyRate, 100000), fee(k.Price, fees.KalshiRate, 10000)
	total := new(big.Rat).Add(premium, pf)
	total.Add(total, kf)
	difference := new(big.Rat).Sub(big.NewRat(quantity, 1), total)
	s.PolyFee, s.KalshiFee, s.Total = pf.FloatString(5), kf.FloatString(5), total.FloatString(5)
	s.ConditionalDifference = difference.FloatString(5)
	s.Status = "conditional_only"
	s.Reason = "Payout assumes the stated outcome relation holds and both legs fill; contract equivalence is unverified"
	return s, nil
}
