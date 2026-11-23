// Package models contains read-only prediction market data.
package models

import "strings"

// Outcome links a label to Gamma's indicative price and its CLOB token ID.
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
	if len(m.Outcomes) != 2 {
		return nil, nil, false
	}
	seenYes, seenNo := false, false
	for _, outcome := range m.Outcomes {
		switch strings.ToLower(strings.TrimSpace(outcome.Name)) {
		case "yes":
			yes, seenYes = outcome.IndicativePrice, true
		case "no":
			no, seenNo = outcome.IndicativePrice, true
		default:
			return nil, nil, false
		}
	}
	return yes, no, seenYes && seenNo
}
