package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/internal/auth"
	db "github.com/vsriram/simple-host/internal/db"
)

const signupApex = "simple-host.test"

// newSignupAddr returns a fresh address with a live dashboard code, removed
// again after the test.
func newSignupAddr(t *testing.T, a *privateApp, label string) (addr, code string) {
	t.Helper()
	addr = label + "-" + strconv.FormatInt(time.Now().UnixNano(), 36) + "@example.com"
	code = "515151"
	lt, _ := auth.GenerateAPIKey()
	if err := db.CreateAuthToken(context.Background(), a.database, addr, code, lt, time.Now().Add(5*time.Minute), "dashboard", sql.NullString{}, sql.NullString{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = a.database.Exec(`DELETE FROM users WHERE username = $1`, addr) })
	return addr, code
}

type signupRow struct {
	source, agent, method sql.NullString
	inferred              bool
}

func signupOf(t *testing.T, a *privateApp, addr string) signupRow {
	t.Helper()
	var r signupRow
	if err := a.database.QueryRow(`SELECT signup_source, signup_agent, signup_method, signup_inferred FROM users WHERE username = $1`, addr).
		Scan(&r.source, &r.agent, &r.method, &r.inferred); err != nil {
		t.Fatal(err)
	}
	return r
}

func (a *privateApp) registerNamedClient(t *testing.T, name string) string {
	t.Helper()
	r := a.at(t, "POST", signupApex, "/oauth/register", map[string]any{
		"client_name": name, "redirect_uris": []string{"https://chat.example.com/cb"}, "token_endpoint_auth_method": "none",
	}, nil)
	if r.status != 201 {
		t.Fatalf("register: %d %s", r.status, r.body)
	}
	id := r.json(t)["client_id"].(string)
	t.Cleanup(func() { _, _ = a.database.Exec(`DELETE FROM oauth_clients WHERE client_id = $1`, id) })
	return id
}

// Every way POST /v1/auth/verify creates an account records where it came
// from, once; a sign-in to an existing account never changes it.
func TestSignupSourceRecordedAtCreation(t *testing.T) {
	a := newPrivateApp(t)
	chatgpt := a.registerNamedClient(t, "ChatGPT")
	page := map[string]string{"Sec-Fetch-Site": "same-origin"}

	cases := []struct {
		label   string
		body    map[string]any
		headers map[string]string
		want    signupRow
	}{
		{"web", map[string]any{"name": "dashboard sign-in"}, page,
			signupRow{source: ns("website"), method: ns("email")}},
		{"web-origin", nil, map[string]string{"Origin": a.srv.URL},
			signupRow{source: ns("website"), method: ns("email")}},
		{"conn", map[string]any{"signup_client": chatgpt}, page,
			signupRow{source: ns("connector:ChatGPT"), method: ns("email")}},
		// An unknown app is not trusted to name itself: a page sign-up.
		{"conn-unknown", map[string]any{"signup_client": "nope"}, page,
			signupRow{source: ns("website"), method: ns("email")}},
		{"agent-client", nil, map[string]string{"X-Simple-Host-Client": "Codex CLI 1.2", "User-Agent": "curl/8.5.0"},
			signupRow{source: ns("agent"), agent: ns("Codex CLI 1.2"), method: ns("email")}},
		{"agent-skill", nil, map[string]string{"X-Skill-Version": "0.27.3", "User-Agent": "curl/8.5.0"},
			signupRow{source: ns("agent"), agent: ns("skill 0.27.3"), method: ns("email")}},
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
		r := a.at(t, "POST", a.appHost(), "/v1/auth/verify", body, c.headers)
		if r.status != 200 || r.json(t)["created"] != true {
			t.Fatalf("%s: verify %d %s", c.label, r.status, r.body)
		}
		if got := signupOf(t, a, addr); got != c.want {
			t.Errorf("%s: got %+v, want %+v", c.label, got, c.want)
		}
	}

	// An account from before tracking signs in again as an agent: still NULL.
	old := a.newPerson(t, "su-old")
	lt, _ := auth.GenerateAPIKey()
	if err := db.CreateAuthToken(context.Background(), a.database, old.email, "626262", lt, time.Now().Add(5*time.Minute), "dashboard", sql.NullString{}, sql.NullString{}); err != nil {
		t.Fatal(err)
	}
	if r := a.at(t, "POST", a.appHost(), "/v1/auth/verify", map[string]any{"email": old.email, "code": "626262"}, map[string]string{"User-Agent": "curl/8"}); r.status != 200 || r.json(t)["created"] == true {
		t.Fatalf("old account sign-in: %d %s", r.status, r.body)
	}
	if got := signupOf(t, a, old.email); got.source.Valid || got.agent.Valid || got.method.Valid || got.inferred {
		t.Fatalf("existing account touched: %+v", got)
	}
	// SetSignup never overwrites a recorded source.
	id, _ := a.userID(t, old)
	_ = db.SetSignup(context.Background(), a.database, id, db.Signup{Source: "website", Method: "email"})
	_ = db.SetSignup(context.Background(), a.database, id, db.Signup{Source: "agent", Agent: "curl"})
	if got := signupOf(t, a, old.email); got.source.String != "website" || got.agent.Valid {
		t.Fatalf("second SetSignup rewrote the source: %+v", got)
	}
}

// Issued accounts and Google sign-ups carry their source too.
func TestSignupSourceIssuedAndGoogle(t *testing.T) {
	a := newPrivateApp(t)
	prefix := "sui" + strconv.FormatInt(time.Now().UnixNano()%1e6, 36)
	r := a.at(t, "POST", signupApex, "/v1/admin/users", map[string]any{"count": 1, "prefix": prefix}, map[string]string{"X-API-Key": a.admin})
	if r.status != 201 {
		t.Fatalf("issue: %d %s", r.status, r.body)
	}
	name := r.json(t)["created"].([]any)[0].(map[string]any)["username"].(string)
	t.Cleanup(func() { _, _ = a.database.Exec(`DELETE FROM users WHERE username = $1`, name) })
	if got := signupOf(t, a, name); got.source.String != "admin" || got.method.String != "issued" {
		t.Fatalf("issued: %+v", got)
	}

	ctx := context.Background()
	claude := a.registerNamedClient(t, "Claude")
	for _, c := range []struct{ returnTo, want string }{
		{"https://simple-host.test/oauth/authorize?client_id=" + claude + "&cn=x", "connector:Claude"},
		{"https://simple-host.test/oauth/authorize?client_id=unknown&cn=x", "website"},
		{"https://simple-host.test/dashboard?cn=x", "website"},
	} {
		s := signupForOwnerReturnTo(ctx, a.database, c.returnTo, "google")
		if s.Source != c.want || s.Method != "google" {
			t.Errorf("%s: %+v", c.returnTo, s)
		}
	}
	if s := signupForOwnerReturnTo(ctx, a.database, "https://simple-host.test/dashboard?cn=x", "github"); s.Source != "website" || s.Method != "github" {
		t.Errorf("github: %+v", s)
	}
}

func TestSignupAgentName(t *testing.T) {
	long := strings.Repeat("a", 100)
	for _, c := range []struct {
		client, skill, ua, want string
	}{
		{"", "", "curl/8.5.0", "curl"},
		{"", "", "python-requests/2.31.0", "python-requests"},
		{"", "", "Go-http-client/1.1", "Go-http-client"},
		{"", "", "Mozilla/5.0 (Macintosh)", "browser"},
		{"", "", "", ""},
		{"", "", "<script>alert(1)</script>/1", "scriptalert"},
		{"My Agent\t\t v2", "", "curl/8", "My Agent v2"},
		{"<b>", "", "node", "b"},
		{long, "", "", strings.Repeat("a", 40)},
		{"", "0.27.3", "curl/8", "skill 0.27.3"},
		{"", "0.27.3\"><img>", "", "skill 0.27.3img"},
		{"", "", "10.0.0.1 x", ""},
		{"203.0.113.9", "", "curl/8", "curl"},
		{"", "", "::1", ""},
		{"10.0.0.1:8080", "", "curl/8", "curl"},
		{"", "", "10.0.0.1:8080/x", ""},
		{"", "", "[2001:db8::1]:443", ""},
	} {
		r := httptest.NewRequest("POST", "/v1/auth/verify", nil)
		if c.client != "" {
			r.Header.Set("X-Simple-Host-Client", c.client)
		}
		if c.skill != "" {
			r.Header.Set("X-Skill-Version", c.skill)
		}
		r.Header.Set("User-Agent", c.ua)
		if got := signupAgentName(r); got != c.want {
			t.Errorf("client=%q skill=%q ua=%q: got %q, want %q", c.client, c.skill, c.ua, got, c.want)
		}
		if len(signupAgentName(r)) > signupAgentMax {
			t.Errorf("over the cap: %q", signupAgentName(r))
		}
	}
}

// GET /v1/admin/users carries the sign-up fields and the site fields the
// admin page's tables read.
func TestAdminUsersSignupAndSiteFields(t *testing.T) {
	a := newPersonApp(t, "serve")
	p := a.newPerson(t, "sufields")
	a.deploy(t, p, "shop")
	id, _ := a.userID(t, p)
	if _, err := a.database.Exec(`UPDATE users SET signup_source = 'agent', signup_agent = 'curl', signup_method = 'email' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	if r := a.at(t, "GET", signupApex, "/v1/admin/users", nil, map[string]string{"X-API-Key": p.key}); r.status != 404 {
		t.Fatalf("non-admin: %d", r.status)
	}
	r := a.at(t, "GET", signupApex, "/v1/admin/users", nil, map[string]string{"X-API-Key": a.admin})
	if r.status != 200 {
		t.Fatalf("admin users: %d %s", r.status, r.body)
	}
	var found bool
	for _, x := range r.json(t)["users"].([]any) {
		u := x.(map[string]any)
		for _, k := range []string{"signup_source", "signup_agent", "signup_method", "signup_inferred"} {
			if _, ok := u[k]; !ok {
				t.Fatalf("user lacks %s: %v", k, u)
			}
		}
		if u["id"] != id {
			continue
		}
		found = true
		if u["signup_source"] != "agent" || u["signup_agent"] != "curl" || u["signup_method"] != "email" || u["signup_inferred"] != false {
			t.Fatalf("signup fields: %v", u)
		}
		s := u["sites"].([]any)[0].(map[string]any)
		if s["deployed_at"] == nil || s["visibility"] == "" || s["offline"] != false {
			t.Fatalf("site fields: %v", s)
		}
		if _, ok := s["domain_status"]; !ok {
			t.Fatalf("site lacks domain_status: %v", s)
		}
	}
	if !found {
		t.Fatal("person missing from /v1/admin/users")
	}
	// ?sizes=1 adds every site's size; without it the answer is unchanged.
	if r := a.at(t, "GET", signupApex, "/v1/admin/usage", nil, map[string]string{"X-API-Key": a.admin}); r.status != 200 || r.json(t)["site_sizes"] != nil {
		t.Fatalf("usage: %d %s", r.status, r.body)
	}
	r = a.at(t, "GET", signupApex, "/v1/admin/usage?sizes=1", nil, map[string]string{"X-API-Key": a.admin})
	sizes, _ := r.json(t)["site_sizes"].([]any)
	var sized bool
	for _, x := range sizes {
		m := x.(map[string]any)
		if m["user_id"] == id && m["name"] == "shop" && m["bytes"].(float64) > 0 {
			sized = true
		}
	}
	if !sized {
		t.Fatalf("site_sizes lacks the site: %s", r.body)
	}
}

// The admin page's per-site actions reach anyone's site by id, as its owner;
// the bare owner routes never did for the admin's key.
func TestAdminSiteActionsActAsOwner(t *testing.T) {
	a := newPersonApp(t, "serve")
	olive, oscar := a.newPerson(t, "asolive"), a.newPerson(t, "asoscar")
	a.deploy(t, olive, "shop")
	if r := a.at(t, "PUT", signupApex, "/v1/sites/shop/files", map[string]any{"files": map[string]string{"index.html": "v2"}}, map[string]string{"X-API-Key": olive.key}); r.status != 200 {
		t.Fatalf("v2: %d %s", r.status, r.body)
	}
	a.deploy(t, oscar, "blog")
	shop := a.siteID(t, olive, "shop")
	admin := map[string]string{"X-API-Key": a.admin}

	// The bare owner route with the admin key does not find another person's site.
	if r := a.at(t, "GET", signupApex, "/v1/sites/shop/versions", nil, admin); r.status != 404 {
		t.Fatalf("bare route as admin: %d %s", r.status, r.body)
	}
	for _, c := range []struct {
		key  string
		path string
		want int
	}{
		{oscar.key, "/v1/admin/sites/" + shop + "/versions", 404}, // another person
		{olive.key, "/v1/admin/sites/" + shop + "/versions", 404}, // the owner is not the admin
		{a.admin, "/v1/admin/sites/not-a-uuid/versions", 404},
		{a.admin, "/v1/admin/sites/00000000-0000-0000-0000-000000000000/versions", 404},
		{a.admin, "/v1/admin/sites/" + strings.ToUpper(shop) + "/versions", 200},
	} {
		if r := a.at(t, "GET", signupApex, c.path, nil, map[string]string{"X-API-Key": c.key}); r.status != c.want {
			t.Errorf("GET %s: %d, want %d (%s)", c.path, r.status, c.want, r.body)
		}
	}
	r := a.at(t, "GET", signupApex, "/v1/admin/sites/"+shop+"/versions", nil, admin)
	if r.status != 200 || !strings.Contains(string(r.body), `"version_number":2`) {
		t.Fatalf("versions: %d %s", r.status, r.body)
	}
	if r := a.at(t, "PUT", signupApex, "/v1/admin/sites/"+shop+"/active-version", map[string]int{"version_number": 1}, admin); r.status != 200 {
		t.Fatalf("make active: %d %s", r.status, r.body)
	}
	var active int
	_ = a.database.QueryRow(`SELECT active_version FROM sites WHERE id = $1`, shop).Scan(&active)
	if active != 1 {
		t.Fatalf("active version %d", active)
	}
	if r := a.at(t, "GET", signupApex, "/v1/admin/sites/"+shop+"/collections", nil, admin); r.status != 200 || !strings.Contains(string(r.body), `"collections"`) {
		t.Fatalf("collections: %d %s", r.status, r.body)
	}
	// A list's rows, by site id; oscar has a same-named list elsewhere.
	if r := a.at(t, "POST", signupApex, "/v1/sites/shop/collections/signups", map[string]string{"who": "olive-row"}, map[string]string{"X-API-Key": olive.key}); r.status != 201 && r.status != 200 {
		t.Fatalf("append: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", signupApex, "/v1/admin/sites/"+shop+"/collections/signups?limit=50", nil, admin); r.status != 200 || !strings.Contains(string(r.body), "olive-row") {
		t.Fatalf("rows: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", signupApex, "/v1/admin/sites/"+shop+"/collections/signups", nil, map[string]string{"X-API-Key": oscar.key}); r.status != 404 {
		t.Fatalf("rows as non-admin: %d", r.status)
	}
	// The id rides along: a lookup by owner and name that lands on another
	// site (a rename or a new site under the old name meanwhile) is refused.
	oliveID, _ := a.userID(t, olive)
	req := httptest.NewRequest("GET", "/", nil)
	req = req.WithContext(context.WithValue(req.Context(), adminSiteKey{}, "00000000-0000-0000-0000-000000000000"))
	if _, err := a.sites.siteForCaller(req, oliveID, "shop"); err != sql.ErrNoRows {
		t.Fatalf("mismatched id: %v", err)
	}
	req = req.WithContext(context.WithValue(req.Context(), adminSiteKey{}, shop))
	if s, err := a.sites.siteForCaller(req, oliveID, "shop"); err != nil || s.ID != shop {
		t.Fatalf("matching id: %v", err)
	}
	// A name from before the naming rules: delete says why it cannot.
	var legacy string
	if err := a.database.QueryRow(`INSERT INTO sites (user_id, name) VALUES ($1, 'Old_Name') RETURNING id`, oliveID).Scan(&legacy); err != nil {
		t.Fatal(err)
	}
	if r := a.at(t, "DELETE", signupApex, "/v1/admin/sites/"+legacy, nil, admin); r.status != 409 || r.json(t)["code"] != "legacy_site_name" {
		t.Fatalf("legacy delete: %d %s", r.status, r.body)
	}
	// A taken-down site stays as it is, as for its owner.
	if r := a.at(t, "POST", signupApex, "/v1/admin/sites/"+shop+"/suspend", map[string]string{"reason": "test"}, admin); r.status != 200 {
		t.Fatalf("suspend: %d", r.status)
	}
	if r := a.at(t, "DELETE", signupApex, "/v1/admin/sites/"+shop, nil, admin); r.status != 403 {
		t.Fatalf("delete taken-down: %d %s", r.status, r.body)
	}
	if r := a.at(t, "POST", signupApex, "/v1/admin/sites/"+shop+"/restore", nil, admin); r.status != 200 {
		t.Fatalf("restore: %d", r.status)
	}
	// Delete goes to the owner's Recently deleted; the other person's site is untouched.
	if r := a.at(t, "DELETE", signupApex, "/v1/admin/sites/"+shop, nil, map[string]string{"X-API-Key": oscar.key}); r.status != 404 {
		t.Fatalf("non-admin delete: %d", r.status)
	}
	if r := a.at(t, "DELETE", signupApex, "/v1/admin/sites/"+shop, nil, admin); r.status != 204 {
		t.Fatalf("delete: %d %s", r.status, r.body)
	}
	var deleted bool
	_ = a.database.QueryRow(`SELECT deleted_at IS NOT NULL FROM sites WHERE id = $1`, shop).Scan(&deleted)
	if !deleted {
		t.Fatal("site not in Recently deleted")
	}
	if r := a.at(t, "GET", signupApex, "/v1/admin/sites/"+shop+"/versions", nil, admin); r.status != 404 {
		t.Fatalf("deleted site still answers: %d", r.status)
	}
	if n := len(a.sitesOf(t, oscar)); n != 1 {
		t.Fatalf("oscar's sites: %d", n)
	}
}

func (a *privateApp) sitesOf(t *testing.T, p person) []any {
	t.Helper()
	r := a.at(t, "GET", signupApex, "/v1/sites", nil, map[string]string{"X-API-Key": p.key, "X-Skill-Version": "1.0.0"})
	var out []any
	if err := json.Unmarshal(r.body, &out); err != nil {
		t.Fatalf("sites: %s", r.body)
	}
	return out
}

// The migration's backfill marks only the obvious older accounts, as inferred.
func TestSignupBackfillMigration(t *testing.T) {
	a := newPrivateApp(t)
	sqlText, err := os.ReadFile("../../db/migrations/v075-signup-source.sql")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	mk := func(label string) string {
		p := a.newPerson(t, label)
		id, _ := a.userID(t, p)
		return id
	}
	google, conn, plain, issued := mk("bfg"), mk("bfc"), mk("bfp"), mk("bfi")
	client := a.registerNamedClient(t, "Grok")
	if _, err := db.InsertOAuthIdentity(ctx, a.database, google, "google", "g-"+google, "x@example.com", true); err != nil {
		t.Fatal(err)
	}
	if _, err := db.InsertOAuthGrant(ctx, a.database, conn, client, "sites", "https://x/mcp", ""); err != nil {
		t.Fatal(err)
	}
	// A Google identity linked a day later is not a Google sign-up.
	if _, err := db.InsertOAuthIdentity(ctx, a.database, plain, "google", "g-"+plain, "y@example.com", true); err != nil {
		t.Fatal(err)
	}
	if _, err := a.database.Exec(`UPDATE oauth_identities SET created_at = now() + interval '1 day' WHERE user_id = $1`, plain); err != nil {
		t.Fatal(err)
	}
	if _, err := a.database.Exec(`UPDATE users SET event_account = TRUE WHERE id = $1`, issued); err != nil {
		t.Fatal(err)
	}
	if _, err := a.database.Exec(string(sqlText)); err != nil {
		t.Fatalf("migration: %v", err)
	}
	if _, err := a.database.Exec(string(sqlText)); err != nil {
		t.Fatalf("migration is not idempotent: %v", err)
	}
	get := func(id string) (src, method string, inf bool) {
		var s, m sql.NullString
		_ = a.database.QueryRow(`SELECT signup_source, signup_method, signup_inferred FROM users WHERE id = $1`, id).Scan(&s, &m, &inf)
		return s.String, m.String, inf
	}
	for _, c := range []struct {
		id, src, method string
		inf             bool
	}{
		{google, "website", "google", true},
		{conn, "connector:Grok", "", true},
		{plain, "", "", false},
		{issued, "admin", "issued", true},
	} {
		if s, m, inf := get(c.id); s != c.src || m != c.method || inf != c.inf {
			t.Errorf("%s: got %q %q %v, want %q %q %v", c.id, s, m, inf, c.src, c.method, c.inf)
		}
	}
}

func ns(s string) sql.NullString { return sql.NullString{String: s, Valid: true} }
