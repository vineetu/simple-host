package mcp

import (
	"net/http"
	"net/url"
	"reflect"
	"testing"
)

func TestListSitesFilters(t *testing.T) {
	for _, tt := range []struct {
		name string
		args map[string]any
		want url.Values
	}{
		{"no arguments", map[string]any{}, url.Values{}},
		{"all filters", map[string]any{"query": "Real estate & homes", "created_after": "2026-10-20", "created_before": "2026-10-21", "updated_after": "2026-10-22T01:02:03+02:00", "updated_before": "2026-10-23", "limit": 12, "offset": 3}, url.Values{"query": {"Real estate & homes"}, "created_after": {"2026-10-20"}, "created_before": {"2026-10-21"}, "updated_after": {"2026-10-22T01:02:03+02:00"}, "updated_before": {"2026-10-23"}, "limit": {"12"}, "offset": {"3"}}},
		{"empty query", map[string]any{"query": ""}, url.Values{"query": {""}}},
		{"large limit", map[string]any{"limit": 1000000}, url.Values{"limit": {"1000000"}}},
		{"zero limit", map[string]any{"limit": 0}, url.Values{"limit": {"0"}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			s := newTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "GET" || r.URL.Path != "/v1/sites" || !reflect.DeepEqual(r.URL.Query(), tt.want) {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
				}
				if len(tt.args) == 0 && r.URL.RequestURI() != "/v1/sites" {
					t.Errorf("no-args URI changed: %s", r.URL)
				}
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`[{"name":"homes","site_url":"https://example.com/","active_version":1,"summary":{"title":"Homes","description":"Real estate","headings":"For sale","text":"Browse listings"}}]`))
			}))
			text, out, isErr := resultOf(t, send(t, s, toolCall("list_sites", tt.args), nil, true))
			if isErr || calls != 1 {
				t.Fatalf("calls=%d: %s", calls, text)
			}
			fields := out["sites"].([]any)[0].(map[string]any)["summary"]
			want := map[string]any{"title": "Homes", "description": "Real estate", "headings": "For sale", "text": "Browse listings"}
			if !reflect.DeepEqual(fields, want) {
				t.Errorf("summary=%v", fields)
			}
			for _, tool := range Tools() {
				if tool.Name == "list_sites" {
					if err := ValidateOutput(tool.OutputSchema, out, nil); err != nil {
						t.Fatal(err)
					}
				}
			}
		})
	}
}

func TestRestSiteSummaryOmitsEmptyFields(t *testing.T) {
	var site restSite
	if _, ok := site.summary()["summary"]; ok {
		t.Fatal("empty summary included")
	}
}
