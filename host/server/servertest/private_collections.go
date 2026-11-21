package servertest

import (
	"context"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/host/handler"
	"github.com/vsriram/simple-host/host/web"

	accountapp "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/access/accesshttp"
	"github.com/vsriram/simple-host/host/access/auth"
	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/email"
	"github.com/vsriram/simple-host/host/storage"
)

// Claimed <name>.<SITE_DOMAIN> addresses and visitor sessions, end to end
// over HTTP against the real router (BoundSubdomains + LegacyHostRedirect in
// front, as cmd/server/main.go wires it). Needs DB_DSN (host/db/schema.sql applied).

const (
	PcSiteDomain  = "simple-host.test"
	PcContentHost = "sites.simple-host.test"
)

// privateApp is the server with the host routing of cmd/server/main.go in
// front, as hosted pages see it.
type PrivateApp struct{ *ConnectorApp }

func NewPrivateApp(t *testing.T, eds ...*edition.Edition) *PrivateApp {
	t.Helper()
	return NewPrivateAppMailer(t, email.NewResendSender("", "test@example.com"), eds...)
}

// newPrivateAppMailer is newPrivateApp with the given mailer.
func NewPrivateAppMailer(t *testing.T, mailer email.Sender, eds ...*edition.Edition) *PrivateApp {
	return NewPrivateAppMailerBase(t, mailer, "", eds...)
}

// newPrivateAppMailerBase builds the app at edition.ForKind(Host), unless an
// override edition (e.g. edition.ForKindWithLimits for a changed-knob test)
// is passed.
func NewPrivateAppMailerBase(t *testing.T, mailer email.Sender, base string, eds ...*edition.Edition) *PrivateApp {
	t.Helper()
	var ed *edition.Edition
	if len(eds) > 0 {
		ed = eds[0]
	} else {
		ed = edition.Default(config.DefaultLimits())
	}
	database := testDB(t)
	disk, err := storage.NewDiskStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	adminKey, _ := auth.GenerateAPIKey()
	adminID, err := accountsrepo.EnsureAdminUser(context.Background(), database)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	a := &PrivateApp{&ConnectorApp{Database: database, Admin: adminKey, Mux: mux}}
	authMW := auth.Middleware(adminKey, adminID, accountapp.New(database), ed.Limits().KeyIdleExpiry)
	var root http.Handler
	a.Srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { root.ServeHTTP(w, r) }))
	t.Cleanup(a.Srv.Close)
	if base == "" {
		base = a.Srv.URL
	}
	users := accesshttp.NewUserHandler(database, accountapp.New(database), mailer, base, ed)
	users.Register(mux, authMW, web.NoticeMiddleware("1.0.0"))
	a.Users = users
	a.Sites, a.Access, a.Data = handler.NewSiteHandlers(ed, "", database, disk, nil, PcSiteDomain, PcContentHost, "cname."+PcSiteDomain, "", "", adminKey, nil, 0, adminID, mailer, users.EmailLimiter)
	handler.Register(mux, a.Sites, a.Access, a.Data, authMW, web.NoticeMiddleware("1.0.0"))
	t.Cleanup(a.Data.WaitNotifications)
	users.SetSites(a.Access)
	users.SetPublicPage(a.Sites.PersonPageURL)
	gated := auth.ScopeGate(accountapp.New(database), mux, ed.Limits().KeyIdleExpiry, nil)
	a.Conn = accesshttp.NewConnectorHandler(database, base, adminKey, PcSiteDomain, PcContentHost, "1.0.0", gated, false, ed)
	a.Conn.Register(mux, authMW)
	a.Conn.SetSignInAlerts(users.SignInAlerts())
	app := web.SecurityHeaders(web.CORS(a.Conn.BearerAuth(gated)))
	// The same chain as cmd/server/main.go.
	root = a.Sites.FamilyHosts(app, a.Sites.SiteBaseHosts(a.Sites.BoundSubdomains(app, a.Sites.SiteHosts(app, a.Sites.PersonHosts(app, a.Sites.LegacyHostRedirect(app))))))
	return a
}

// at sends one request as if it arrived at host over HTTPS (behind nginx).
// appHost is the app's own address in this fixture (the public base URL's
// host): account sign-in answers only there (accountauth.go), while hosted
// pages live on pcSiteDomain hosts.
func (a *PrivateApp) AppHost() string {
	u, _ := url.Parse(a.Srv.URL)
	return u.Host
}

func (a *PrivateApp) At(t *testing.T, method, host, path string, body any, headers map[string]string) Resp {
	t.Helper()
	var rd io.Reader
	if body != nil {
		if s, ok := body.(string); ok {
			rd = strings.NewReader(s)
		} else {
			rd = JSONBody(body)
		}
	}
	req, err := http.NewRequest(method, a.Srv.URL+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = host
	req.Header.Set("X-Forwarded-Proto", "https")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
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
	return Resp{res.StatusCode, res.Header, b}
}

func (a *PrivateApp) Deploy(t *testing.T, p Person, site string) {
	t.Helper()
	r := a.At(t, "POST", "simple-host.test", "/v1/sites/"+site+"/files",
		map[string]any{"files": map[string]string{"index.html": "<h1>" + site + "</h1>", "sub/index.html": "sub"}},
		map[string]string{"X-API-Key": p.Key})
	if r.Status != http.StatusCreated {
		t.Fatalf("deploy %s: %d %s", site, r.Status, r.Body)
	}
}

func (a *PrivateApp) UserID(t *testing.T, p Person) (id, handle string) {
	t.Helper()
	if err := a.Database.QueryRow(`SELECT id, handle FROM users WHERE username = $1`, p.Email).Scan(&id, &handle); err != nil {
		t.Fatal(err)
	}
	return
}

func (a *PrivateApp) SiteID(t *testing.T, p Person, site string) string {
	t.Helper()
	uid, _ := a.UserID(t, p)
	var id string
	if err := a.Database.QueryRow(`SELECT id FROM sites WHERE user_id = $1 AND name = $2`, uid, site).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// session signs p in as a visitor of site on host, returning the cookie value.
func (a *PrivateApp) Session(t *testing.T, p Person, siteID, host string) string {
	t.Helper()
	uid, _ := a.UserID(t, p)
	id := make([]byte, 32)
	copy(id, []byte(strings.Repeat("x", 8)+time.Now().Format(time.RFC3339Nano)))
	for i := range id {
		id[i] ^= byte(i * 7)
	}
	key, _ := auth.GenerateAPIKey() // shk_ + 64 hex chars = 32 random bytes
	raw := strings.TrimPrefix(key, auth.APIKeyPrefix)
	id, _ = hex.DecodeString(raw)
	now := time.Now()
	if _, err := accountsrepo.InsertVisitorSession(context.Background(), a.Database, id, uid, siteID, host, now.Add(time.Hour), now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	return raw
}

func Browser(host, cookie string, extra ...string) map[string]string {
	h := map[string]string{"Origin": "https://" + host, "Sec-Fetch-Site": "same-origin", "X-SH-CSRF": "1"}
	if cookie != "" {
		h["Cookie"] = accesshttp.VisitorCookieHost + "=" + cookie
	}
	for i := 0; i+1 < len(extra); i += 2 {
		if extra[i+1] == "" {
			delete(h, extra[i])
		} else {
			h[extra[i]] = extra[i+1]
		}
	}
	return h
}

func (a *PrivateApp) SiteIDOwner(t *testing.T, p Person) string {
	id, _ := a.UserID(t, p)
	return id
}

// connectResource runs the connector flow for a whole-server token (a /v1
// credential), not an /mcp-only one.
func (a *PrivateApp) ConnectResource(t *testing.T, p Person, resource string) string {
	t.Helper()
	clientID := a.RegisterClient(t, TestRedirect)
	verifier, challenge := NewVerifier()
	q := AuthorizeQuery(clientID, TestRedirect, challenge)
	q.Set("resource", resource)
	code := CodeFrom(t, a.Consent(t, q, p.Key, "allow"))
	r := a.Form(t, "/oauth/token", url.Values{
		"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {TestRedirect},
		"client_id": {clientID}, "code_verifier": {verifier}, "resource": {resource},
	}, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("token: %d %s", r.Status, r.Body)
	}
	return r.JSON(t)["access_token"].(string)
}
