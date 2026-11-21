package accesstest

import (
	"context"
	"database/sql"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/host/access/accesshttp"
	"github.com/vsriram/simple-host/host/access/auth"
	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	model "github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/server/servertest"
	"github.com/vsriram/simple-host/host/web"
)

// newSignupAddr returns a fresh address with a live dashboard code, removed
// again after the test.
func newSignupAddr(t *testing.T, a *servertest.PrivateApp, label string) (addr, code string) {
	t.Helper()
	addr = label + "-" + strconv.FormatInt(time.Now().UnixNano(), 36) + "@example.com"
	code = "515151"
	lt, _ := auth.GenerateAPIKey()
	if err := accountsrepo.CreateAuthToken(context.Background(), a.Database, addr, code, lt, time.Now().Add(5*time.Minute), "dashboard", sql.NullString{}, sql.NullString{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = a.Database.Exec(`DELETE FROM users WHERE username = $1`, addr) })
	return addr, code
}

type signupRow struct {
	source, agent, method sql.NullString
	inferred              bool
}

func signupOf(t *testing.T, a *servertest.PrivateApp, addr string) signupRow {
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
	a := servertest.NewPrivateApp(t)
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
	id, _ := a.UserID(t, old)
	_ = accountsrepo.SetSignup(context.Background(), a.Database, id, model.Signup{Source: "website", Method: "email"})
	_ = accountsrepo.SetSignup(context.Background(), a.Database, id, model.Signup{Source: "agent", Agent: "curl"})
	if got := signupOf(t, a, old.Email); got.source.String != "website" || got.agent.Valid {
		t.Fatalf("second SetSignup rewrote the source: %+v", got)
	}
}

// Issued accounts and Google sign-ups carry their source too.
func TestSignupSourceIssuedAndGoogle(t *testing.T) {
	a := servertest.NewPrivateApp(t)
	prefix := "sui" + strconv.FormatInt(time.Now().UnixNano()%1e6, 36)
	r := a.At(t, "POST", servertest.SignupApex, "/v1/admin/users", map[string]any{"count": 1, "prefix": prefix}, map[string]string{"X-API-Key": a.Admin})
	if r.Status != 201 {
		t.Fatalf("issue: %d %s", r.Status, r.Body)
	}
	name := r.JSON(t)["created"].([]any)[0].(map[string]any)["username"].(string)
	t.Cleanup(func() { _, _ = a.Database.Exec(`DELETE FROM users WHERE username = $1`, name) })
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

// GET /v1/admin/users carries the sign-up fields and the site fields the
// admin page's tables read.
func TestAdminUsersSignupAndSiteFields(t *testing.T) {
	a := servertest.NewPersonApp(t, "serve")
	p := a.NewPerson(t, "sufields")
	a.Deploy(t, p, "shop")
	id, _ := a.UserID(t, p)
	if _, err := a.Database.Exec(`UPDATE users SET signup_source = 'agent', signup_agent = 'curl', signup_method = 'email' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	if r := a.At(t, "GET", servertest.SignupApex, "/v1/admin/users", nil, map[string]string{"X-API-Key": p.Key}); r.Status != 404 {
		t.Fatalf("non-admin: %d", r.Status)
	}
	r := a.At(t, "GET", servertest.SignupApex, "/v1/admin/users", nil, map[string]string{"X-API-Key": a.Admin})
	if r.Status != 200 {
		t.Fatalf("admin users: %d %s", r.Status, r.Body)
	}
	var found bool
	for _, x := range r.JSON(t)["users"].([]any) {
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
	if r := a.At(t, "GET", servertest.SignupApex, "/v1/admin/usage", nil, map[string]string{"X-API-Key": a.Admin}); r.Status != 200 || r.JSON(t)["site_sizes"] != nil {
		t.Fatalf("usage: %d %s", r.Status, r.Body)
	}
	r = a.At(t, "GET", servertest.SignupApex, "/v1/admin/usage?sizes=1", nil, map[string]string{"X-API-Key": a.Admin})
	sizes, _ := r.JSON(t)["site_sizes"].([]any)
	var sized bool
	for _, x := range sizes {
		m := x.(map[string]any)
		if m["user_id"] == id && m["name"] == "shop" && m["bytes"].(float64) > 0 {
			sized = true
		}
	}
	if !sized {
		t.Fatalf("site_sizes lacks the site: %s", r.Body)
	}
}

// The admin page's per-site actions reach anyone's site by id, as its owner;
// the bare owner routes never did for the admin's key.
func TestAdminSiteActionsActAsOwner(t *testing.T) {
	a := servertest.NewPersonApp(t, "serve")
	olive, oscar := a.NewPerson(t, "asolive"), a.NewPerson(t, "asoscar")
	a.Deploy(t, olive, "shop")
	if r := a.At(t, "PUT", servertest.SignupApex, "/v1/sites/shop/files", map[string]any{"files": map[string]string{"index.html": "v2"}}, map[string]string{"X-API-Key": olive.Key}); r.Status != 200 {
		t.Fatalf("v2: %d %s", r.Status, r.Body)
	}
	a.Deploy(t, oscar, "blog")
	shop := a.SiteID(t, olive, "shop")
	admin := map[string]string{"X-API-Key": a.Admin}

	// The bare owner route with the admin key does not find another person's site.
	if r := a.At(t, "GET", servertest.SignupApex, "/v1/sites/shop/versions", nil, admin); r.Status != 404 {
		t.Fatalf("bare route as admin: %d %s", r.Status, r.Body)
	}
	for _, c := range []struct {
		key  string
		path string
		want int
	}{
		{oscar.Key, "/v1/admin/sites/" + shop + "/versions", 404}, // another person
		{olive.Key, "/v1/admin/sites/" + shop + "/versions", 404}, // the owner is not the admin
		{a.Admin, "/v1/admin/sites/not-a-uuid/versions", 404},
		{a.Admin, "/v1/admin/sites/00000000-0000-0000-0000-000000000000/versions", 404},
		{a.Admin, "/v1/admin/sites/" + strings.ToUpper(shop) + "/versions", 200},
	} {
		if r := a.At(t, "GET", servertest.SignupApex, c.path, nil, map[string]string{"X-API-Key": c.key}); r.Status != c.want {
			t.Errorf("GET %s: %d, want %d (%s)", c.path, r.Status, c.want, r.Body)
		}
	}
	r := a.At(t, "GET", servertest.SignupApex, "/v1/admin/sites/"+shop+"/versions", nil, admin)
	if r.Status != 200 || !strings.Contains(string(r.Body), `"version_number":2`) {
		t.Fatalf("versions: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "PUT", servertest.SignupApex, "/v1/admin/sites/"+shop+"/active-version", map[string]int{"version_number": 1}, admin); r.Status != 200 {
		t.Fatalf("make active: %d %s", r.Status, r.Body)
	}
	var active int
	_ = a.Database.QueryRow(`SELECT active_version FROM sites WHERE id = $1`, shop).Scan(&active)
	if active != 1 {
		t.Fatalf("active version %d", active)
	}
	// The id rides along: a lookup by owner and name that lands on another
	// site (a rename or a new site under the old name meanwhile) is refused.
	oliveID, _ := a.UserID(t, olive)
	req := httptest.NewRequest("GET", "/", nil)
	req = req.WithContext(context.WithValue(req.Context(), web.AdminSiteKey{}, "00000000-0000-0000-0000-000000000000"))
	if _, err := a.Sites.SiteForCaller(req, oliveID, "shop"); err != sql.ErrNoRows {
		t.Fatalf("mismatched id: %v", err)
	}
	req = req.WithContext(context.WithValue(req.Context(), web.AdminSiteKey{}, shop))
	if s, err := a.Sites.SiteForCaller(req, oliveID, "shop"); err != nil || s.ID != shop {
		t.Fatalf("matching id: %v", err)
	}
	// A name from before the naming rules: delete says why it cannot.
	var legacy string
	if err := a.Database.QueryRow(`INSERT INTO sites (user_id, name) VALUES ($1, 'Old_Name') RETURNING id`, oliveID).Scan(&legacy); err != nil {
		t.Fatal(err)
	}
	if r := a.At(t, "DELETE", servertest.SignupApex, "/v1/admin/sites/"+legacy, nil, admin); r.Status != 409 || r.JSON(t)["code"] != "legacy_site_name" {
		t.Fatalf("legacy delete: %d %s", r.Status, r.Body)
	}
	// A taken-down site stays as it is, as for its owner.
	if r := a.At(t, "POST", servertest.SignupApex, "/v1/admin/sites/"+shop+"/suspend", map[string]string{"reason": "test"}, admin); r.Status != 200 {
		t.Fatalf("suspend: %d", r.Status)
	}
	if r := a.At(t, "DELETE", servertest.SignupApex, "/v1/admin/sites/"+shop, nil, admin); r.Status != 403 {
		t.Fatalf("delete taken-down: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "POST", servertest.SignupApex, "/v1/admin/sites/"+shop+"/restore", nil, admin); r.Status != 200 {
		t.Fatalf("restore: %d", r.Status)
	}
	// Delete goes to the owner's Recently deleted; the other person's site is untouched.
	if r := a.At(t, "DELETE", servertest.SignupApex, "/v1/admin/sites/"+shop, nil, map[string]string{"X-API-Key": oscar.Key}); r.Status != 404 {
		t.Fatalf("non-admin delete: %d", r.Status)
	}
	if r := a.At(t, "DELETE", servertest.SignupApex, "/v1/admin/sites/"+shop, nil, admin); r.Status != 204 {
		t.Fatalf("delete: %d %s", r.Status, r.Body)
	}
	var deleted bool
	_ = a.Database.QueryRow(`SELECT deleted_at IS NOT NULL FROM sites WHERE id = $1`, shop).Scan(&deleted)
	if !deleted {
		t.Fatal("site not in Recently deleted")
	}
	if r := a.At(t, "GET", servertest.SignupApex, "/v1/admin/sites/"+shop+"/versions", nil, admin); r.Status != 404 {
		t.Fatalf("deleted site still answers: %d", r.Status)
	}
	if n := len(a.SitesOf(t, oscar)); n != 1 {
		t.Fatalf("oscar's sites: %d", n)
	}
}

// The migration's backfill marks only the obvious older accounts, as inferred.
func TestSignupBackfillMigration(t *testing.T) {
	a := servertest.NewPrivateApp(t)
	sqlText, err := os.ReadFile("../../../db/migrations/v075-signup-source.sql")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	mk := func(label string) string {
		p := a.NewPerson(t, label)
		id, _ := a.UserID(t, p)
		return id
	}
	google, conn, plain, issued := mk("bfg"), mk("bfc"), mk("bfp"), mk("bfi")
	client := a.RegisterNamedClient(t, "Grok")
	if _, err := accountsrepo.InsertOAuthIdentity(ctx, a.Database, google, "google", "g-"+google, "x@example.com", true); err != nil {
		t.Fatal(err)
	}
	if _, err := accountsrepo.InsertOAuthGrant(ctx, a.Database, conn, client, "sites", "https://x/mcp", ""); err != nil {
		t.Fatal(err)
	}
	// A Google identity linked a day later is not a Google sign-up.
	if _, err := accountsrepo.InsertOAuthIdentity(ctx, a.Database, plain, "google", "g-"+plain, "y@example.com", true); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Database.Exec(`UPDATE oauth_identities SET created_at = now() + interval '1 day' WHERE user_id = $1`, plain); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Database.Exec(`UPDATE users SET event_account = TRUE WHERE id = $1`, issued); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Database.Exec(string(sqlText)); err != nil {
		t.Fatalf("migration: %v", err)
	}
	if _, err := a.Database.Exec(string(sqlText)); err != nil {
		t.Fatalf("migration is not idempotent: %v", err)
	}
	get := func(id string) (src, method string, inf bool) {
		var s, m sql.NullString
		_ = a.Database.QueryRow(`SELECT signup_source, signup_method, signup_inferred FROM users WHERE id = $1`, id).Scan(&s, &m, &inf)
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
