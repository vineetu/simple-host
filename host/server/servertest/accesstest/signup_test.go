package accesstest

import (
	"context"
	"database/sql"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/vsriram/simple-host/host/access/accesshttp"
	"github.com/vsriram/simple-host/host/access/auth"
	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	model "github.com/vsriram/simple-host/host/model"
	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
)

// newSignupAddr returns a fresh address with a live dashboard code.
func newSignupAddr(t *testing.T, a *App, label string) (addr, code string) {
	t.Helper()
	addr = label + "-" + strconv.FormatInt(time.Now().UnixNano(), 36) + "@example.com"
	code = "515151"
	lt, _ := auth.GenerateAPIKey()
	if err := accountsrepo.CreateAuthToken(context.Background(), a.Database, addr, code, lt, time.Now().Add(5*time.Minute), "dashboard", sql.NullString{}, sql.NullString{}); err != nil {
		t.Fatal(err)
	}
	return addr, code
}

type signupRow struct {
	source, agent, method sql.NullString
	inferred              bool
}

func signupOf(t *testing.T, a *App, addr string) signupRow {
	t.Helper()
	var r signupRow
	if err := a.Database.QueryRow(`SELECT signup_source, signup_agent, signup_method, signup_inferred FROM users WHERE username = $1`, addr).
		Scan(&r.source, &r.agent, &r.method, &r.inferred); err != nil {
		t.Fatal(err)
	}
	return r
}

// Every way POST /v1/auth/verify creates an account records where it came
// from, once; a sign-in to an existing account never changes it.
func TestSignupSourceRecordedAtCreation(t *testing.T) {
	a := NewApp(t)
	chatgpt := a.RegisterNamedClient(t, "ChatGPT")
	page := map[string]string{"Sec-Fetch-Site": "same-origin"}

	cases := []struct {
		label   string
		body    map[string]any
		headers map[string]string
		want    signupRow
	}{
		{"web", map[string]any{"name": "dashboard sign-in"}, page,
			signupRow{source: ns("website"), method: ns("email")}},
		// A page on the app's own address: the Origin must match the public
		// base URL (here the plain http test server), and the account sign-in
		// gate compares it with the request's own scheme, so say http.
		{"web-origin", nil, map[string]string{"Origin": a.Srv.URL, "X-Forwarded-Proto": "http"},
			signupRow{source: ns("website"), method: ns("email")}},
		{"conn", map[string]any{"signup_client": chatgpt}, page,
			signupRow{source: ns("connector:ChatGPT"), method: ns("email")}},
		// An unknown app is not trusted to name itself: a page sign-up.
		{"conn-unknown", map[string]any{"signup_client": "nope"}, page,
			signupRow{source: ns("website"), method: ns("email")}},
		{"agent-client", nil, map[string]string{"X-Simple-Host-Client": "Codex CLI 1.2", "User-Agent": "curl/8.5.0"},
			signupRow{source: ns("agent"), agent: ns("Codex CLI 1.2"), method: ns("email")}},
		{"agent-ua", nil, map[string]string{"User-Agent": "python-requests/2.31.0"},
			signupRow{source: ns("agent"), agent: ns("python-requests"), method: ns("email")}},
		// A cross-site browser request is not one of the site's pages.
		{"agent-xsite", nil, map[string]string{"Sec-Fetch-Site": "cross-site", "User-Agent": "Mozilla/5.0 (X11)"},
			signupRow{source: ns("agent"), agent: ns("browser"), method: ns("email")}},
	}
	for _, c := range cases {
		addr, code := newSignupAddr(t, a, "su-"+c.label)
		body := map[string]any{"email": addr, "code": code}
		for k, v := range c.body {
			body[k] = v
		}
		r := a.At(t, "POST", a.AppHost(), "/v1/auth/verify", body, c.headers)
		if r.Status != 200 || r.JSON(t)["created"] != true {
			t.Fatalf("%s: verify %d %s", c.label, r.Status, r.Body)
		}
		if got := signupOf(t, a, addr); got != c.want {
			t.Errorf("%s: got %+v, want %+v", c.label, got, c.want)
		}
	}

	// An account from before tracking signs in again as an agent: still NULL.
	old := a.NewPerson(t, "su-old")
	lt, _ := auth.GenerateAPIKey()
	if err := accountsrepo.CreateAuthToken(context.Background(), a.Database, old.Email, "626262", lt, time.Now().Add(5*time.Minute), "dashboard", sql.NullString{}, sql.NullString{}); err != nil {
		t.Fatal(err)
	}
	if r := a.At(t, "POST", a.AppHost(), "/v1/auth/verify", map[string]any{"email": old.Email, "code": "626262"}, map[string]string{"User-Agent": "curl/8"}); r.Status != 200 || r.JSON(t)["created"] == true {
		t.Fatalf("old account sign-in: %d %s", r.Status, r.Body)
	}
	if got := signupOf(t, a, old.Email); got.source.Valid || got.agent.Valid || got.method.Valid || got.inferred {
		t.Fatalf("existing account touched: %+v", got)
	}
	// SetSignup never overwrites a recorded source.
	id := old.ID
	_ = accountsrepo.SetSignup(context.Background(), a.Database, id, model.Signup{Source: "website", Method: "email"})
	_ = accountsrepo.SetSignup(context.Background(), a.Database, id, model.Signup{Source: "agent", Agent: "curl"})
	if got := signupOf(t, a, old.Email); got.source.String != "website" || got.agent.Valid {
		t.Fatalf("second SetSignup rewrote the source: %+v", got)
	}
}

// Issued accounts and Google sign-ups carry their source too.
func TestSignupSourceIssuedAndGoogle(t *testing.T) {
	a := NewApp(t)
	prefix := "sui" + strconv.FormatInt(time.Now().UnixNano()%1e6, 36)
	r := a.At(t, "POST", rt.SiteDomain, "/v1/admin/users", map[string]any{"count": 1, "prefix": prefix}, map[string]string{"X-API-Key": a.Admin})
	if r.Status != 201 {
		t.Fatalf("issue: %d %s", r.Status, r.Body)
	}
	name := r.JSON(t)["created"].([]any)[0].(map[string]any)["username"].(string)
	if got := signupOf(t, a, name); got.source.String != "admin" || got.method.String != "issued" {
		t.Fatalf("issued: %+v", got)
	}

	ctx := context.Background()
	claude := a.RegisterNamedClient(t, "Claude")
	for _, c := range []struct{ returnTo, want string }{
		{"https://simple-host.test/oauth/authorize?client_id=" + claude + "&cn=x", "connector:Claude"},
		{"https://simple-host.test/oauth/authorize?client_id=unknown&cn=x", "website"},
		{"https://simple-host.test/dashboard?cn=x", "website"},
	} {
		s := accesshttp.SignupForOwnerReturnTo(ctx, a.Database, httptest.NewRequest("GET", "/", nil), c.returnTo, "google")
		if s.Source != c.want || s.Method != "google" {
			t.Errorf("%s: %+v", c.returnTo, s)
		}
	}
	if s := accesshttp.SignupForOwnerReturnTo(ctx, a.Database, httptest.NewRequest("GET", "/", nil), "https://simple-host.test/dashboard?cn=x", "github"); s.Source != "website" || s.Method != "github" {
		t.Errorf("github: %+v", s)
	}
}

func ns(s string) sql.NullString { return sql.NullString{String: s, Valid: true} }
