package mcp

import (
	"net/http"
	"strings"
	"testing"
)

// The owner's own visits (SIM-151) reach the agent in text only: the chat
// apps cache each tool's output schema and reject keys it does not list.
func TestOwnerViewsAreTextOnly(t *testing.T) {
	split := func(owner string) string {
		return `{"person":{"views":5,"visitors":2},"bot":{"views":9,"visitors":3},"infra":{"views":1,"visitors":1},"owner":` + owner + `,"unknown":{"views":0,"visitors":0}}`
	}
	ownerTotals := `{"views":4,"visitors":1}`
	s := newTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/sites":
			w.Write([]byte(`[{"name":"menu","site_url":"https://menu.ann.simple-host.app/","active_version":1,"views":{"people_7_days":2,"people_30_days":9}},` +
				`{"name":"draft","site_url":"https://draft.ann.simple-host.app/","active_version":0,"views":{"people_7_days":0,"people_30_days":0}}]`))
		case "/v1/sites/menu/analytics":
			w.Write([]byte(`{"range_days":7,"totals":` + split(ownerTotals) + `,"last_24h":` + split(`{"views":0,"visitors":0}`) + `,"daily":[],"hourly":[]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"error":"not_found"}`))
		}
	}))

	text, out, isErr := resultOf(t, send(t, s, toolCall("list_sites", map[string]any{}), nil, true))
	if isErr {
		t.Fatal(text)
	}
	want := "\n\nViews by people, not counting your own visits (last 7 days / last 30 days): menu 2 / 9; draft 0 / 0." + ownVisitsCaveat
	if !strings.HasSuffix(text, want) {
		t.Errorf("list_sites text:\n%s", text)
	}
	for _, item := range out["sites"].([]any) {
		if _, ok := item.(map[string]any)["views"]; ok {
			t.Error("list_sites put views in the structured result")
		}
	}

	text, out, isErr = resultOf(t, send(t, s, toolCall("get_site", map[string]any{"site": "menu"}), nil, true))
	if isErr {
		t.Fatal(text)
	}
	if !strings.HasSuffix(text, "\n\nViews by people, not counting your own visits: 2 in the last 7 days, 9 in the last 30 days."+ownVisitsCaveat) {
		t.Errorf("get_site text:\n%s", text)
	}
	if _, ok := out["views"]; ok {
		t.Error("get_site put views in the structured result")
	}

	text, out, isErr = resultOf(t, send(t, s, toolCall("site_analytics", map[string]any{"site": "menu", "days": 7}), nil, true))
	if isErr {
		t.Fatal(text)
	}
	if !strings.HasSuffix(text, "\n\nYour own visits, left out of person: 4 views from 1 of your addresses in this window."+ownVisitsCaveat) {
		t.Errorf("site_analytics text:\n%s", text)
	}
	for _, key := range []string{"totals", "last_24h"} {
		m := out[key].(map[string]any)
		if _, ok := m["owner"]; ok {
			t.Errorf("%s still carries owner", key)
		}
		for _, class := range []string{"person", "bot", "infra", "unknown"} {
			if _, ok := m[class]; !ok {
				t.Errorf("%s lost %s", key, class)
			}
		}
	}
	if strings.Contains(text[:strings.Index(text, "Your own visits")], `"owner"`) {
		t.Error("site_analytics JSON text still carries owner")
	}

	// Nothing from the owner: the sentence still says so, with zero.
	s = newTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"range_days":30,"totals":` + split(`{"views":0,"visitors":0}`) + `,"last_24h":` + split(`{"views":0,"visitors":0}`) + `,"daily":[],"hourly":[]}`))
	}))
	text, _, isErr = resultOf(t, send(t, s, toolCall("site_analytics", map[string]any{"site": "menu"}), nil, true))
	if isErr || !strings.HasSuffix(text, "\n\nYour own visits, left out of person: 0 views in this window."+ownVisitsCaveat) {
		t.Errorf("site_analytics text with no owner visits:\n%s", text)
	}
}
