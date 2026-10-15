package handler

import (
	"context"
	"encoding/csv"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	db "github.com/vsriram/simple-host/internal/db"
)

// landing is the arrival cookie a browser gets from the home page at
// target (path and query), with the given Referer, as a Cookie header value.
func landing(t *testing.T, target, referer string, existing *http.Cookie) (*http.Cookie, string) {
	t.Helper()
	r := httptest.NewRequest("GET", "https://simple-host.app"+target, nil)
	r.Header.Set("X-Forwarded-Proto", "https")
	if referer != "" {
		r.Header.Set("Referer", referer)
	}
	if existing != nil {
		r.AddCookie(existing)
	}
	rec := httptest.NewRecorder()
	setArrivalCookie(rec, r)
	for _, c := range rec.Result().Cookies() {
		if c.Name == arrivalCookie {
			return c, c.Name + "=" + c.Value
		}
	}
	return nil, ""
}

// The arrival cookie: first-party, __Host- (so no sibling host can plant
// it), HttpOnly, Lax, Secure, 30 days, first touch wins; it keeps from, utm_* and the linking host only.
func TestArrivalCookie(t *testing.T) {
	c, _ := landing(t, "/?from=vercel-ship&utm_source=poll&utm_medium=web&utm_campaign=%3Dcmd()", "https://ship.vineetsriram.com/results?email=a@b.c", nil)
	if c == nil {
		t.Fatal("no cookie")
	}
	if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode || c.Domain != "" || c.Path != "/" || c.MaxAge != 30*24*3600 {
		t.Fatalf("cookie attributes: %+v", c)
	}
	v, _ := url.ParseQuery(c.Value)
	if v.Get("f") != "vercel-ship" || v.Get("us") != "poll" || v.Get("um") != "web" || v.Get("uc") != "=cmd()" || v.Get("r") != "ship.vineetsriram.com" {
		t.Fatalf("cookie value: %v", v)
	}
	if strings.Contains(c.Value, "email") || strings.Contains(c.Value, "results") {
		t.Fatalf("referrer path or query kept: %s", c.Value)
	}

	// A second link in the window does not take the credit.
	if c2, _ := landing(t, "/?from=artifact-hub", "", c); c2 != nil {
		t.Fatalf("second landing replaced the first: %+v", c2)
	}
	// Any well-formed from value counts; anything else sets nothing.
	if c3, _ := landing(t, "/?from=hn-launch", "", nil); c3 == nil {
		t.Fatal("unnamed from value not remembered")
	}
	for _, bad := range []string{"/", "/?from=", "/?from=%3Cscript%3E", "/?from=-x", "/?from=" + strings.Repeat("a", 41)} {
		if c4, _ := landing(t, bad, "", nil); c4 != nil {
			t.Errorf("%s set %+v", bad, c4)
		}
	}
	// The site's own address is not a referrer; an IP never is.
	for _, ref := range []string{"https://simple-host.app/pricing", "http://10.0.0.1/x"} {
		c5, _ := landing(t, "/?from=x1", ref, nil)
		if v, _ := url.ParseQuery(c5.Value); v.Get("r") != "" {
			t.Errorf("referrer %s kept as %q", ref, v.Get("r"))
		}
	}

	// Reading back: inside the window, expired, from the future, legacy.
	now := time.Now()
	read := func(value string) (*db.SignupDetail, bool) {
		r := httptest.NewRequest("POST", "/v1/auth/verify", nil)
		r.AddCookie(&http.Cookie{Name: arrivalCookie, Value: value})
		return readArrival(r, now)
	}
	at := func(d time.Duration) string {
		return url.Values{"f": {"vercel-ship"}, "t": {strconv.FormatInt(now.Add(d).Unix(), 10)}}.Encode()
	}
	if d, ok := read(at(-29 * 24 * time.Hour)); !ok || d.From != "vercel-ship" || d.LandedAt.IsZero() {
		t.Fatalf("29 days old: %+v %v", d, ok)
	}
	if _, ok := read(at(-31 * 24 * time.Hour)); ok {
		t.Fatal("31 days old still counted")
	}
	if _, ok := read(at(48 * time.Hour)); ok {
		t.Fatal("a landing in the future counted")
	}
	if _, ok := read("artifact-hub"); ok {
		t.Fatal("a bare value (plantable by a sibling host as a plain-named cookie) counted")
	}
	r := httptest.NewRequest("POST", "/v1/auth/verify", nil)
	r.AddCookie(&http.Cookie{Name: "sh_from", Value: at(-time.Hour)})
	if _, ok := readArrival(r, now); ok {
		t.Fatal("the plain-named cookie a sibling host can plant counted")
	}
	if _, ok := read("f=Bad%20Value&t=1"); ok {
		t.Fatal("malformed from accepted")
	}
}

// Attribution through POST /v1/auth/verify: a direct sign-up, a later one in
// another tab carrying the cookie, an expired cookie, and a sign-up on the
// connector's consent page in the same browser.
func TestArrivalAttribution(t *testing.T) {
	a := newPrivateApp(t)
	chatgpt := a.registerNamedClient(t, "ChatGPT")
	_, fresh := landing(t, "/?from=vercel-ship&utm_source=poll", "https://ship.vineetsriram.com/", nil)
	expired := arrivalCookie + "=" + url.Values{"f": {"vercel-ship"}, "t": {strconv.FormatInt(time.Now().Add(-31*24*time.Hour).Unix(), 10)}}.Encode()
	_, unnamed := landing(t, "/?from=hn-launch", "", nil)

	cases := []struct {
		label, cookie, client, wantSource, wantAgent string
		detail                                       bool
	}{
		{"direct", "", "", "website", "", false},
		{"later-tab", fresh, "", "vercel-ship", "", true},
		{"expired", expired, "", "website", "", false},
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
		if c.cookie != "" {
			headers["Cookie"] = c.cookie
		}
		r := a.at(t, "POST", a.appHost(), "/v1/auth/verify", body, headers)
		if r.status != 200 || r.json(t)["created"] != true {
			t.Fatalf("%s: verify %d %s", c.label, r.status, r.body)
		}
		got := signupOf(t, a, addr)
		if got.source.String != c.wantSource || got.agent.String != c.wantAgent {
			t.Errorf("%s: source %q agent %q", c.label, got.source.String, got.agent.String)
		}
		var detail []byte
		if err := a.database.QueryRow(`SELECT signup_detail FROM users WHERE username = $1`, addr).Scan(&detail); err != nil {
			t.Fatal(err)
		}
		if (len(detail) > 0) != c.detail {
			t.Errorf("%s: detail %s", c.label, detail)
		}
		if c.label == "later-tab" && (!strings.Contains(string(detail), `"utm_source": "poll"`) || !strings.Contains(string(detail), `"referrer": "ship.vineetsriram.com"`)) {
			t.Errorf("later-tab detail: %s", detail)
		}
	}

	// An agent's API call never takes the browser's link, even with the cookie.
	addr, code := newSignupAddr(t, a, "arr-agent")
	if r := a.at(t, "POST", a.appHost(), "/v1/auth/verify", map[string]any{"email": addr, "code": code}, map[string]string{"User-Agent": "curl/8", "Cookie": fresh}); r.status != 200 {
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
