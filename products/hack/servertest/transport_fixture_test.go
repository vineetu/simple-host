package servertest

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

	productconfig "github.com/vsriram/simple-host/products/hack/config"
	productedition "github.com/vsriram/simple-host/products/hack/edition"
	hackmcp "github.com/vsriram/simple-host/products/hack/mcp"

	accountapp "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/access/accesshttp"
	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/data/datahttp"
	sitesapp "github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/sites/siteshttp"
	"github.com/vsriram/simple-host/host/web"

	accountrepo "github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/email"
	"github.com/vsriram/simple-host/host/handler"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sqlkit"
	"github.com/vsriram/simple-host/host/storage"
	"github.com/vsriram/simple-host/products/hack"
	"github.com/vsriram/simple-host/products/hack/app/events"
	"github.com/vsriram/simple-host/products/hack/app/members"
	"github.com/vsriram/simple-host/products/hack/app/storagebind"
	"github.com/vsriram/simple-host/products/hack/app/teams"
	"github.com/vsriram/simple-host/products/hack/hackhttp"
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

func hackHandleCheckerForTest(ctx context.Context, q sqlkit.Querier, h string) (events.HandleVerdict, error) {
	v, err := accountapp.JudgeHandle(ctx, q, "", h)
	return events.HandleVerdict{Status: v.Status, Code: v.Code}, err
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
		ed = productedition.ForKind(productedition.Hack)
	}
	database := testDB(t)
	adminKey, _ := auth.GenerateAPIKey()
	adminID, err := accountrepo.EnsureAdminUser(context.Background(), database)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	var root http.Handler
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { root.ServeHTTP(w, r) }))
	t.Cleanup(srv.Close)
	authMW := auth.Middleware(adminKey, adminID, accountapp.New(database), ed.Limits().KeyIdleExpiry)
	users := accesshttp.NewUserHandler(database, accountapp.New(database), email.NewResendSender("", "test@example.com"), srv.URL, ed)
	users.Register(mux, authMW, web.NoticeMiddleware("1.0.0"))
	h := hackhttp.NewHackHandler(database, srv.URL, "simple-hack.test", ed)
	h.ShareAssets(web.NewAssetPipeline(ed, srv.URL))
	h.SetHandleChecker(hackHandleCheckerForTest)
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

func (a *hackApp) cleanupEvent(slug string) {
	var accountID string
	_ = a.database.QueryRow(`SELECT account_id FROM events WHERE slug = $1`, slug).Scan(&accountID)
	_, _ = a.database.Exec(`DELETE FROM events WHERE slug = $1`, slug)
	if accountID != "" {
		_, _ = a.database.Exec(`DELETE FROM users WHERE id = $1`, accountID)
	}
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

func uniqueSlug() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return "hax" + hex.EncodeToString(b[:])
}

// setHackAppLimits overwrites a's already-built hackhttp.HackHandler's own limits
// snapshot in place (moved from host/handler/limits_test.go, cleanup
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

var ()

func (m *replyMailer) messages() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.sent...)
}

func runComposedCleanup(a *teamSiteApp, mail *replyMailer, now time.Time) {
	l := productconfig.DefaultLimits()
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
	srv      *httptest.Server
	database *sql.DB
	admin    string
	mux      *http.ServeMux
	conn     *accesshttp.ConnectorHandler
	sites    *siteshttp.SiteHandler
	access   *accesshttp.AccessHandler
	data     *datahttp.DataHandler
	users    *accesshttp.UserHandler
	hack     *hackhttp.HackHandler
	certDir  string
	disk     fixtureDisk
}

func newTeamSiteApp(t *testing.T) *teamSiteApp {
	t.Helper()
	return newTeamSiteAppMailer(t, nil)
}

// newTeamSiteAppMailer is newTeamSiteApp with its sign-in mail sent to
// mailer (nil: an unconfigured sender, as in most tests).
func newTeamSiteAppMailer(t *testing.T, mailer email.Sender) *teamSiteApp {
	t.Helper()
	accountapp.SetGroupHooks(accountapp.GroupHooks{Erasure: members.AccountErasure{}})
	t.Cleanup(func() { accountapp.SetGroupHooks(accountapp.GroupHooks{}) })
	database := testDB(t)
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
	accountrepo.SetPlatformDomains(tsDomain)
	t.Cleanup(func() { accountrepo.SetPlatformDomains(oldDomain) })

	adminKey, _ := auth.GenerateAPIKey()
	adminID, err := accountrepo.EnsureAdminUser(context.Background(), database)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	a := &teamSiteApp{database: database, admin: adminKey, mux: mux, certDir: certDir}
	ed := productedition.ForKind(productedition.Hack)
	authMW := auth.Middleware(adminKey, adminID, accountapp.New(database), 180*24*time.Hour)
	var root http.Handler
	a.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { root.ServeHTTP(w, r) }))
	t.Cleanup(a.srv.Close)
	if mailer == nil {
		mailer = email.NewResendSender("", "test@example.com")
	}
	users := accesshttp.NewUserHandler(database, accountapp.New(database), mailer, a.srv.URL, ed)
	a.users = users
	users.Register(mux, authMW, web.NoticeMiddleware("1.0.0"))
	a.disk = disk
	a.sites, a.access, a.data = handler.NewSiteHandlers(ed, "", database, disk, nil, tsDomain, "sites."+tsDomain, "cname."+tsDomain, "", "", adminKey, nil, 0, adminID, mailer, users.EmailLimiter)
	gate := teams.SiteGate{TeamSitesReady: a.sites.TeamSitesReady, RequestCert: a.sites.RequestSiteCert, PaperSite: a.sites.RecordGateSite, PaperState: a.sites.RecordGateState}
	a.sites.SetGroupGate(gate)
	handler.SetStorageBindings(a.access, a.data, storagebind.Provider{DB: database})
	handler.Register(mux, a.sites, a.access, a.data, authMW, web.NoticeMiddleware("1.0.0"))
	t.Cleanup(a.data.WaitNotifications)
	a.sites.Hosts.SetPersonHosts("canonical")
	a.sites.Hosts.SetSiteHosts("canonical", certDir)
	a.hack = hackhttp.NewHackHandler(database, a.srv.URL, tsDomain)
	installAssets(a.sites, nil)
	a.hack.ShareAssets(a.sites.Pages())
	a.hack.SetHandleChecker(hackHandleCheckerForTest)
	a.hack.SetSites(a.sites)
	a.hack.Register(mux, authMW)
	a.sites.Hosts.SetGroupHosts(hackhttp.NewGroupHosts(database, hackhttp.HackEventPage(database, a.srv.URL, a.sites.TeamSiteURL, a.sites.TeamSitesReady, testHackPages), hackhttp.HackScreenshot(database, a.sites.TeamSitesReady)))
	gated := auth.ScopeGate(accountapp.New(database), mux, 180*24*time.Hour, func(ctx context.Context, user *model.User, site string) (int, string, string, error) {
		return sitesapp.CheckWrite(ctx, database, gate, user, site)
	})
	a.conn = accesshttp.NewConnectorHandler(database, a.srv.URL, adminKey, tsDomain, "sites."+tsDomain, "1.0.0", gated, true, a.access, users.SignInAlerts(), ed)
	a.conn.Register(mux, authMW)
	a.conn.SetTools(hackmcp.Contribution())
	a.conn.SetTeamConnections(true)
	app := web.SecurityHeaders(web.CORS(a.conn.BearerAuth(gated)))
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
// testRedirect below are duplicated from host/handler/connector_test.go
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

// newPerson is connectorApp's own (host/handler/connector_test.go),
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

func deployBody(text string) map[string]any {
	return map[string]any{"files": map[string]string{"index.html": "<h1>" + text + "</h1>"}}
}

// deployTeam publishes site (a team's slug, or the event's id) under event's
// account with the person's own key: the platform checks they may act for it.
func (a *teamSiteApp) deployTeam(t *testing.T, event, site, key, text string) resp {
	t.Helper()
	return a.api(t, "PUT", "/v1/sites/"+site+"/files?create=1&owner="+event, deployBody(text), key)
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

func installAssets(sh *siteshttp.SiteHandler, mux *http.ServeMux) {
	a := hack.Extension().Assets(sh)
	sh.SetProductAssets(a.Replace, a.Own, a.Files, a.Skills, a.Hooks, a.Rewrite)
	if mux != nil && a.Routes != nil {
		a.Routes(mux, sh.AssetServices())
	}
}

// fixtureDisk uses the production backend contracts, with no concrete disk type.
type fixtureDisk interface {
	storage.SiteFiles
	storage.LocalServing
}

// eventIDOf is the event's id: the name of its website under the event's account.
func eventIDOf(t *testing.T, a *teamSiteApp, slug string) string {
	t.Helper()
	var id string
	if err := a.database.QueryRow(`SELECT id FROM events WHERE slug=$1`, slug).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}
