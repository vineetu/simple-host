package handler

import (
	"context"
	"encoding/csv"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	db "github.com/vsriram/simple-host/internal/db"
)

func TestRequestArrival(t *testing.T) {
	r := httptest.NewRequest("POST", "/v1/auth/verify?from=vercel-ship&utm_source=poll&utm_campaign=%3Cscript%3E&referrer=ship.vineetsriram.com", nil)
	d, ok := requestArrival(r)
	if !ok || d.From != "vercel-ship" || d.UTMSource != "poll" || d.UTMCampaign != "script" || d.Referrer != "ship.vineetsriram.com" || d.LandedAt.IsZero() {
		t.Fatalf("detail: %+v %v", d, ok)
	}
	for _, bad := range []string{"", "%3Cscript%3E", "-x", strings.Repeat("a", 41)} {
		r := httptest.NewRequest("POST", "/v1/auth/verify?from="+bad, nil)
		if _, ok := requestArrival(r); ok {
			t.Errorf("accepted %q", bad)
		}
	}
	for _, ref := range []string{"10.0.0.1", "simple-host.app"} {
		r := httptest.NewRequest("POST", "https://simple-host.app/v1/auth/verify?from=x&referrer="+ref, nil)
		if d, _ := requestArrival(r); d.Referrer != "" {
			t.Errorf("kept referrer %s", ref)
		}
	}
	r = httptest.NewRequest("POST", "/v1/auth/verify", strings.NewReader("from=agentcon&utm_medium=web"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if d, ok := requestArrival(r); !ok || d.From != "agentcon" || d.UTMMedium != "web" {
		t.Fatalf("form: %+v", d)
	}
}

// Same-visit carried labels; a new visit has no remembered attribution.
func TestArrivalAttribution(t *testing.T) {
	a := newPrivateApp(t)
	chatgpt := a.registerNamedClient(t, "ChatGPT")
	fresh := "from=vercel-ship&utm_source=poll&referrer=ship.vineetsriram.com"
	unnamed := "from=hn-launch"

	cases := []struct {
		label, query, client, wantSource, wantAgent string
		detail                                      bool
	}{
		{"direct", "", "", "website", "", false},
		{"plain-from", "from=artifact-hub", "", "artifact-hub", "", true},
		{"same-visit", fresh, "", "vercel-ship", "", true},
		{"new-visit", "", "", "website", "", false},
		{"connector", fresh, chatgpt, "vercel-ship", "ChatGPT", true},
		{"connector-plain", "", chatgpt, "connector:ChatGPT", "", false},
		{"unnamed", unnamed, "", "from:hn-launch", "", true},
	}
	for _, c := range cases {
		addr, code := newSignupAddr(t, a, "arr-"+c.label)
		body := map[string]any{"email": addr, "code": code}
		if c.client != "" {
			body["signup_client"] = c.client
		}
		headers := map[string]string{"Sec-Fetch-Site": "same-origin"}

		r := a.at(t, "POST", a.appHost(), "/v1/auth/verify?"+c.query, body, headers)
		if r.status != 200 || r.json(t)["created"] != true {
			t.Fatalf("%s: verify %d %s", c.label, r.status, r.body)
		}
		got := signupOf(t, a, addr)
		if got.source.String != c.wantSource || got.agent.String != c.wantAgent {
			t.Errorf("%s: source %q agent %q", c.label, got.source.String, got.agent.String)
		}
		// The admin source view still exposes the stored label and named sources.
		admin := a.at(t, "GET", a.appHost(), "/v1/admin/users", nil, map[string]string{"X-API-Key": a.admin})
		if admin.status != 200 || !strings.Contains(string(admin.body), c.wantSource) || !strings.Contains(string(admin.body), "Artifact Hub") {
			t.Fatalf("%s admin source view: %d", c.label, admin.status)
		}

		var detail []byte
		if err := a.database.QueryRow(`SELECT signup_detail FROM users WHERE username = $1`, addr).Scan(&detail); err != nil {
			t.Fatal(err)
		}
		if (len(detail) > 0) != c.detail {
			t.Errorf("%s: detail %s", c.label, detail)
		}
		if c.label == "same-visit" && (!strings.Contains(string(detail), `"utm_source": "poll"`) || !strings.Contains(string(detail), `"referrer": "ship.vineetsriram.com"`)) {
			t.Errorf("same-visit detail: %s", detail)
		}
	}

	// An agent's API call never takes the browser's link, even with carried labels.
	addr, code := newSignupAddr(t, a, "arr-agent")
	if r := a.at(t, "POST", a.appHost(), "/v1/auth/verify?"+fresh, map[string]any{"email": addr, "code": code}, map[string]string{"User-Agent": "curl/8"}); r.status != 200 {
		t.Fatalf("agent: %d %s", r.status, r.body)
	}
	if got := signupOf(t, a, addr); got.source.String != "agent" {
		t.Errorf("agent sign-up credited to the link: %+v", got)
	}
}

// GET /v1/admin/signups and .csv: admin only (404 for anyone else), the
// group's accounts with what they did after joining, and a funnel.
func TestAdminSignupsDrillDown(t *testing.T) {
	a := newPersonApp(t, "serve")
	ctx := context.Background()
	src := "from:t" + strconv.FormatInt(time.Now().UnixNano()%1e9, 36)
	active, idle := a.newPerson(t, "coact"), a.newPerson(t, "coidle")
	activeID, _ := a.userID(t, active)
	idleID, _ := a.userID(t, idle)
	for _, id := range []string{activeID, idleID} {
		if err := db.SetSignup(ctx, a.database, id, db.Signup{Source: src, Method: "email",
			Detail: &db.SignupDetail{From: strings.TrimPrefix(src, "from:"), UTMCampaign: "=HYPERLINK(\"x\")", Referrer: "ship.vineetsriram.com", LandedAt: time.Now().Add(-time.Hour)}}); err != nil {
			t.Fatal(err)
		}
	}
	a.deploy(t, active, "shop")
	siteID := a.siteID(t, active, "shop")
	client := a.registerNamedClient(t, "Claude")
	if _, err := a.database.Exec(`INSERT INTO oauth_grants (user_id, client_id, scope, resource) VALUES ($1, $2, 'sites', 'x')`, activeID, client); err != nil {
		t.Fatal(err)
	}
	if _, err := a.database.Exec(`INSERT INTO site_view_hourly (site_id, hour, class, views) VALUES ($1, date_trunc('hour', now()), 'person', 7), ($1, date_trunc('hour', now()), 'bot', 50)`, siteID); err != nil {
		t.Fatal(err)
	}

	path := "/v1/admin/signups?group=" + url.QueryEscape(src)
	for _, p := range []string{path, "/v1/admin/signups.csv?group=" + url.QueryEscape(src)} {
		if r := a.at(t, "GET", signupApex, p, nil, nil); r.status != 401 {
			t.Errorf("%s without a key: %d", p, r.status)
		}
		if r := a.at(t, "GET", signupApex, p, nil, map[string]string{"X-API-Key": active.key}); r.status != 404 || strings.Contains(string(r.body), idle.email) {
			t.Errorf("%s as a non-admin: %d %s", p, r.status, r.body)
		}
	}
	if r := a.at(t, "GET", signupApex, "/v1/admin/signups", nil, map[string]string{"X-API-Key": a.admin}); r.status != 400 {
		t.Errorf("no group: %d", r.status)
	}

	r := a.at(t, "GET", signupApex, path, nil, map[string]string{"X-API-Key": a.admin})
	if r.status != 200 {
		t.Fatalf("admin: %d %s", r.status, r.body)
	}
	out := r.json(t)
	f := out["funnel"].(map[string]any)
	if f["signed_up"] != 2.0 || f["connected_ai"] != 1.0 || f["published"] != 1.0 || f["got_visitors"] != 1.0 {
		t.Fatalf("funnel: %v", f)
	}
	accounts := out["accounts"].([]any)
	if len(accounts) != 2 || accounts[0].(map[string]any)["email"] != idle.email {
		t.Fatalf("accounts (newest first): %s", r.body)
	}
	got := accounts[1].(map[string]any)
	apps := got["connected_apps"].([]any)
	if got["email"] != active.email || len(apps) != 1 || apps[0].(map[string]any)["name"] != "Claude" || got["first_connect_at"] == nil ||
		got["sites_published"] != 1.0 || got["first_publish_at"] == nil || got["visits"] != 7.0 || got["last_active_at"] == nil {
		t.Fatalf("activation: %v", got)
	}
	if d := got["signup_detail"].(map[string]any); d["referrer"] != "ship.vineetsriram.com" {
		t.Fatalf("detail: %v", d)
	}

	// The admin users list carries the group and the labels.
	r = a.at(t, "GET", signupApex, "/v1/admin/users", nil, map[string]string{"X-API-Key": a.admin})
	var labelled bool
	for _, g := range r.json(t)["signup_groups"].([]any) {
		if g.(map[string]any)["key"] == src {
			labelled = true
		}
	}
	if !labelled || !strings.Contains(string(r.body), `"signup_group":"`+src+`"`) {
		t.Fatalf("users lack the group: %s", r.body[:min(len(r.body), 400)])
	}

	// CSV: a formula in a link parameter is neutralised like other exports.
	r = a.at(t, "GET", signupApex, "/v1/admin/signups.csv?group="+url.QueryEscape(src), nil, map[string]string{"X-API-Key": a.admin})
	if r.status != 200 || !strings.HasPrefix(r.header.Get("Content-Type"), "text/csv") || r.header.Get("Cache-Control") != "no-store" {
		t.Fatalf("csv: %d %v", r.status, r.header)
	}
	rows, err := csv.NewReader(strings.NewReader(string(r.body))).ReadAll()
	if err != nil || len(rows) != 3 {
		t.Fatalf("csv rows: %v %d", err, len(rows))
	}
	for _, row := range rows[1:] {
		if row[8] != `'=HYPERLINK("x")` {
			t.Errorf("utm_campaign cell not neutralised: %q", row[8])
		}
	}
}
