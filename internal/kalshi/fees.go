package kalshi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

// FeeInfo preserves public series parameters and any event override. A missing
// or unsupported fee type/multiplier is not interpreted as a zero fee.
type FeeInfo struct {
	SeriesTicker string          `json:"series_ticker"`
	Type         string          `json:"type"`
	Multiplier   json.RawMessage `json:"multiplier"`
}

func (c *Client) FetchFeeInfo(ctx context.Context, eventTicker string) (FeeInfo, error) {
	var zero FeeInfo
	if err := validateTicker(eventTicker); err != nil {
		return zero, err
	}
	var eventWire struct {
		Event *struct {
			Ticker     string          `json:"event_ticker"`
			Series     string          `json:"series_ticker"`
			FeeType    *string         `json:"fee_type_override"`
			Multiplier json.RawMessage `json:"fee_multiplier_override"`
		} `json:"event"`
	}
	if err := c.get(ctx, "/events/"+url.PathEscape(eventTicker), nil, &eventWire); err != nil {
		return zero, err
	}
	if eventWire.Event == nil || eventWire.Event.Ticker != eventTicker {
		return zero, fmt.Errorf("missing/mismatched Kalshi event")
	}
	event := eventWire.Event
	if err := validateTicker(event.Series); err != nil {
		return zero, err
	}
	var seriesWire struct {
		Series *struct {
			Ticker     string          `json:"ticker"`
			Type       string          `json:"fee_type"`
			Multiplier json.RawMessage `json:"fee_multiplier"`
		} `json:"series"`
	}
	if err := c.get(ctx, "/series/"+url.PathEscape(event.Series), nil, &seriesWire); err != nil {
		return zero, err
	}
	if seriesWire.Series == nil || seriesWire.Series.Ticker != event.Series {
		return zero, fmt.Errorf("missing/mismatched Kalshi series")
	}
	result := FeeInfo{SeriesTicker: event.Series, Type: seriesWire.Series.Type, Multiplier: seriesWire.Series.Multiplier}
	if event.FeeType != nil {
		result.Type = *event.FeeType
	}
	if len(event.Multiplier) > 0 && string(event.Multiplier) != "null" {
		result.Multiplier = event.Multiplier
	}
	return result, nil
}
