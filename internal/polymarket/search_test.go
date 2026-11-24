package polymarket

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTopicSearchUsesRemoteQueryAndValidatesNestedMarkets(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.Method != "GET" || r.URL.Path != "/public-search" || q.Get("q") != "bitcoin & ether" || q.Get("page") != "2" || q.Get("limit_per_type") != "5" || q.Get("events_status") != "active" || q.Get("search_profiles") != "false" || q.Get("keep_closed_markets") != "0" {
			t.Errorf("bad request %s %s", r.Method, r.URL)
		}
		fmt.Fprintf(w, `{"events":[{"title":"Bitcoin this month","markets":[%s,%s,%s,{"bad":true}]}],"pagination":{"hasMore":true}}`, record(nil), record(nil), record(map[string]any{"closed": true}))
	}))
	defer server.Close()
	c := &Client{baseURL: server.URL, httpClient: server.Client()}
	page, err := c.SearchActiveMarkets(context.Background(), "bitcoin & ether", 5, 2)
	if err != nil || !page.HasMore || page.Events != 1 || len(page.Markets) != 1 || len(page.Warnings) != 1 || page.Markets[0].EventTitles[0] != "Bitcoin this month" {
		t.Fatalf("page=%+v err=%v", page, err)
	}
}
func TestTopicSearchFailuresAndEmptyResults(t *testing.T) {
	for _, tt := range []struct {
		body    string
		status  int
		wantErr bool
	}{
		{`{"events":null,"pagination":{"hasMore":false}}`, 200, false},
		{`{"events":[],"pagination":{"hasMore":false}}`, 200, false},
		{`{}`, 200, true}, {`[]`, 200, true}, {`{`, 200, true},
		{`{"events":[],"pagination":{}}`, 200, true},
		{`{"events":{},"pagination":{"hasMore":true}}`, 200, true},
		{"", 429, true}, {strings.Repeat(" ", maxResponseBytes+1), 200, true},
	} {
		t.Run(fmt.Sprint(tt.status, len(tt.body)), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tt.status); fmt.Fprint(w, tt.body) }))
			defer server.Close()
			c := &Client{baseURL: server.URL, httpClient: server.Client()}
			_, err := c.SearchActiveMarkets(context.Background(), "bitcoin", 5, 1)
			if (err != nil) != tt.wantErr {
				t.Fatal(err)
			}
		})
	}
}
func TestTopicSearchValidation(t *testing.T) {
	for _, tt := range []struct {
		topic       string
		limit, page int
	}{{"", 5, 1}, {"bitcoin", 21, 1}, {"bitcoin", 5, 0}, {"bitcoin", 5, 101}} {
		if _, err := NewClient().SearchActiveMarkets(context.Background(), tt.topic, tt.limit, tt.page); err == nil {
			t.Fatal("invalid request accepted")
		}
	}
}
