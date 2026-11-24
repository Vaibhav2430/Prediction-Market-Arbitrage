// Package models contains read-only prediction market data.
package models

import "strings"

// Outcome links a label to Gamma's indicative price and its trading asset ID.
// TokenID is a CTF token ID or, for V2 markets, a position ID.
// A nil price means unavailable; zero is a valid price. No executable quotes
// or order-book depth are represented here.
type Outcome struct {
	Name            string
	IndicativePrice *float64
	TokenID         string
}

type Market struct {
	ID          string
	Question    string
	Slug        string
	ConditionID string
	Outcomes    []Outcome
}

// YesNo returns prices by outcome label, never by assumed array position.
// binary is false for markets that do not contain exactly Yes and No.
func (m Market) YesNo() (yes, no *float64, binary bool) {
	y, n, binary := m.YesNoOutcomes()
	return y.IndicativePrice, n.IndicativePrice, binary
}

// YesNoOutcomes selects entire outcomes by label, keeping asset IDs aligned.
func (m Market) YesNoOutcomes() (yes, no Outcome, binary bool) {
	if len(m.Outcomes) != 2 {
		return yes, no, false
	}
	seenYes, seenNo := false, false
	for _, outcome := range m.Outcomes {
		switch strings.ToLower(strings.TrimSpace(outcome.Name)) {
		case "yes":
			yes, seenYes = outcome, true
		case "no":
			no, seenNo = outcome, true
		default:
			return Outcome{}, Outcome{}, false
		}
	}
	return yes, no, seenYes && seenNo
}
