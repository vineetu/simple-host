package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	accountapp "github.com/vsriram/simple-host/internal/app/accounts"
	"github.com/vsriram/simple-host/internal/app/hack/entries"
	"github.com/vsriram/simple-host/internal/app/hack/events"
	"github.com/vsriram/simple-host/internal/app/operations"
	"github.com/vsriram/simple-host/internal/auth"
	"github.com/vsriram/simple-host/internal/config"
	db "github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/edition"
	"github.com/vsriram/simple-host/internal/email"
	"github.com/vsriram/simple-host/internal/hackhttp"
	"github.com/vsriram/simple-host/internal/handler"
	"github.com/vsriram/simple-host/internal/model"
	accountrepo "github.com/vsriram/simple-host/internal/repository/accounts"
	legacyrepo "github.com/vsriram/simple-host/internal/repository/legacy"
	"github.com/vsriram/simple-host/internal/repository/sqlkit"
	"github.com/vsriram/simple-host/internal/storage"
)

// Shared composition fixtures for tests spanning Host HTTP, Hack HTTP and MCP.
type person struct{ email, key string }

type resp struct {
	status int
	header http.Header
	body   []byte
}

func (r resp) json(t *testing.T) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(r.body, &out); err != nil {
		t.Fatalf("not JSON (%d): %s", r.status, r.body)
	}
	return out
}

func jsonBody(v any) io.Reader {
	b, _ := json.Marshal(v)
	return strings.NewReader(string(b))
}

func hackHandleCheckerForTest(ctx context.Context, q sqlkit.Querier, h string) (hackhttp.HandleVerdict, error) {
	v, err := handler.JudgeHandle(ctx, q, "", h)
	return hackhttp.HandleVerdict{Status: v.Status, Code: v.Code}, err
}

type replyMailer struct {
	mu   sync.Mutex
	sent []string // to|replyTo|subject|text
}

func (m *replyMailer) SendSignInCode(string, string, string) error { return nil }
func (m *replyMailer) SendNoticeReplyTo(to, replyTo, subject, text string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, to+"|"+replyTo+"|"+subject+"|"+text)
	return nil
}

type failingReplyMailer struct{ replyMailer }

func (m *failingReplyMailer) SendNoticeReplyTo(to, replyTo, subject, text string) error {
	return errors.New("mail server down")
}

// last returns the newest mail about site, or "".
func (m *replyMailer) last(site string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := len(m.sent) - 1; i >= 0; i-- {
		if strings.Contains(m.sent[i], "Your site "+site+" ") {
			return m.sent[i]
		}
	}
	return ""
}

type hackApp struct {
	database *sql.DB
	admin    string
	mux      *http.ServeMux
	srv      *httptest.Server
	hack     *hackhttp.HackHandler
}

func newHackApp(t *testing.T, eds ...*edition.Edition) *hackApp {
	t.Helper()
	var ed *edition.Edition
	if len(eds) > 0 {
		ed = eds[0]
	} else {
		ed = edition.ForKind(edition.Hack)
	}
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
	adminKey, _ := auth.GenerateAPIKey()
	adminID, err := legacyrepo.EnsureAdminUser(context.Background(), database)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	var root http.Handler
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { root.ServeHTTP(w, r) }))
	t.Cleanup(srv.Close)
	authMW := auth.Middleware(adminKey, adminID, accountapp.New(database), ed.Limits().KeyIdleExpiry)
	users := handler.NewUserHandler(database, accountapp.New(database), email.NewResendSender("", "test@example.com"), srv.URL, ed)
	users.Register(mux, authMW, handler.NoticeMiddleware("1.0.0"))
	h := hackhttp.NewHackHandler(database, srv.URL, "simple-hack.test", ed)
	h.ShareAssets(handler.NewPages(ed, srv.URL))
	h.SetHandleChecker(hackHandleCheckerForTest)
	h.SetKeyResolver(func(ctx context.Context, key string, idle time.Duration) (model.User, error) {
		return db.GetUserByAPIKey(ctx, database, key, idle)
	})
	h.Register(mux, authMW)
	root = mux
	return &hackApp{database: database, admin: adminKey, mux: mux, srv: srv, hack: h}
}

func (a *hackApp) at(t *testing.T, method, path string, body any, headers map[string]string) resp {
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
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return resp{res.StatusCode, res.Header, b}
}

func (a *hackApp) key(p person) map[string]string {
	return map[string]string{"X-API-Key": p.key}
}

func (a *hackApp) adminH() map[string]string {
	return map[string]string{"X-API-Key": a.admin}
}

func (a *hackApp) newPerson(t *testing.T, label string) person {
	t.Helper()
	addr := label + "-" + strings.ReplaceAll(uniqueSlug(), "-", "") + "@example.com"
	key, _ := auth.GenerateAPIKey()
	u, err := accountrepo.CreateUser(context.Background(), a.database, addr, key, false)
	if err != nil {
		t.Fatal(err)
	}
	handle := "u" + uniqueSlug()
	if _, err := a.database.Exec(`UPDATE users SET handle = $1 WHERE id = $2`, handle, u.ID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = a.database.Exec(`DELETE FROM users WHERE id = $1`, u.ID) })
	return person{email: addr, key: key}
}

// newKeyOnlyPerson makes an account with an "@"-less username, the shape of
// Simple Hack's key-only test accounts (no email on file): CreateUser places
// no format constraint on username beyond uniqueness, so this is a
// first-class way to get a person an announcement's email_participants
// cannot reach.
func (a *hackApp) newKeyOnlyPerson(t *testing.T, label string) person {
	t.Helper()
	username := "k" + label + strings.ReplaceAll(uniqueSlug(), "-", "")
	key, _ := auth.GenerateAPIKey()
	u, err := accountrepo.CreateUser(context.Background(), a.database, username, key, false)
	if err != nil {
		t.Fatal(err)
	}
	handle := "u" + uniqueSlug()
	if _, err := a.database.Exec(`UPDATE users SET handle = $1 WHERE id = $2`, handle, u.ID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = a.database.Exec(`DELETE FROM users WHERE id = $1`, u.ID) })
	return person{email: username, key: key}
}

func (a *hackApp) userID(t *testing.T, p person) string {
	t.Helper()
	var id string
	if err := a.database.QueryRow(`SELECT id FROM users WHERE username = $1`, p.email).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func (a *hackApp) cleanupEvent(slug string) {
	var accountID string
	_ = a.database.QueryRow(`SELECT account_id FROM events WHERE slug = $1`, slug).Scan(&accountID)
	_, _ = a.database.Exec(`DELETE FROM events WHERE slug = $1`, slug)
	if accountID != "" {
		_, _ = a.database.Exec(`DELETE FROM users WHERE id = $1`, accountID)
	}
}

type mailbox struct {
	mu      sync.Mutex
	codes   map[string]string // address -> last sign-in code
	notices []string          // "to|subject|text"
}

func (m *mailbox) SendSignInCode(to, code, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.codes == nil {
		m.codes = map[string]string{}
	}
	m.codes[to] = code
	return nil
}

func (m *mailbox) SendNotice(to, subject, text string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.notices = append(m.notices, to+"|"+subject+"|"+text)
	return nil
}

// to returns the notices sent to address.
func (m *mailbox) to(address string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for _, n := range m.notices {
		if strings.HasPrefix(n, address+"|") {
			out = append(out, n)
		}
	}
	return out
}

func tarGzOf(t *testing.T, n int) string {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range map[string][]byte{"index.html": []byte("<h1>big</h1>"), "big.bin": make([]byte, n)} {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func wantCode(t *testing.T, what string, r resp, status int, code string) {
	t.Helper()
	if r.status != status {
		t.Fatalf("%s: %d %s, want %d %s", what, r.status, r.body, status, code)
	}
	if code != "" {
		var body struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(r.body, &body)
		if body.Code != code {
			t.Fatalf("%s: code %q (%s), want %q", what, body.Code, r.body, code)
		}
	}
}

func waitBlocked() { time.Sleep(300 * time.Millisecond) }

func uniqueSlug() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return "hax" + hex.EncodeToString(b[:])
}

// setHackAppLimits overwrites a's already-built hackhttp.HackHandler's own limits
// snapshot in place (moved from internal/handler/limits_test.go, cleanup
// 3.2 slice 3: hackApp, its only caller, moved here; limits_test.go's
// setAppLimits/setConnectorAppLimits stay there for the fixtures that stay
// there). See that file's withLimits/otherValue for the rest of the
// env-knob test machinery this pairs with.
func (a *hackApp) createBody(slug string, extra map[string]any) map[string]any {
	body := map[string]any{
		"slug":                  slug,
		"title":                 "Spring hack",
		"organiser_name":        "Ada Lovelace",
		"organisation":          "Analytic Club",
		"contact_email":         "ada@example.com",
		"purpose":               "build something kind",
		"expected_participants": 80,
		"starts_at":             "2026-10-01",
		"ends_at":               "2026-10-03",
		"time_zone":             "UTC",
	}
	for k, v := range extra {
		if v == nil {
			delete(body, k)
		} else {
			body[k] = v
		}
	}
	return body
}

func (a *hackApp) createEvent(t *testing.T, p person, slug string, extra map[string]any) resp {
	t.Helper()
	r := a.at(t, "POST", "/v1/hack/events", a.createBody(slug, extra), a.key(p))
	if r.status == http.StatusCreated {
		t.Cleanup(func() { a.cleanupEvent(slug) })
	}
	return r
}

func (a *hackApp) openEvent(t *testing.T, p person, slug string) {
	t.Helper()
	r := a.at(t, "POST", "/v1/hack/events/"+slug+"/stage", map[string]string{"stage": "open"}, a.key(p))
	if r.status != 200 {
		t.Fatalf("open: %d %s", r.status, r.body)
	}
}

// giveSubmittedEntry writes (or updates) a minimal, already-decided entry
// for teamID directly via SQL: submitted_at and submit_settled_at both set.
// Entries and participant journey slice 1 (zv-hack-entry-summary.sql) made
// judging eligibility depend on EntryJudgeableSQL -- a submitted or settled
// entry, not excluded -- so a team with no entry row at all is never
// judgeable. Many tests outside the entries package score a team directly
// to exercise leave/removal/judging behaviour rather than the entries API
// itself; this is their fixture, the same direct INSERT newJudgingWorld
// uses (hack_judging_test.go). It never touches title/tagline/etc, so it is
// safe to call after an entry already exists (e.g. via PUT .../entry).
func (a *hackApp) giveSubmittedEntry(t *testing.T, slug, teamID string) {
	t.Helper()
	if _, err := a.database.Exec(`
		INSERT INTO event_entries (team_id, event_id, submitted_at, submit_settled_at)
		VALUES ($1, (SELECT id FROM events WHERE slug = $2), now(), now())
		ON CONFLICT (team_id) DO UPDATE SET submitted_at = now(), submit_settled_at = now()`,
		teamID, slug); err != nil {
		t.Fatal(err)
	}
}

func jsonArr(t *testing.T, r resp) []map[string]any {
	t.Helper()
	var out []map[string]any
	if err := json.Unmarshal(r.body, &out); err != nil {
		t.Fatalf("not array (%d): %s", r.status, r.body)
	}
	return out
}

// createEvent creates a draft event without opening it (hackApp's own
// semantics, duplicated here: TestImageScanEventIcon needs a draft, not an
// opened, event).
func (a *teamSiteApp) createEvent(t *testing.T, p person, slug string, extra map[string]any) resp {
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

func get(t *testing.T, h http.Handler, host, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if host != "" {
		req.Host = host
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func chromeTestHandler(kind edition.Kind) *handler.SiteHandler {
	return chromeTestHandlerWithBase(kind, "")
}

func chromeTestHandlerWithBase(kind edition.Kind, base string) *handler.SiteHandler {
	return handler.NewSiteHandler(edition.ForKind(kind), base, nil, nil, "simple-host.app", "sites.simple-host.app", "", "", "", "", nil, 0, "", "", nil, nil)
}

func chromeTestMux(t *testing.T, kind edition.Kind) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	handler.RegisterUIRoutes(mux, "https://simple-host.app", chromeTestHandler(kind))
	return mux
}

var (
	chromeScriptTagRe  = regexp.MustCompile(`<script\b[^>]*>`)
	chromeScriptSrcRe  = regexp.MustCompile(`script-src ([^;]*)`)
	chromeCSPNonceRe   = regexp.MustCompile(`'nonce-([A-Za-z0-9+/=]+)'`)
	chromeInlineAttrRe = regexp.MustCompile(`<[a-zA-Z][^<>]*\son[a-z]+\s*=`)
)

// assertStrictScriptCSP checks one apex HTML response: script-src carries a
// nonce and no 'unsafe-inline', every inline <script> carries that nonce,
// and no element uses an on*= handler attribute (a nonce does not cover
// those).
func assertStrictScriptCSP(t *testing.T, label string, rec *httptest.ResponseRecorder) {
	t.Helper()
	csp := rec.Header().Get("Content-Security-Policy")
	m := chromeScriptSrcRe.FindStringSubmatch(csp)
	if m == nil {
		t.Errorf("%s: no script-src in CSP %q", label, csp)
		return
	}
	if strings.Contains(m[1], "unsafe-inline") {
		t.Errorf("%s: script-src allows 'unsafe-inline': %q", label, m[1])
	}
	n := chromeCSPNonceRe.FindStringSubmatch(m[1])
	if n == nil {
		t.Errorf("%s: script-src has no nonce: %q", label, m[1])
		return
	}
	body := rec.Body.String()
	inline := 0
	for _, tag := range chromeScriptTagRe.FindAllString(body, -1) {
		if strings.Contains(tag, " src=") || strings.Contains(tag, `type="application/json"`) {
			continue
		}
		inline++
		if !strings.Contains(tag, `nonce="`+n[1]+`"`) {
			t.Errorf("%s: inline script without the response nonce: %s", label, tag)
		}
	}
	if inline == 0 {
		t.Errorf("%s: no inline scripts found; the check is not looking at a real page", label)
	}
	if hit := chromeInlineAttrRe.FindString(body); hit != "" {
		t.Errorf("%s: inline event handler attribute, blocked by the CSP: %s", label, hit)
	}
}

type contentMailSink struct {
	notices []struct{ to, subject, body string }
}

func (*contentMailSink) SendSignInCode(string, string, string) error { return nil }
func (s *contentMailSink) SendNotice(to, subject, body string) error {
	s.notices = append(s.notices, struct{ to, subject, body string }{to, subject, body})
	return nil
}
func (m *replyMailer) messages() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.sent...)
}

// Composed tests run the extracted worker workflows with a test clock and
// timing, while HTTP assertions continue through the real registered routes.
func sweepNoticeMail(a *teamSiteApp, delay time.Duration) {
	ctx := context.Background()
	s := events.New(a.database)
	s.PruneNotices(ctx)
	// The caller installs this sink before triggering a sweep.
	s.DeliverNoticeMail(ctx, a.noticeSink, delay, 15*time.Minute, a.srv.URL, entries.New(a.database, func(ctx context.Context, ev model.Event, slug string, takenDown bool) bool {
		info, _, err := a.sites.TeamSiteInfo(ctx, ev.AccountID, slug)
		return err == nil && info.Exists && info.ActiveVersion > 0 && !info.Suspended && !takenDown
	}))
}

func runComposedCleanup(a *teamSiteApp, mail *replyMailer, now time.Time) {
	l := config.DefaultLimits()
	events.New(a.database).RunEventCleanup(context.Background(), now, l.EventSitesKeep, l.EventRemovalWarn, a.sites.TrashTeamSite, events.ArchiveWarningSender(mail), l.IdleReplyTo)
}

type rubricCriterion struct {
	ID          string `json:"id"`
	Position    int    `json:"position"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Weight      int    `json:"weight"`
	MaxPoints   int    `json:"max_points"`
}
type rubricView struct {
	Criteria []rubricCriterion `json:"criteria"`
}

func decodeRubric(t *testing.T, r resp) rubricView {
	t.Helper()
	if r.status != http.StatusOK {
		t.Fatalf("rubric: %d %s", r.status, r.body)
	}
	var v rubricView
	if err := json.Unmarshal(r.body, &v); err != nil {
		t.Fatalf("rubric json: %v body %s", err, r.body)
	}
	return v
}
func rubricBody(items ...map[string]any) map[string]any {
	return map[string]any{"criteria": items}
}
func criterion(name, desc string, weight, points int) map[string]any {
	return map[string]any{"name": name, "description": desc, "weight": weight, "max_points": points}
}

const tsDomain = "simple-hack.test"

func markReady(t *testing.T, dir, handle string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "ready", handle), nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

type teamSiteApp struct {
	srv        *httptest.Server
	database   *sql.DB
	admin      string
	mux        *http.ServeMux
	conn       *handler.ConnectorHandler
	sites      *handler.SiteHandler
	users      *handler.UserHandler
	hack       *hackhttp.HackHandler
	certDir    string
	disk       *storage.DiskStorage
	noticeSink *contentMailSink
}

func newTeamSiteApp(t *testing.T) *teamSiteApp {
	t.Helper()
	return newTeamSiteAppMailer(t, nil)
}

// newTeamSiteAppMailer is newTeamSiteApp with its sign-in mail sent to
// mailer (nil: an unconfigured sender, as in most tests).
func newTeamSiteAppMailer(t *testing.T, mailer email.Sender) *teamSiteApp {
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
	oldDomain := accountrepo.CurrentPlatformDomain()
	accountrepo.SetPlatformDomain(tsDomain)
	t.Cleanup(func() { accountrepo.SetPlatformDomain(oldDomain) })

	adminKey, _ := auth.GenerateAPIKey()
	adminID, err := legacyrepo.EnsureAdminUser(context.Background(), database)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	a := &teamSiteApp{database: database, admin: adminKey, mux: mux, certDir: certDir}
	ed := edition.ForKind(edition.Hack)
	authMW := auth.Middleware(adminKey, adminID, accountapp.New(database), 180*24*time.Hour)
	var root http.Handler
	a.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { root.ServeHTTP(w, r) }))
	t.Cleanup(a.srv.Close)
	if mailer == nil {
		mailer = email.NewResendSender("", "test@example.com")
	}
	users := handler.NewUserHandler(database, accountapp.New(database), mailer, a.srv.URL, ed)
	a.users = users
	users.Register(mux, authMW, handler.NoticeMiddleware("1.0.0"))
	a.disk = disk
	a.sites = handler.NewSiteHandler(ed, "", database, disk, tsDomain, "sites."+tsDomain, "cname."+tsDomain, "", "", adminKey, nil, 0, "on", adminID, mailer, users.EmailLimiter())
	a.sites.SetOperationsReporting(operations.NewReporting(database))
	a.sites.SetOperationsNetwork(operations.NewNetworkUsage(database, ed))
	a.sites.Register(mux, authMW, handler.NoticeMiddleware("1.0.0"))
	t.Cleanup(a.sites.WaitNotifications)
	a.sites.SetPersonHosts("canonical")
	a.sites.SetSiteHosts("canonical", certDir)
	a.hack = hackhttp.NewHackHandler(database, a.srv.URL, tsDomain)
	a.hack.ShareAssets(a.sites.Pages())
	a.hack.SetHandleChecker(hackHandleCheckerForTest)
	a.hack.SetKeyResolver(func(ctx context.Context, key string, idle time.Duration) (model.User, error) {
		return db.GetUserByAPIKey(ctx, database, key, idle)
	})
	a.hack.SetSites(a.sites)
	a.hack.Register(mux, authMW)
	a.sites.SetHackEventPage(hackhttp.HackEventPage(database, a.srv.URL, a.sites.TeamSiteURL, a.sites.TeamSitesReady, testHackPages))
	a.sites.SetHackScreenshot(hackhttp.HackScreenshot(database, a.sites.TeamSitesReady))
	gated := a.sites.LegacyStorageCutoffGate(a.sites.HackLegacyStorageGate(auth.ScopeGate(accountapp.New(database), mux, true, 180*24*time.Hour)))
	a.conn = handler.NewConnectorHandler(database, a.srv.URL, adminKey, tsDomain, "sites."+tsDomain, "1.0.0", gated, true, ed)
	a.conn.Register(mux, authMW)
	a.conn.SetHackTeams(true)
	a.conn.SetSignInAlerts(users.SignInAlerts())
	app := handler.SecurityHeaders(handler.CORS(a.conn.BearerAuth(gated)))
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

// do, form, registerClient, consent, connect, rpc and the package-level
// codeFrom/connectDataRe/newVerifier/authorizeQuery/toolResultOf/
// testRedirect below are duplicated from internal/handler/connector_test.go
// (connectorApp's own methods; cleanup 3.2 slice 3), for the same reason
// teamSiteApp no longer embeds connectorApp (see its doc comment): they are
// the OAuth-connector request helpers hack_connector_test.go and others
// need, now with a *teamSiteApp receiver.
func (a *teamSiteApp) do(t *testing.T, method, path string, body io.Reader, headers map[string]string) resp {
	t.Helper()
	req, err := http.NewRequest(method, a.srv.URL+path, body)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
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

func (a *teamSiteApp) form(t *testing.T, path string, v url.Values, headers map[string]string) resp {
	h := map[string]string{"Content-Type": "application/x-www-form-urlencoded"}
	for k, val := range headers {
		h[k] = val
	}
	return a.do(t, http.MethodPost, path, strings.NewReader(v.Encode()), h)
}

func newVerifier() (string, string) {
	v, _ := auth.GenerateAPIKey() // 64 hex chars: a valid verifier
	sum := sha256.Sum256([]byte(v))
	return v, base64.RawURLEncoding.EncodeToString(sum[:])
}

func (a *teamSiteApp) registerClient(t *testing.T, redirect string) string {
	t.Helper()
	r := a.do(t, http.MethodPost, "/oauth/register", jsonBody(map[string]any{
		"client_name": "Test Chat", "redirect_uris": []string{redirect}, "token_endpoint_auth_method": "none",
	}), map[string]string{"Content-Type": "application/json"})
	if r.status != http.StatusCreated {
		t.Fatalf("register: %d %s", r.status, r.body)
	}
	return r.json(t)["client_id"].(string)
}

var connectDataRe = regexp.MustCompile(`<script type="application/json" id="connect-data">(.*?)</script>`)

// consent runs what the consent page does: load /oauth/authorize, then post
// the decision with the person's key. It returns the redirect the browser
// would follow.
func (a *teamSiteApp) consent(t *testing.T, q url.Values, key, decision string) string {
	t.Helper()
	page := a.do(t, http.MethodGet, "/oauth/authorize?"+q.Encode(), nil, nil)
	if page.status != http.StatusOK {
		t.Fatalf("authorize page: %d %s", page.status, page.body)
	}
	m := connectDataRe.FindSubmatch(page.body)
	if m == nil {
		t.Fatal("consent page carries no request data")
	}
	var data map[string]any
	_ = json.Unmarshal(m[1], &data)
	r := a.do(t, http.MethodPost, "/oauth/authorize/decision", jsonBody(map[string]string{
		"query": q.Encode(), "csrf": data["csrf"].(string), "decision": decision,
	}), map[string]string{"Content-Type": "application/json", "X-API-Key": key, "Origin": a.srv.URL})
	if r.status != http.StatusOK {
		t.Fatalf("decision: %d %s", r.status, r.body)
	}
	return r.json(t)["redirect_to"].(string)
}

func authorizeQuery(clientID, redirect, challenge string) url.Values {
	return url.Values{
		"response_type": {"code"}, "client_id": {clientID}, "redirect_uri": {redirect},
		"code_challenge": {challenge}, "code_challenge_method": {"S256"}, "state": {"st4te"}, "scope": {"sites"},
	}
}

func codeFrom(t *testing.T, redirectTo string) string {
	t.Helper()
	u, _ := url.Parse(redirectTo)
	code := u.Query().Get("code")
	if code == "" {
		t.Fatalf("no code in %s", redirectTo)
	}
	return code
}

// connect runs the whole flow for one person and returns the token response.
func (a *teamSiteApp) connect(t *testing.T, p person, clientID, redirect string) map[string]any {
	t.Helper()
	verifier, challenge := newVerifier()
	q := authorizeQuery(clientID, redirect, challenge)
	q.Set("resource", a.srv.URL+"/mcp")
	code := codeFrom(t, a.consent(t, q, p.key, "allow"))
	r := a.form(t, "/oauth/token", url.Values{
		"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirect},
		"client_id": {clientID}, "code_verifier": {verifier}, "resource": {a.srv.URL + "/mcp"},
	}, nil)
	if r.status != http.StatusOK {
		t.Fatalf("token: %d %s", r.status, r.body)
	}
	return r.json(t)
}

func (a *teamSiteApp) rpc(t *testing.T, token, method string, params any) resp {
	t.Helper()
	return a.do(t, http.MethodPost, "/mcp", jsonBody(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params}),
		map[string]string{"Content-Type": "application/json", "Authorization": "Bearer " + token, "MCP-Protocol-Version": "2025-06-18"})
}

func toolResultOf(t *testing.T, r resp) (string, map[string]any, bool) {
	t.Helper()
	res, ok := r.json(t)["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result: %d %s", r.status, r.body)
	}
	text := res["content"].([]any)[0].(map[string]any)["text"].(string)
	s, _ := res["structuredContent"].(map[string]any)
	return text, s, res["isError"].(bool)
}

const testRedirect = "https://chat.example.com/oauth/callback"

// newPerson is connectorApp's own (internal/handler/connector_test.go),
// duplicated verbatim (same addr format) for the same reason as the rest
// of teamSiteApp's own-receiver helpers above.
func (a *teamSiteApp) newPerson(t *testing.T, label string) person {
	t.Helper()
	addr := label + "-" + strconv.FormatInt(time.Now().UnixNano(), 36) + "@example.com"
	key, _ := auth.GenerateAPIKey()
	u, err := accountrepo.CreateUser(context.Background(), a.database, addr, key, false)
	if err != nil {
		t.Fatal(err)
	}
	handle := strings.ReplaceAll(strings.Split(addr, "@")[0], ".", "-")
	if _, err := a.database.Exec(`UPDATE users SET handle = $1 WHERE id = $2`, handle, u.ID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = a.database.Exec(`DELETE FROM users WHERE id = $1`, u.ID) })
	return person{email: addr, key: key}
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
