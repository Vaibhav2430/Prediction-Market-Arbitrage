package kalshi

import (
	"context"
	"fmt"
	"net/url"
)

type SettlementSource struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}
type RuleSources struct {
	EventTicker   string             `json:"event_ticker"`
	SeriesTicker  string             `json:"series_ticker"`
	TermsURL      string             `json:"terms_url"`
	EventSources  []SettlementSource `json:"event_sources"`
	SeriesSources []SettlementSource `json:"series_sources"`
}

// FetchRuleSources preserves source identities and the linked binding terms.
// Prices, volume, fee schedules and administrative update timestamps are omitted.
func (c *Client) FetchRuleSources(ctx context.Context, eventTicker string) (RuleSources, error) {
	var zero RuleSources
	if err := validateTicker(eventTicker); err != nil {
		return zero, err
	}
	var ew struct {
		Event *struct {
			Ticker  string             `json:"event_ticker"`
			Series  string             `json:"series_ticker"`
			Sources []SettlementSource `json:"settlement_sources"`
		} `json:"event"`
	}
	if err := c.get(ctx, "/events/"+url.PathEscape(eventTicker), nil, &ew); err != nil {
		return zero, err
	}
	if ew.Event == nil || ew.Event.Ticker != eventTicker {
		return zero, fmt.Errorf("missing/mismatched event rule sources")
	}
	if err := validateTicker(ew.Event.Series); err != nil {
		return zero, err
	}
	var sw struct {
		Series *struct {
			Ticker   string             `json:"ticker"`
			TermsURL string             `json:"contract_terms_url"`
			Sources  []SettlementSource `json:"settlement_sources"`
		} `json:"series"`
	}
	if err := c.get(ctx, "/series/"+url.PathEscape(ew.Event.Series), nil, &sw); err != nil {
		return zero, err
	}
	if sw.Series == nil || sw.Series.Ticker != ew.Event.Series || sw.Series.TermsURL == "" {
		return zero, fmt.Errorf("missing/mismatched series terms")
	}
	return RuleSources{EventTicker: eventTicker, SeriesTicker: sw.Series.Ticker, TermsURL: sw.Series.TermsURL, EventSources: ew.Event.Sources, SeriesSources: sw.Series.Sources}, nil
}
