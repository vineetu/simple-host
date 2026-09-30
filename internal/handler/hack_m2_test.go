package handler

import (
	"context"
	"database/sql"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/internal/auth"
	db "github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/email"
	"github.com/vsriram/simple-host/internal/storage"
)

// M2: team sites, member keys, the deadline freeze, organiser moderation and
// the connector bound to a team — wired the way cmd/server/main.go wires
// hosted mode (scope gate, connector, host routing).

const tsDomain = "simple-hack.test"

type teamSiteApp struct {
	*connectorApp
	sites   *SiteHandler
	hack    *HackHandler
	certDir string
}

func newTeamSiteApp(t *testing.T) *teamSiteApp {
	t.Helper()
	dsn := os.Getenv("DB_DSN")
	if dsn == "" {
		t.Skip("DB_DSN unset")
	}
	database, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	if err := database.Ping(); err != nil {
		t.Skipf("postgres not reachable: %v", err)
	}
	if err := db.VerifyHackSchema(context.Background(), database); err != nil {
		t.Fatalf("test database is behind the schema: %v", err)
	}
	disk, err := storage.NewDiskStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	certDir := t.TempDir()
	for _, d := range []string{"requests", "ready"} {
		if err := os.MkdirAll(filepath.Join(certDir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	SetHackMode(true)
	auth.SetTeamKeys(true)
	db.SetPlatformDomain(tsDomain)
	t.Cleanup(func() { SetHackMode(false); auth.SetTeamKeys(false); db.SetPlatformDomain("") })

	adminKey, _ := auth.GenerateAPIKey()
	adminID, err := db.EnsureAdminUser(context.Background(), database)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	a := &teamSiteApp{connectorApp: &connectorApp{database: database, admin: adminKey, mux: mux}, certDir: certDir}
	authMW := auth.Middleware(adminKey, adminID, database)
	var root http.Handler
	a.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { root.ServeHTTP(w, r) }))
	t.Cleanup(a.srv.Close)
	mailer := email.NewResendSender("", "test@example.com")
	users := NewUserHandler(database, mailer, a.srv.URL)
	users.Register(mux, authMW, NoticeMiddleware("1.0.0"))
	a.sites = NewSiteHandler(database, disk, tsDomain, "sites."+tsDomain, "cname."+tsDomain, "", "", adminKey, nil, 0, "on", adminID, mailer, users.EmailLimiter())
	a.sites.Register(mux, authMW, NoticeMiddleware("1.0.0"))
	a.sites.SetPersonHosts("canonical")
	a.sites.SetSiteHosts("canonical", certDir)
	a.hack = NewHackHandler(database, a.srv.URL, tsDomain)
	a.hack.SetSites(a.sites)
	a.hack.Register(mux, authMW)
	a.sites.SetHackEventPage(HackEventPage(database, a.srv.URL, a.sites.TeamSiteURL, a.sites.TeamSitesReady))
	a.sites.SetHackScreenshot(HackScreenshot(database, a.sites.TeamSitesReady))
	gated := auth.ScopeGate(database, mux)
	a.conn = NewConnectorHandler(database, a.srv.URL, adminKey, tsDomain, "sites."+tsDomain, "1.0.0", gated)
	a.conn.Register(mux, authMW)
	a.conn.SetHackTeams(true)
	a.conn.SetSignInAlerts(users.SignInAlerts())
	app := SecurityHeaders(CORS(a.conn.BearerAuth(gated)))
	root = a.sites.FamilyHosts(app, a.sites.SiteBaseHosts(a.sites.BoundSubdomains(app, a.sites.SiteHosts(app, a.sites.PersonHosts(app, a.sites.LegacyHostRedirect(app))))))
	return a
}

// on sends a request to host (as nginx would, over HTTPS).
func (a *teamSiteApp) on(t *testing.T, method, host, path string, body any, key string) resp {
	t.Helper()
	var rd io.Reader
	if body != nil {
		if s, ok := body.(string); ok {
			rd = strings.NewReader(s)
		} else {
			rd = jsonBody(body)
		}
	}
	req, err := http.NewRequest(method, a.srv.URL+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	if host != "" {
		req.Host = host
	}
	req.Header.Set("X-Forwarded-Proto", "https")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		req.Header.Set("X-API-Key", key)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return resp{res.StatusCode, res.Header, b}
}

// api calls the apex API.
func (a *teamSiteApp) api(t *testing.T, method, path string, body any, key string) resp {
	return a.on(t, method, "", path, body, key)
}

func (a *teamSiteApp) uid(t *testing.T, p person) string {
	t.Helper()
	var id string
	if err := a.database.QueryRow(`SELECT id FROM users WHERE username = $1`, p.email).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func (a *teamSiteApp) makeEvent(t *testing.T, org person) string {
	t.Helper()
	slug := uniqueSlug()
	body := map[string]any{
		"slug": slug, "title": "M2 test", "organiser_name": "Org", "contact_email": "org@example.com",
		"purpose": "testing", "expected_participants": 10, "starts_at": "2026-10-01", "time_zone": "UTC",
	}
	r := a.api(t, "POST", "/v1/hack/events", body, org.key)
	if r.status != http.StatusCreated {
		t.Fatalf("create: %d %s", r.status, r.body)
	}
	t.Cleanup(func() {
		var accountID string
		_ = a.database.QueryRow(`SELECT account_id FROM events WHERE slug = $1`, slug).Scan(&accountID)
		_, _ = a.database.Exec(`DELETE FROM events WHERE slug = $1`, slug)
		if accountID != "" {
			_, _ = a.database.Exec(`DELETE FROM users WHERE id = $1`, accountID)
		}
	})
	if r := a.api(t, "POST", "/v1/hack/events/"+slug+"/stage", map[string]string{"stage": "open"}, org.key); r.status != 200 {
		t.Fatalf("open: %d %s", r.status, r.body)
	}
	return slug
}

func (a *teamSiteApp) join(t *testing.T, slug string, p person, org person) {
	t.Helper()
	ev := a.api(t, "GET", "/v1/hack/events/"+slug, nil, org.key).json(t)
	code := ev["organiser"].(map[string]any)["join_code"].(string)
	if r := a.api(t, "POST", "/v1/hack/join/"+code, map[string]any{"accept_coc": true, "display_name": "P"}, p.key); r.status != 200 && r.status != 201 {
		t.Fatalf("join: %d %s", r.status, r.body)
	}
}

func (a *teamSiteApp) startTeam(t *testing.T, slug, name string, p person) (teamSlug, code string) {
	t.Helper()
	r := a.api(t, "POST", "/v1/hack/events/"+slug+"/teams", map[string]string{"name": name}, p.key)
	if r.status != http.StatusCreated {
		t.Fatalf("team: %d %s", r.status, r.body)
	}
	m := r.json(t)
	return m["slug"].(string), m["code"].(string)
}

func (a *teamSiteApp) teamKey(t *testing.T, slug string, p person) string {
	t.Helper()
	r := a.api(t, "POST", "/v1/hack/events/"+slug+"/key", nil, p.key)
	if r.status != http.StatusCreated {
		t.Fatalf("key: %d %s", r.status, r.body)
	}
	return r.json(t)["key"].(string)
}

func deployBody(text string) map[string]any {
	return map[string]any{"files": map[string]string{"index.html": "<h1>" + text + "</h1>"}}
}

func (a *teamSiteApp) deployTeam(t *testing.T, site, key, text string) resp {
	t.Helper()
	return a.api(t, "PUT", "/v1/sites/"+site+"/files?create=1", deployBody(text), key)
}

func wantTS(t *testing.T, what string, r resp, status int, code string) {
	t.Helper()
	if r.status != status {
		t.Fatalf("%s: got %d want %d: %s", what, r.status, status, r.body)
	}
	if code != "" {
		if got, _ := r.json(t)["code"].(string); got != code {
			t.Fatalf("%s: code %q want %q: %s", what, got, code, r.body)
		}
	}
}

func TestHackM2TeamSitesEndToEnd(t *testing.T) {
	a := newTeamSiteApp(t)
	org, p1, p2, p3 := a.newPerson(t, "org"), a.newPerson(t, "p1"), a.newPerson(t, "p2"), a.newPerson(t, "p3")
	slug := a.makeEvent(t, org)
	// Opening the event asked for its certificate.
	if _, err := os.Stat(filepath.Join(a.certDir, "requests", slug)); err != nil {
		t.Fatalf("no certificate request on open: %v", err)
	}
	for _, p := range []person{p1, p2, p3} {
		a.join(t, slug, p, org)
	}
	alpha, alphaCode := a.startTeam(t, slug, "Alpha", p1)
	if r := a.api(t, "POST", "/v1/hack/events/"+slug+"/teams/join", map[string]string{"code": alphaCode}, p2.key); r.status != 200 {
		t.Fatalf("join team: %d %s", r.status, r.body)
	}
	beta, _ := a.startTeam(t, slug, "Beta", p3)
	k1, k2, k3 := a.teamKey(t, slug, p1), a.teamKey(t, slug, p2), a.teamKey(t, slug, p3)

	// Not ready: nothing lands anywhere.
	wantTS(t, "deploy before ready", a.deployTeam(t, alpha, k1, "a1"), 409, "team_sites_not_ready")
	markReady(t, a.certDir, slug)

	// A personal key owns no sites.
	wantTS(t, "personal key", a.deployTeam(t, alpha, p1.key, "x"), 403, "no_personal_sites")
	// Members deploy to their team's site.
	if r := a.deployTeam(t, alpha, k1, "alpha one"); r.status != 201 {
		t.Fatalf("p1 deploy: %d %s", r.status, r.body)
	}
	if r := a.deployTeam(t, alpha, k2, "alpha two"); r.status != 200 {
		t.Fatalf("p2 deploy: %d %s", r.status, r.body)
	}
	if r := a.deployTeam(t, beta, k3, "beta one"); r.status != 201 {
		t.Fatalf("p3 deploy: %d %s", r.status, r.body)
	}
	alphaHost := alpha + "." + slug + "." + tsDomain
	betaHost := beta + "." + slug + "." + tsDomain
	if r := a.on(t, "GET", alphaHost, "/", nil, ""); r.status != 200 || !strings.Contains(string(r.body), "alpha two") {
		t.Fatalf("alpha site: %d %s", r.status, r.body)
	}
	// Person-path serving stays off: the event host never serves a team's files.
	if r := a.on(t, "GET", slug+"."+tsDomain, "/"+alpha+"/", nil, ""); r.status == 200 {
		t.Fatalf("event host served a team path: %s", r.body)
	}
	if r := a.on(t, "GET", slug+"."+tsDomain, "/v1/sites/"+alpha+"/data/x", nil, ""); r.status != 404 {
		t.Fatalf("event host /v1/: %d", r.status)
	}

	// Cross-team: a key never reaches another team's site.
	wantTS(t, "alpha key on beta", a.deployTeam(t, beta, k1, "pwn"), 403, "team_site_only")
	wantTS(t, "alpha key rollback beta", a.api(t, "PUT", "/v1/sites/"+beta+"/active-version", map[string]int{"version_number": 1}, k1), 403, "team_site_only")
	wantTS(t, "alpha key preview beta", a.api(t, "POST", "/v1/sites/"+beta+"/versions/1/preview-link", nil, k1), 403, "team_site_only")
	wantTS(t, "alpha key beta kind", a.api(t, "PUT", "/v1/sites/"+beta+"/data/votes/kind", map[string]string{"kind": "public"}, k1), 403, "team_site_only")
	// On beta's own host, alpha's key naming alpha resolves nothing there.
	if r := a.on(t, "GET", betaHost, "/v1/sites/"+beta+"/data", nil, k1); r.status/100 == 2 {
		t.Fatalf("alpha key on beta host: %d %s", r.status, r.body)
	}
	if r := a.on(t, "GET", betaHost, "/v1/sites/"+alpha+"/data", nil, k1); r.status/100 == 2 && !strings.Contains(string(r.body), `"site":"`+alpha+`"`) {
		t.Fatalf("alpha key on beta host read another site: %d %s", r.status, r.body)
	}
	if r := a.api(t, "GET", "/v1/me", nil, k1); r.status != 200 || !strings.Contains(string(r.body), `"site":"`+alpha+`"`) || strings.Contains(string(r.body), "events.invalid") {
		t.Fatalf("me as team: %d %s", r.status, r.body)
	}
	// Listing shows the team's own site only.
	lr := a.api(t, "GET", "/v1/sites", nil, k1)
	if lr.status != 200 || strings.Contains(string(lr.body), `"`+beta+`"`) || !strings.Contains(string(lr.body), alpha) {
		t.Fatalf("list: %d %s", lr.status, lr.body)
	}
	// Scope: nothing but the team routes.
	for _, c := range []struct{ m, p string }{
		{"DELETE", "/v1/sites/" + alpha},
		{"PATCH", "/v1/sites/" + alpha},
		{"PUT", "/v1/sites/" + alpha + "/allow-anonymous-writes"},
		{"PUT", "/v1/sites/" + alpha + "/keep-versions"},
		{"PUT", "/v1/sites/" + alpha + "/lock"},
		{"POST", "/v1/sites/" + alpha + "/domain"},
		{"GET", "/v1/hack/events"},
		{"GET", "/v1/hack/events/" + slug},
		{"POST", "/v1/me/keys"},
		{"DELETE", "/v1/me"},
		{"GET", "/v1/u/" + slug + "/sites/" + beta + "/data/x"},
	} {
		wantTS(t, c.m+" "+c.p, a.api(t, c.m, c.p, map[string]any{}, k1), 403, "team_key_scope")
	}
	// Declaring a kind on its own site is allowed (private saves).
	if r := a.api(t, "PUT", "/v1/sites/"+alpha+"/data/signups/kind", map[string]string{"kind": "submissions"}, k1); r.status/100 != 2 {
		t.Fatalf("declare kind: %d %s", r.status, r.body)
	}
	// A team key cannot connect an app (consent resolves people only).
	if u, status, _ := a.conn.resolveConsentUser(context.Background(), k1); status == 0 || u.ID != "" {
		t.Fatalf("team key passed consent: %d", status)
	}

	// ---- deadline freeze ---------------------------------------------------
	ev := a.api(t, "PATCH", "/v1/hack/events/"+slug, map[string]string{"submission_deadline": time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)}, org.key)
	if ev.status != 200 {
		t.Fatalf("deadline: %d %s", ev.status, ev.body)
	}
	wantTS(t, "deploy after deadline", a.deployTeam(t, alpha, k1, "late"), 409, "submissions_closed")
	wantTS(t, "rollback after deadline", a.api(t, "PUT", "/v1/sites/"+alpha+"/active-version", map[string]int{"version_number": 1}, k1), 409, "submissions_closed")
	wantTS(t, "data write after deadline", a.api(t, "PUT", "/v1/sites/"+alpha+"/data/signups/kind", map[string]string{"kind": "content"}, k1), 409, "submissions_closed")
	// Preview links still work (read-only).
	if r := a.api(t, "POST", "/v1/sites/"+alpha+"/versions/2/preview-link", nil, k1); r.status != 200 {
		t.Fatalf("preview after deadline: %d %s", r.status, r.body)
	}
	teams := a.api(t, "GET", "/v1/hack/events/"+slug+"/teams", nil, org.key).json(t)["teams"].([]any)
	var alphaObj map[string]any
	for _, x := range teams {
		if m := x.(map[string]any); m["slug"] == alpha {
			alphaObj = m
		}
	}
	if alphaObj["pinned_version"] != float64(2) || alphaObj["frozen"] != true {
		t.Fatalf("pin: %v", alphaObj)
	}
	pinned, _ := alphaObj["pinned_url"].(string)
	pu, err := url.Parse(pinned)
	if err != nil || pu.Host != alphaHost || !strings.Contains(pu.Path, "/__preview/2/") {
		t.Fatalf("pinned url %q", pinned)
	}
	if r := a.on(t, "GET", pu.Host, pu.Path, nil, ""); r.status != 200 || !strings.Contains(string(r.body), "alpha two") {
		t.Fatalf("pinned page: %d %s", r.status, r.body)
	}
	// A future deadline while submissions are closed is refused.
	if r := a.api(t, "POST", "/v1/hack/events/"+slug+"/stage", map[string]string{"stage": "closed"}, org.key); r.status != 200 {
		t.Fatalf("closed: %d %s", r.status, r.body)
	}
	wantTS(t, "future deadline when closed", a.api(t, "PATCH", "/v1/hack/events/"+slug, map[string]string{"submission_deadline": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)}, org.key), 409, "submissions_closed_stage")

	// Per-team extension: alpha again, beta still frozen.
	later := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	if r := a.api(t, "PUT", "/v1/hack/events/"+slug+"/teams/"+alpha+"/deadline", map[string]string{"deadline": later}, org.key); r.status != 200 || r.json(t)["pinned_version"] != nil {
		t.Fatalf("extend: %d %s", r.status, r.body)
	}
	if r := a.deployTeam(t, alpha, k1, "alpha three"); r.status != 200 {
		t.Fatalf("deploy with extension: %d %s", r.status, r.body)
	}
	wantTS(t, "beta still frozen", a.deployTeam(t, beta, k3, "late"), 409, "submissions_closed")
	wantTS(t, "extension earlier than deadline", a.api(t, "PUT", "/v1/hack/events/"+slug+"/teams/"+beta+"/deadline", map[string]string{"deadline": "2020-01-01T00:00:00Z"}, org.key), 400, "deadline_not_later")

	// ---- organiser moderation ---------------------------------------------
	if r := a.api(t, "POST", "/v1/hack/events/"+slug+"/teams/"+alpha+"/takedown", map[string]string{"reason": "spam"}, org.key); r.status != 200 {
		t.Fatalf("takedown: %d %s", r.status, r.body)
	}
	if r := a.on(t, "GET", alphaHost, "/", nil, ""); r.status == 200 && strings.Contains(string(r.body), "alpha") {
		t.Fatalf("taken-down site still served: %d", r.status)
	}
	wantTS(t, "deploy taken down", a.deployTeam(t, alpha, k1, "x"), 403, "team_site_taken_down")
	if r := a.api(t, "POST", "/v1/hack/events/"+slug+"/teams/"+alpha+"/restore", nil, org.key); r.status != 200 {
		t.Fatalf("restore: %d %s", r.status, r.body)
	}
	if r := a.on(t, "GET", alphaHost, "/", nil, ""); r.status != 200 {
		t.Fatalf("restored site: %d", r.status)
	}
	// A platform take-down is not the organiser's to undo.
	var betaSite string
	if err := a.database.QueryRow(`SELECT s.id FROM sites s JOIN events e ON e.account_id = s.user_id WHERE e.slug = $1 AND s.name = $2`, slug, beta).Scan(&betaSite); err != nil {
		t.Fatal(err)
	}
	if err := db.SetSiteSuspended(context.Background(), a.database, betaSite, "platform says no"); err != nil {
		t.Fatal(err)
	}
	wantTS(t, "restore platform takedown", a.api(t, "POST", "/v1/hack/events/"+slug+"/teams/"+beta+"/restore", nil, org.key), 409, "platform_takedown")

	// Another event's organiser cannot touch this event's teams.
	org2 := a.newPerson(t, "org2")
	_ = a.makeEvent(t, org2)
	wantTS(t, "stranger takedown", a.api(t, "POST", "/v1/hack/events/"+slug+"/teams/"+alpha+"/takedown", nil, org2.key), 404, "event_not_found")
	wantTS(t, "stranger extension", a.api(t, "PUT", "/v1/hack/events/"+slug+"/teams/"+alpha+"/deadline", map[string]string{"deadline": ""}, org2.key), 404, "event_not_found")

	// ---- keys follow the person -------------------------------------------
	// Removing a person ends their key at once.
	if r := a.api(t, "DELETE", "/v1/hack/events/"+slug+"/people/"+a.uid(t, p2), nil, org.key); r.status != 204 {
		t.Fatalf("remove: %d %s", r.status, r.body)
	}
	if r := a.deployTeam(t, alpha, k2, "x"); r.status != 401 {
		t.Fatalf("removed person's key: %d %s", r.status, r.body)
	}
	// Moving a person to another team: the old key stops.
	if r := a.api(t, "POST", "/v1/hack/events/"+slug+"/teams/"+beta+"/members", map[string]string{"user_id": a.uid(t, p1)}, org.key); r.status != 200 {
		t.Fatalf("move: %d %s", r.status, r.body)
	}
	if r := a.deployTeam(t, alpha, k1, "x"); r.status != 401 {
		t.Fatalf("moved person's old key: %d %s", r.status, r.body)
	}
	var n int
	_ = a.database.QueryRow(`SELECT count(*) FROM event_team_keys b JOIN events e ON e.id = b.event_id WHERE e.slug = $1 AND b.user_id = $2`, slug, a.uid(t, p1)).Scan(&n)
	if n != 0 {
		t.Fatalf("stale key kept: %d", n)
	}
	// The organiser turns a key off.
	k3b := k3
	if r := a.api(t, "DELETE", "/v1/hack/events/"+slug+"/people/"+a.uid(t, p3)+"/key", nil, org.key); r.status != 204 {
		t.Fatalf("revoke: %d", r.status)
	}
	if r := a.api(t, "GET", "/v1/sites", nil, k3b); r.status != 401 {
		t.Fatalf("revoked key: %d", r.status)
	}

	// ---- a removed team's site goes, and its name is never reused ----------
	// Alpha emptied when its last member moved: it went, and its site too.
	if r := a.on(t, "GET", alphaHost, "/", nil, ""); r.status == 200 {
		t.Fatalf("emptied team's site still served")
	}
	if r := a.api(t, "DELETE", "/v1/hack/events/"+slug+"/teams/"+beta, nil, org.key); r.status != 204 {
		t.Fatalf("delete team: %d %s", r.status, r.body)
	}
	var live int
	_ = a.database.QueryRow(`SELECT count(*) FROM sites s JOIN events e ON e.account_id = s.user_id WHERE e.slug = $1 AND s.deleted_at IS NULL`, slug).Scan(&live)
	if live != 0 {
		t.Fatalf("removed teams' sites still live: %d", live)
	}
	// Back to building so people join and teams form again.
	if r := a.api(t, "POST", "/v1/hack/events/"+slug+"/stage", map[string]string{"stage": "building"}, org.key); r.status != 200 {
		t.Fatalf("building: %d %s", r.status, r.body)
	}
	if ev := a.api(t, "GET", "/v1/hack/events/"+slug, nil, org.key).json(t)["event"].(map[string]any); ev["submission_deadline"] != nil {
		t.Fatalf("back to building kept the passed deadline: %v", ev["submission_deadline"])
	}
	p4 := a.newPerson(t, "p4")
	a.join(t, slug, p4, org)
	again, _ := a.startTeam(t, slug, "Alpha", p4)
	if again == alpha {
		t.Fatalf("new team inherited the removed team's site name %q", again)
	}
}

func TestHackM2ConnectorBoundToTeam(t *testing.T) {
	a := newTeamSiteApp(t)
	org, p1, p2 := a.newPerson(t, "org"), a.newPerson(t, "p1"), a.newPerson(t, "p2")
	slug := a.makeEvent(t, org)
	a.join(t, slug, p1, org)
	a.join(t, slug, p2, org)
	alpha, _ := a.startTeam(t, slug, "Alpha", p1)
	beta, _ := a.startTeam(t, slug, "Beta", p2)
	markReady(t, a.certDir, slug)
	var alphaID, betaID string
	_ = a.database.QueryRow(`SELECT t.id FROM event_teams t JOIN events e ON e.id = t.event_id WHERE e.slug = $1 AND t.slug = $2`, slug, alpha).Scan(&alphaID)
	_ = a.database.QueryRow(`SELECT t.id FROM event_teams t JOIN events e ON e.id = t.event_id WHERE e.slug = $1 AND t.slug = $2`, slug, beta).Scan(&betaID)

	// my-teams lists the person's team.
	mt := a.api(t, "GET", "/v1/hack/my-teams", nil, p1.key).json(t)["teams"].([]any)
	if len(mt) != 1 || mt[0].(map[string]any)["team_id"] != alphaID {
		t.Fatalf("my-teams: %v", mt)
	}

	client := a.registerClient(t, testRedirect)
	verifier, challenge := newVerifier()
	q := authorizeQuery(client, testRedirect, challenge)
	q.Set("resource", a.srv.URL+"/mcp")
	page := a.do(t, http.MethodGet, "/oauth/authorize?"+q.Encode(), nil, nil)
	m := connectDataRe.FindSubmatch(page.body)
	if m == nil || !strings.Contains(string(m[1]), `"hack":true`) {
		t.Fatalf("consent data: %s", m)
	}
	csrf := strings.Split(strings.Split(string(m[1]), `"csrf":"`)[1], `"`)[0]
	decide := func(team string) resp {
		return a.do(t, http.MethodPost, "/oauth/authorize/decision", jsonBody(map[string]string{
			"query": q.Encode(), "csrf": csrf, "decision": "allow", "team_id": team,
		}), map[string]string{"Content-Type": "application/json", "X-API-Key": p1.key, "Origin": a.srv.URL})
	}
	wantTS(t, "no team", decide(""), 400, "team_required")
	wantTS(t, "other team", decide(betaID), 403, "not_on_team")
	r := decide(alphaID)
	if r.status != 200 {
		t.Fatalf("decide: %d %s", r.status, r.body)
	}
	code := codeFrom(t, r.json(t)["redirect_to"].(string))
	tok := a.form(t, "/oauth/token", url.Values{
		"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {testRedirect},
		"client_id": {client}, "code_verifier": {verifier}, "resource": {a.srv.URL + "/mcp"},
	}, nil)
	if tok.status != 200 {
		t.Fatalf("token: %d %s", tok.status, tok.body)
	}
	access := tok.json(t)["access_token"].(string)
	call := func(name string, args map[string]any) (string, bool) {
		rr := a.rpc(t, access, "tools/call", map[string]any{"name": name, "arguments": args})
		if rr.status != 200 {
			t.Fatalf("mcp %s: %d %s", name, rr.status, rr.body)
		}
		text, _, isErr := toolResultOf(t, rr)
		return text, isErr
	}
	if text, isErr := call("create_site", map[string]any{"site": alpha, "files": map[string]string{"index.html": "<h1>via connector</h1>"}}); isErr {
		t.Fatalf("connector deploy: %s", text)
	}
	if text, isErr := call("create_site", map[string]any{"site": beta, "files": map[string]string{"index.html": "x"}}); !isErr || !strings.Contains(text, "team") {
		t.Fatalf("connector other team: %v %s", isErr, text)
	}
	if text, isErr := call("list_sites", map[string]any{}); isErr || strings.Contains(text, beta) {
		t.Fatalf("mcp list: %v %s", isErr, text)
	}
	if text, isErr := call("delete_site", map[string]any{"site": alpha}); !isErr {
		t.Fatalf("connector delete: %s", text)
	}
	// Off the team, the connection stops publishing.
	if r := a.api(t, "POST", "/v1/hack/events/"+slug+"/teams/leave", nil, p1.key); r.status != 200 {
		t.Fatalf("leave: %d %s", r.status, r.body)
	}
	if text, isErr := call("update_site", map[string]any{"site": alpha, "files": map[string]string{"index.html": "x"}}); !isErr {
		t.Fatalf("connector after leaving: %s", text)
	}
}

// A deploy let in before the deadline is pinned; nothing after it lands.
func TestHackM2DeadlineRace(t *testing.T) {
	a := newTeamSiteApp(t)
	org, p1 := a.newPerson(t, "org"), a.newPerson(t, "p1")
	slug := a.makeEvent(t, org)
	a.join(t, slug, p1, org)
	alpha, _ := a.startTeam(t, slug, "Alpha", p1)
	markReady(t, a.certDir, slug)
	k1 := a.teamKey(t, slug, p1)
	if r := a.deployTeam(t, alpha, k1, "v1"); r.status != 201 {
		t.Fatalf("deploy: %d %s", r.status, r.body)
	}
	// The deadline a moment away; deploy in a loop across it.
	dl := time.Now().Add(1500 * time.Millisecond)
	if r := a.api(t, "PATCH", "/v1/hack/events/"+slug, map[string]string{"submission_deadline": dl.UTC().Format(time.RFC3339Nano)}, org.key); r.status != 200 {
		t.Fatalf("deadline: %d %s", r.status, r.body)
	}
	lastOK := 1
	for i := 2; time.Now().Before(dl.Add(700 * time.Millisecond)); i++ {
		time.Sleep(120 * time.Millisecond)
		r := a.deployTeam(t, alpha, k1, "v")
		if r.status == 200 {
			lastOK = int(r.json(t)["active_version"].(float64))
		} else if r.status != 409 {
			t.Fatalf("deploy %d: %d %s", i, r.status, r.body)
		}
	}
	if err := db.PinDueTeams(context.Background(), a.database, ""); err != nil {
		t.Fatal(err)
	}
	var pinned sql.NullInt64
	if err := a.database.QueryRow(`SELECT t.pinned_version FROM event_teams t JOIN events e ON e.id = t.event_id WHERE e.slug = $1`, slug).Scan(&pinned); err != nil {
		t.Fatal(err)
	}
	if !pinned.Valid || int(pinned.Int64) != lastOK {
		t.Fatalf("pinned %v, last accepted deploy %d", pinned, lastOK)
	}
}

// The platform's take-down of an event takes its team sites down with it.
func TestHackM2EventTakedownTakesTeamSitesDown(t *testing.T) {
	a := newTeamSiteApp(t)
	org, p1 := a.newPerson(t, "org"), a.newPerson(t, "p1")
	slug := a.makeEvent(t, org)
	a.join(t, slug, p1, org)
	alpha, _ := a.startTeam(t, slug, "Alpha", p1)
	markReady(t, a.certDir, slug)
	k1 := a.teamKey(t, slug, p1)
	if r := a.deployTeam(t, alpha, k1, "up"); r.status != 201 {
		t.Fatalf("deploy: %d %s", r.status, r.body)
	}
	host := alpha + "." + slug + "." + tsDomain
	if r := a.api(t, "POST", "/v1/admin/hack/events/"+slug+"/takedown", map[string]string{"reason": "abuse"}, a.admin); r.status != 200 {
		t.Fatalf("takedown: %d %s", r.status, r.body)
	}
	if r := a.on(t, "GET", host, "/", nil, ""); r.status == 200 && strings.Contains(string(r.body), "up") {
		t.Fatalf("team site served while the event is taken down")
	}
	wantTS(t, "deploy while taken down", a.deployTeam(t, alpha, k1, "x"), 403, "")
	// Reads stop too: the team key is as suspended as the event.
	wantTS(t, "read while taken down", a.api(t, "GET", "/v1/sites", nil, k1), 403, "account_suspended")
	wantTS(t, "data while taken down", a.api(t, "GET", "/v1/sites/"+alpha+"/data", nil, k1), 403, "account_suspended")
	if r := a.api(t, "POST", "/v1/admin/hack/events/"+slug+"/restore", nil, a.admin); r.status != 200 {
		t.Fatalf("restore: %d %s", r.status, r.body)
	}
	if r := a.on(t, "GET", host, "/", nil, ""); r.status != 200 {
		t.Fatalf("team site after restore: %d", r.status)
	}
}

// Review round 1: names are never reused, a solo team cannot throw its
// submission away, extensions only extend, bare names stay in their event.
func TestHackM2ReviewRound1(t *testing.T) {
	a := newTeamSiteApp(t)
	org, p1, p2 := a.newPerson(t, "org"), a.newPerson(t, "p1"), a.newPerson(t, "p2")
	slug := a.makeEvent(t, org)
	a.join(t, slug, p1, org)
	a.join(t, slug, p2, org)
	solo, _ := a.startTeam(t, slug, "Solo", p1)
	markReady(t, a.certDir, slug)
	k1 := a.teamKey(t, slug, p1)
	if r := a.deployTeam(t, solo, k1, "mine"); r.status != 201 {
		t.Fatalf("deploy: %d %s", r.status, r.body)
	}
	// Anonymous bare names on the apex resolve nothing on this platform.
	if r := a.api(t, "GET", "/v1/sites/"+solo+"/data", nil, ""); r.status/100 == 2 {
		t.Fatalf("bare anonymous read: %d %s", r.status, r.body)
	}
	if r := a.api(t, "GET", "/v1/sites/"+solo+"/data", nil, k1); r.status != 200 {
		t.Fatalf("team key bare read of its own site: %d %s", r.status, r.body)
	}
	// Extensions only extend: a later event deadline wins over an older one.
	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	if r := a.api(t, "PUT", "/v1/hack/events/"+slug+"/teams/"+solo+"/deadline", map[string]string{"deadline": past}, org.key); r.status != 409 {
		t.Fatalf("extension without an event deadline: %d %s", r.status, r.body)
	}
	soon := time.Now().Add(time.Hour).UTC()
	a.api(t, "PATCH", "/v1/hack/events/"+slug, map[string]string{"submission_deadline": soon.Format(time.RFC3339)}, org.key)
	if r := a.api(t, "PUT", "/v1/hack/events/"+slug+"/teams/"+solo+"/deadline", map[string]string{"deadline": soon.Add(time.Hour).Format(time.RFC3339)}, org.key); r.status != 200 {
		t.Fatalf("extension: %d %s", r.status, r.body)
	}
	a.api(t, "PATCH", "/v1/hack/events/"+slug, map[string]string{"submission_deadline": soon.Add(24 * time.Hour).Format(time.RFC3339)}, org.key)
	if r := a.deployTeam(t, solo, k1, "still open"); r.status != 200 {
		t.Fatalf("older extension froze the team early: %d %s", r.status, r.body)
	}
	// After the deadline a solo team's only member cannot leave (and take
	// the pinned submission with the team).
	a.api(t, "PUT", "/v1/hack/events/"+slug+"/teams/"+solo+"/deadline", map[string]string{"deadline": ""}, org.key)
	a.api(t, "PATCH", "/v1/hack/events/"+slug, map[string]string{"submission_deadline": time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)}, org.key)
	wantTS(t, "leave after deadline", a.api(t, "POST", "/v1/hack/events/"+slug+"/teams/leave", nil, p1.key), 409, "submissions_closed")
	// Pinned: even emptied by the organiser, the team (and its site) stays.
	a.api(t, "GET", "/v1/hack/events/"+slug+"/teams", nil, org.key)
	if r := a.api(t, "DELETE", "/v1/hack/events/"+slug+"/teams/"+solo+"/members/"+a.uid(t, p1), nil, org.key); r.status != 204 {
		t.Fatalf("take off: %d %s", r.status, r.body)
	}
	var n int
	_ = a.database.QueryRow(`SELECT count(*) FROM event_teams t JOIN events e ON e.id = t.event_id WHERE e.slug = $1 AND t.slug = $2`, slug, solo).Scan(&n)
	if n != 1 {
		t.Fatalf("pinned team removed when emptied")
	}
	// Explicit removal still works, and the name is never reused, even
	// after the site is purged.
	if r := a.api(t, "DELETE", "/v1/hack/events/"+slug+"/teams/"+solo, nil, org.key); r.status != 204 {
		t.Fatalf("delete team: %d", r.status)
	}
	if _, err := a.database.Exec(`DELETE FROM sites s USING events e WHERE e.account_id = s.user_id AND e.slug = $1`, slug); err != nil {
		t.Fatal(err)
	}
	a.api(t, "POST", "/v1/hack/events/"+slug+"/stage", map[string]string{"stage": "building"}, org.key)
	if again, _ := a.startTeam(t, slug, "Solo", p2); again == solo {
		t.Fatalf("team name reused after purge: %s", again)
	}
	// The event name is kept for good once it had a team.
	var accountID string
	_ = a.database.QueryRow(`SELECT account_id FROM events WHERE slug = $1`, slug).Scan(&accountID)
	if r := a.api(t, "DELETE", "/v1/admin/hack/events/"+slug, nil, a.admin); r.status != 204 {
		t.Fatalf("admin delete: %d %s", r.status, r.body)
	}
	t.Cleanup(func() { _, _ = a.database.Exec(`DELETE FROM event_used_names WHERE event_slug = $1`, slug) })
	r := a.api(t, "GET", "/v1/hack/names/"+slug, nil, org.key)
	if r.json(t)["available"] != false {
		t.Fatalf("deleted event's name available again: %s", r.body)
	}
}
