package handler

import (
	"context"
	"crypto/rand"
	"crypto/x509"
	"database/sql"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/internal/auth"
	db "github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/edition"
	"github.com/vsriram/simple-host/internal/email"
	"github.com/vsriram/simple-host/internal/hackhttp"
	"github.com/vsriram/simple-host/internal/storage"
)

// uniqueSlug is hack_test.go's own (internal/hackhttp now); duplicated here
// for the same reason as the rest of this file.
func uniqueSlug() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return "hax" + hex.EncodeToString(b[:])
}

// bridgeTeamApp (cleanup 3.2 slice 3): the Host+Hack wiring
// imagescan_test.go and sso_test.go need now that HackHandler and its
// teamSiteApp-style fixture moved to internal/hackhttp
// (hack_m2_test.go/hack_test.go). Those two files are not hack-prefixed, so
// this slice's move left them stranded: a _test.go file's symbols, even
// exported ones, are never visible to another package, so reusing
// hack_m2_test.go's teamSiteApp is impossible regardless of naming. This is
// the brief's own suggested fallback ("their own minimal site+hack wiring
// copied in") — a copy of newTeamSiteAppMailer's wiring, built entirely
// from already-exported constructors (connectorApp's own on the Host side,
// hackhttp.NewHackHandler across the boundary). Because it lives in package
// handler, it can still embed *connectorApp directly, unlike hackhttp's own
// copy.
const bridgeTSDomain = "simple-hack.test"

type bridgeTeamApp struct {
	*connectorApp
	sites   *SiteHandler
	users   *UserHandler
	hack    *hackhttp.HackHandler
	certDir string
}

// newBridgeTeamApp wires Host+Hack the way cmd/server/main.go does, for the
// two non-hack-prefixed tests that need a real HackHandler alongside a real
// SiteHandler.
func newBridgeTeamApp(t *testing.T) *bridgeTeamApp {
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
	oldDomain := db.PlatformDomain()
	db.SetPlatformDomain(bridgeTSDomain)
	t.Cleanup(func() { db.SetPlatformDomain(oldDomain) })

	ed := edition.ForKind(edition.Hack)
	adminKey, _ := auth.GenerateAPIKey()
	adminID, err := db.EnsureAdminUser(context.Background(), database)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	a := &bridgeTeamApp{connectorApp: &connectorApp{database: database, admin: adminKey, mux: mux}, certDir: certDir}
	authMW := auth.Middleware(adminKey, adminID, database, 180*24*time.Hour)
	var root http.Handler
	a.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { root.ServeHTTP(w, r) }))
	t.Cleanup(a.srv.Close)
	mailer := email.NewResendSender("", "test@example.com")
	users := NewUserHandler(database, mailer, a.srv.URL, ed)
	a.users = users
	users.Register(mux, authMW, NoticeMiddleware("1.0.0"))
	a.sites = NewSiteHandler(ed, "", database, disk, bridgeTSDomain, "sites."+bridgeTSDomain, "cname."+bridgeTSDomain, "", "", adminKey, nil, 0, "on", adminID, mailer, users.EmailLimiter())
	a.sites.Register(mux, authMW, NoticeMiddleware("1.0.0"))
	t.Cleanup(a.sites.WaitNotifications)
	a.sites.SetPersonHosts("canonical")
	a.sites.SetSiteHosts("canonical", certDir)
	a.hack = hackhttp.NewHackHandler(database, a.srv.URL, bridgeTSDomain)
	a.hack.SetKeyResolver(func(ctx context.Context, key string, idle time.Duration) (db.User, error) {
		return db.GetUserByAPIKey(ctx, database, key, idle)
	})
	a.hack.ShareAssets(a.sites.Pages())
	a.hack.SetSites(a.sites)
	a.hack.Register(mux, authMW)
	a.sites.SetHackEventPage(hackhttp.HackEventPage(database, a.srv.URL, a.sites.TeamSiteURL, a.sites.TeamSitesReady, hackhttp.NewHackEventPages(EmbeddedStatic())))
	a.sites.SetHackScreenshot(hackhttp.HackScreenshot(database, a.sites.TeamSitesReady))
	gated := a.sites.LegacyStorageCutoffGate(a.sites.HackLegacyStorageGate(auth.ScopeGate(database, mux, true, 180*24*time.Hour)))
	a.conn = NewConnectorHandler(database, a.srv.URL, adminKey, bridgeTSDomain, "sites."+bridgeTSDomain, "1.0.0", gated, true, ed)
	a.conn.Register(mux, authMW)
	a.conn.SetHackTeams(true)
	a.conn.SetSignInAlerts(users.SignInAlerts())
	app := SecurityHeaders(CORS(a.conn.BearerAuth(gated)))
	root = a.sites.FamilyHosts(app, a.sites.SiteBaseHosts(a.sites.BoundSubdomains(app, a.sites.SiteHosts(app, a.sites.PersonHosts(app, a.sites.LegacyHostRedirect(app))))))
	return a
}

// on sends a request to host (as nginx would, over HTTPS). teamSiteApp's own
// method (internal/hackhttp/hack_m2_test.go), duplicated here for the same
// cross-package reason as the type itself.
func (a *bridgeTeamApp) on(t *testing.T, method, host, path string, body any, key string) resp {
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
func (a *bridgeTeamApp) api(t *testing.T, method, path string, body any, key string) resp {
	return a.on(t, method, "", path, body, key)
}

// makeEvent creates and opens an event for org (teamSiteApp's own method,
// duplicated here for the same cross-package reason as the type itself).
func (a *bridgeTeamApp) makeEvent(t *testing.T, org person) string {
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

// createEvent creates a draft event without opening it (hackApp's own
// semantics, duplicated here: TestImageScanEventIcon needs a draft, not an
// opened, event).
func (a *bridgeTeamApp) createEvent(t *testing.T, p person, slug string, extra map[string]any) resp {
	t.Helper()
	body := map[string]any{
		"slug": slug, "title": "Spring hack", "organiser_name": "Ada Lovelace", "organisation": "Analytic Club",
		"contact_email": "ada@example.com", "purpose": "build something kind", "expected_participants": 80,
		"starts_at": "2026-10-01", "ends_at": "2026-10-03", "time_zone": "UTC",
	}
	for k, v := range extra {
		if v == nil {
			delete(body, k)
		} else {
			body[k] = v
		}
	}
	r := a.api(t, "POST", "/v1/hack/events", body, p.key)
	if r.status == http.StatusCreated {
		t.Cleanup(func() {
			var accountID string
			_ = a.database.QueryRow(`SELECT account_id FROM events WHERE slug = $1`, slug).Scan(&accountID)
			_, _ = a.database.Exec(`DELETE FROM events WHERE slug = $1`, slug)
			if accountID != "" {
				_, _ = a.database.Exec(`DELETE FROM users WHERE id = $1`, accountID)
			}
		})
	}
	return r
}

func (a *bridgeTeamApp) join(t *testing.T, slug string, p person, org person) {
	t.Helper()
	ev := a.api(t, "GET", "/v1/hack/events/"+slug, nil, org.key).json(t)
	code := ev["organiser"].(map[string]any)["join_code"].(string)
	if r := a.api(t, "POST", "/v1/hack/join/"+code, map[string]any{"accept_coc": true, "display_name": "P"}, p.key); r.status != 200 && r.status != 201 {
		t.Fatalf("join: %d %s", r.status, r.body)
	}
}

func (a *bridgeTeamApp) startTeam(t *testing.T, slug, name string, p person) (teamSlug, code string) {
	t.Helper()
	r := a.api(t, "POST", "/v1/hack/events/"+slug+"/teams", map[string]string{"name": name}, p.key)
	if r.status != http.StatusCreated {
		t.Fatalf("team: %d %s", r.status, r.body)
	}
	m := r.json(t)
	return m["slug"].(string), m["code"].(string)
}

func (a *bridgeTeamApp) teamKey(t *testing.T, slug string, p person) string {
	t.Helper()
	r := a.api(t, "POST", "/v1/hack/events/"+slug+"/key", nil, p.key)
	if r.status != http.StatusCreated {
		t.Fatalf("key: %d %s", r.status, r.body)
	}
	return r.json(t)["key"].(string)
}

// wantTS is hack_m2_test.go's own (internal/hackhttp now), duplicated here
// (cleanup 3.2 slice 3): imagescan_test.go, notify_site_events_test.go and
// sso_test.go all need it.
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

// deployBody is hack_m2_test.go's own (internal/hackhttp now); duplicated
// here for the same reason as the rest of this file.
func deployBody(text string) map[string]any {
	return map[string]any{"files": map[string]string{"index.html": "<h1>" + text + "</h1>"}}
}

func (a *bridgeTeamApp) deployTeam(t *testing.T, site, key, text string) resp {
	t.Helper()
	return a.api(t, "PUT", "/v1/sites/"+site+"/files?create=1", deployBody(text), key)
}

// bridgeHackNotifyFixture is hack_notifications_test.go's own
// hackNotifyFixture (internal/hackhttp), duplicated here with a
// *bridgeTeamApp (cleanup 3.2 slice 3): notify_site_events_test.go's
// TestNotifySiteEventsHack is the one non-hack-prefixed test that needs a
// Hack team site wired for webhook delivery. notificationReceiver,
// testPasscodeKey and notifyTestSecret need no duplicate here (they are
// this package's own, notify_test.go/passcode_test.go, unchanged by the
// move).
type bridgeHackNotifyFixture struct {
	a                          *bridgeTeamApp
	x                          *notificationReceiver
	org, member, other         person
	slug, team, key, sid, base string
}

func newBridgeHackNotifyFixture(t *testing.T) bridgeHackNotifyFixture {
	t.Helper()
	isolateQueueTest(t)
	a := newBridgeTeamApp(t)
	t.Cleanup(a.sites.WaitNotifications)
	x := newNotificationReceiver(t)
	if err := a.sites.SetPasscodeKey(testPasscodeKey); err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(x.server.Certificate())
	a.sites.SetNotifyClientForTest(roots)
	a.conn.SetEventsBackend(a.sites.EventsBackend())
	org, member, other := a.newPerson(t, "hn-org"), a.newPerson(t, "hn-member"), a.newPerson(t, "hn-other")
	slug := a.makeEvent(t, org)
	a.join(t, slug, member, org)
	a.join(t, slug, other, org)
	team, code := a.startTeam(t, slug, "Notifications", member)
	wantTS(t, "join teammate", a.api(t, "POST", "/v1/hack/events/"+slug+"/teams/join", map[string]string{"code": code}, other.key), 200, "")
	markReady(t, a.certDir, slug)
	key := a.teamKey(t, slug, member)
	wantTS(t, "deploy", a.deployTeam(t, team, key, "notify"), 201, "")
	var sid string
	if err := a.database.QueryRow(`SELECT s.id FROM sites s JOIN events e ON e.account_id=s.user_id WHERE e.slug=$1 AND s.name=$2 AND s.deleted_at IS NULL`, slug, team).Scan(&sid); err != nil {
		t.Fatal(err)
	}
	return bridgeHackNotifyFixture{a, x, org, member, other, slug, team, key, sid, "/v1/sites/" + team + "/notifications"}
}

func (f bridgeHackNotifyFixture) body() map[string]any {
	return map[string]any{"url": f.x.server.URL, "secret": notifyTestSecret, "events": []string{"record.added", "record.changed", "file.uploaded"}, "auth_header": map[string]string{"name": "Authorization", "value": "Bearer receiver"}}
}

func (f bridgeHackNotifyFixture) create(t *testing.T, base string, p person) string {
	t.Helper()
	r := f.a.api(t, "POST", base, f.body(), p.key)
	if r.status != 201 {
		t.Fatalf("create notification: status %d", r.status)
	}
	return r.json(t)["notification"].(map[string]any)["id"].(string)
}

func (f bridgeHackNotifyFixture) drain(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for f.a.sites.PendingNotificationsForTest() > 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if f.a.sites.PendingNotificationsForTest() > 0 {
		t.Fatal("emit timeout")
	}
	for i := 0; i < 100; i++ {
		if !f.a.sites.DeliverNotificationForTest(context.Background()) {
			return
		}
	}
	t.Fatal("delivery queue did not drain")
}

func (a *bridgeTeamApp) uid(t *testing.T, p person) string {
	t.Helper()
	var id string
	if err := a.database.QueryRow(`SELECT id FROM users WHERE username = $1`, p.email).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}
