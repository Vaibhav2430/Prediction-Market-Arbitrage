package kalshi

import (
	"context"
	"fmt"
	"net/http"
	"testing"
)

func TestFeeInfoAppliesEventOverrides(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/events/EVENT":
			fmt.Fprint(w, `{"event":{"event_ticker":"EVENT","series_ticker":"SERIES","fee_type_override":"quadratic","fee_multiplier_override":2}}`)
		case "/series/SERIES":
			fmt.Fprint(w, `{"series":{"ticker":"SERIES","fee_type":"quadratic_with_maker_fees","fee_multiplier":1}}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	})
	f, err := c.FetchFeeInfo(context.Background(), "EVENT")
	if err != nil || f.Type != "quadratic" || string(f.Multiplier) != "2" {
		t.Fatalf("fees=%+v err=%v", f, err)
	}
}
func TestFeeInfoRejectsMissingEvent(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{}`) })
	if _, err := c.FetchFeeInfo(context.Background(), "EVENT"); err == nil {
		t.Fatal("missing event accepted")
	}
}
