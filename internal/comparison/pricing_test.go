package comparison

import (
	"encoding/json"
	"github.com/Vaibhav2430/Prediction-Market-Arbitrage/internal/kalshi"
	"github.com/Vaibhav2430/Prediction-Market-Arbitrage/internal/models"
	"math/big"
	"testing"
)

func TestExactCostAndFeeRounding(t *testing.T) {
	p := &models.TokenLevel{Price: 3000, Size: 10000000}
	k := &models.PriceLevel{Price: 6500, Quantity: 1000}
	s, err := Price("YES", "NO", p, k, 5000000, 5, Fees{PolyRate: big.NewRat(1, 100), KalshiRate: big.NewRat(7, 100)})
	if err != nil || s.Premium != "4.75000" || s.PolyFee != "0.01050" || s.KalshiFee != "0.07970" || s.Total != "4.84020" || s.ConditionalDifference != "0.15980" || s.Status != "conditional_only" {
		t.Fatalf("scenario=%+v err=%v", s, err)
	}
}
func TestSizeMissingLiquidityAndMissingFees(t *testing.T) {
	p := &models.TokenLevel{Price: 5000, Size: 4999999}
	k := &models.PriceLevel{Price: 5000, Quantity: 1000}
	fees := Fees{PolyRate: new(big.Rat), KalshiRate: new(big.Rat)}
	s, err := Price("YES", "NO", p, k, 1000000, 5, fees)
	if err != nil || s.Available != 4 || s.Total != "" {
		t.Fatalf("scenario=%+v err=%v", s, err)
	}
	p.Size = 10000000
	s, err = Price("YES", "NO", p, k, 5000000, 1, fees)
	if err != nil || s.Total != "" {
		t.Fatal("minimum order size ignored")
	}
	s, err = Price("YES", "NO", nil, k, 5000000, 5, fees)
	if err != nil || s.Total != "" {
		t.Fatal("missing liquidity became quote")
	}
	s, err = Price("YES", "NO", p, k, 5000000, 5, Fees{})
	if err != nil || s.Premium != "5.00000" || s.Total != "" || s.ConditionalDifference != "" {
		t.Fatal("missing fees must withhold net result")
	}
}
func TestResolveDynamicFeesAndUnknowns(t *testing.T) {
	enabled := true
	exponent := 1
	m := models.Market{FeesEnabled: &enabled, FeeRate: json.RawMessage(`0.01`), FeeExponent: &exponent}
	f := ResolveFees(m, kalshi.FeeInfo{Type: "quadratic_with_maker_fees", Multiplier: json.RawMessage(`2`)})
	if f.PolyRate.Cmp(big.NewRat(1, 100)) != 0 || f.KalshiRate.Cmp(big.NewRat(14, 100)) != 0 {
		t.Fatal(f)
	}
	f = ResolveFees(models.Market{}, kalshi.FeeInfo{})
	if f.PolyRate != nil || f.KalshiRate != nil || len(f.Warnings) == 0 {
		t.Fatal("unknown fees treated as zero")
	}
	enabled = false
	f = ResolveFees(m, kalshi.FeeInfo{Type: "quadratic", Multiplier: json.RawMessage(`0`)})
	if f.PolyRate.Sign() != 0 || f.KalshiRate.Sign() != 0 {
		t.Fatal("explicit zero fees not preserved")
	}
	for _, raw := range []string{`null`, `"NaN"`, `-1`, `"1/2"`, `true`} {
		if _, err := rate(json.RawMessage(raw)); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}
func TestQuantityBounds(t *testing.T) {
	for _, q := range []int64{0, 1000001} {
		if _, err := Price("YES", "NO", nil, nil, 1, q, Fees{}); err == nil {
			t.Fatal("invalid quantity accepted")
		}
	}
}
