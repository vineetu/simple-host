package handler

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/host/config"

	accountapp "github.com/vsriram/simple-host/host/access"
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
	pcSiteDomain  = "simple-host.test"
	pcContentHost = "sites.simple-host.test"
)

// privateApp is the server with the host routing of cmd/server/main.go in
// front, as hosted pages see it.
type privateApp struct{ *connectorApp }

func newPrivateApp(t *testing.T, eds ...*edition.Edition) *privateApp {
	t.Helper()
	return newPrivateAppMailer(t, email.NewResendSender("", "test@example.com"), eds...)
}

// newPrivateAppMailer is newPrivateApp with the given mailer.
func newPrivateAppMailer(t *testing.T, mailer email.Sender, eds ...*edition.Edition) *privateApp {
	return newPrivateAppMailerBase(t, mailer, "", eds...)
}

// newPrivateAppMailerBase builds the app at edition.ForKind(Host), unless an
// override edition (e.g. edition.ForKindWithLimits for a changed-knob test)
// is passed.
func newPrivateAppMailerBase(t *testing.T, mailer email.Sender, base string, eds ...*edition.Edition) *privateApp {
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
	a := &privateApp{&connectorApp{database: database, admin: adminKey, mux: mux}}
	authMW := auth.Middleware(adminKey, adminID, accountapp.New(database), ed.Limits().KeyIdleExpiry)
	var root http.Handler
	a.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { root.ServeHTTP(w, r) }))
	t.Cleanup(a.srv.Close)
	if base == "" {
		base = a.srv.URL
	}
	users := NewUserHandler(database, accountapp.New(database), mailer, base, ed)
	users.Register(mux, authMW, NoticeMiddleware("1.0.0"))
	a.users = users
	a.sites, a.access, a.data = NewSiteHandlers(ed, "", database, disk, pcSiteDomain, pcContentHost, "cname."+pcSiteDomain, "", "", adminKey, nil, 0, adminID, mailer, users.EmailLimiter())
	a.sites.Register(mux, authMW, NoticeMiddleware("1.0.0"))
	t.Cleanup(a.data.WaitNotifications)
	users.SetSites(a.sites)
	users.SetPublicPage(a.sites.PersonPageURL)
	users.SetAddressState(a.sites.AddressState)
	gated := auth.ScopeGate(accountapp.New(database), mux, ed.Limits().KeyIdleExpiry, nil)
	a.conn = NewConnectorHandler(database, base, adminKey, pcSiteDomain, pcContentHost, "1.0.0", gated, false, ed)
	a.conn.Register(mux, authMW)
	a.conn.SetSignInAlerts(users.SignInAlerts())
	app := SecurityHeaders(CORS(a.conn.BearerAuth(gated)))
	// The same chain as cmd/server/main.go.
	root = a.sites.FamilyHosts(app, a.sites.SiteBaseHosts(a.sites.BoundSubdomains(app, a.sites.SiteHosts(app, a.sites.PersonHosts(app, a.sites.LegacyHostRedirect(app))))))
	return a
}

// at sends one request as if it arrived at host over HTTPS (behind nginx).
// appHost is the app's own address in this fixture (the public base URL's
// host): account sign-in answers only there (accountauth.go), while hosted
// pages live on pcSiteDomain hosts.
func (a *privateApp) appHost() string {
	u, _ := url.Parse(a.srv.URL)
	return u.Host
}

func (a *privateApp) at(t *testing.T, method, host, path string, body any, headers map[string]string) resp {
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
	return resp{res.StatusCode, res.Header, b}
}

func (a *privateApp) deploy(t *testing.T, p person, site string) {
	t.Helper()
	r := a.at(t, "POST", "simple-host.test", "/v1/sites/"+site+"/files",
		map[string]any{"files": map[string]string{"index.html": "<h1>" + site + "</h1>", "sub/index.html": "sub"}},
		map[string]string{"X-API-Key": p.key})
	if r.status != http.StatusCreated {
		t.Fatalf("deploy %s: %d %s", site, r.status, r.body)
	}
}

func (a *privateApp) userID(t *testing.T, p person) (id, handle string) {
	t.Helper()
	if err := a.database.QueryRow(`SELECT id, handle FROM users WHERE username = $1`, p.email).Scan(&id, &handle); err != nil {
		t.Fatal(err)
	}
	return
}

func (a *privateApp) siteID(t *testing.T, p person, site string) string {
	t.Helper()
	uid, _ := a.userID(t, p)
	var id string
	if err := a.database.QueryRow(`SELECT id FROM sites WHERE user_id = $1 AND name = $2`, uid, site).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// session signs p in as a visitor of site on host, returning the cookie value.
func (a *privateApp) session(t *testing.T, p person, siteID, host string) string {
	t.Helper()
	uid, _ := a.userID(t, p)
	id := make([]byte, 32)
	copy(id, []byte(strings.Repeat("x", 8)+time.Now().Format(time.RFC3339Nano)))
	for i := range id {
		id[i] ^= byte(i * 7)
	}
	key, _ := auth.GenerateAPIKey() // shk_ + 64 hex chars = 32 random bytes
	raw := strings.TrimPrefix(key, auth.APIKeyPrefix)
	id, _ = hex.DecodeString(raw)
	now := time.Now()
	if _, err := accountsrepo.InsertVisitorSession(context.Background(), a.database, id, uid, siteID, host, now.Add(time.Hour), now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	return raw
}

func browser(host, cookie string, extra ...string) map[string]string {
	h := map[string]string{"Origin": "https://" + host, "Sec-Fetch-Site": "same-origin", "X-SH-CSRF": "1"}
	if cookie != "" {
		h["Cookie"] = visitorCookieHost + "=" + cookie
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

// Sessions and cookies on a claimed address: host-only __Host- cookie, never a
// Domain= attribute; /me from a sibling host reports signed out; sign-in by
// emailed code must come from the host itself.
func TestClaimedSubdomainSessionIsolation(t *testing.T) {
	a := newPrivateApp(t)
	olive, vic := a.newPerson(t, "olive"), a.newPerson(t, "vic")
	a.deploy(t, olive, "clay")
	const dom = "clay-and-kiln.simple-host.test"
	if r := a.at(t, "POST", "simple-host.test", "/v1/sites/clay/domain", map[string]string{"domain": dom}, map[string]string{"X-API-Key": olive.key}); r.status != 200 {
		t.Fatalf("claim: %d %s", r.status, r.body)
	}
	siteID := a.siteID(t, olive, "clay")
	// Establish hand-off on the claimed host sets a host-only __Host- cookie.
	uid, _ := a.userID(t, vic)
	sid, _ := hex.DecodeString(strings.Repeat("ab", 32))
	now := time.Now()
	if _, err := accountsrepo.InsertVisitorSession(context.Background(), a.database, sid, uid, siteID, dom, now.Add(time.Hour), now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = accountsrepo.DeleteVisitorSession(context.Background(), a.database, sid) })
	if err := accountsrepo.InsertEstablishToken(context.Background(), a.database, "once-"+uid, sid, dom, "https://"+dom+"/", testNonceHash(testNonce), now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	r := a.at(t, "GET", dom, "/v1/visitor/establish?once=once-"+uid, nil, nonceCookie(testNonce))
	sc := r.header.Values("Set-Cookie")
	if r.status != http.StatusFound || len(sc) != 2 || !strings.HasPrefix(sc[0], visitorCookieHost+"=") ||
		strings.Contains(strings.ToLower(sc[0]), "domain=") || !strings.Contains(sc[0], "Secure") || !strings.Contains(sc[0], "HttpOnly") {
		t.Fatalf("establish cookie: %d %v", r.status, sc)
	}
	cookie := strings.Repeat("ab", 32)
	// /me: same origin sees the visitor; a sibling host's page does not.
	me := "/v1/sites/clay/me"
	if r := a.at(t, "GET", dom, me, nil, browser(dom, cookie)); r.json(t)["signed_in"] != true || r.json(t)["email"] != vic.email {
		t.Fatalf("/me same origin: %s", r.body)
	}
	for _, o := range []string{"https://" + pcContentHost, "https://evil.simple-host.test"} {
		r := a.at(t, "GET", dom, me, nil, browser(dom, cookie, "Origin", o, "Sec-Fetch-Site", "same-site"))
		if r.json(t)["signed_in"] == true || strings.Contains(string(r.body), vic.email) {
			t.Errorf("/me from %s: %s", o, r.body)
		}
	}
	// Email sign-in on the claimed host must be same-origin.
	if r := a.at(t, "POST", dom, "/v1/sites/clay/visitor/auth/verify", map[string]string{"email": vic.email, "code": "123456"}, map[string]string{"Origin": "https://" + pcContentHost, "X-SH-CSRF": "1"}); r.status != 403 {
		t.Errorf("sibling sign-in: %d %s", r.status, r.body)
	}
	// CORS: the claimed host's Origin is its own; another site's API does not trust it.
	a.deploy(t, vic, "other")
	_, vicHandle := a.userID(t, vic)
	if r := a.at(t, "GET", pcContentHost, "/v1/u/"+vicHandle+"/sites/other/me", nil, map[string]string{"Origin": "https://" + dom}); r.status != 403 {
		t.Errorf("claimed origin trusted by another site: %d", r.status)
	}
	// Apex auth is unaffected: the dashboard API takes keys, never this cookie.
	if r := a.at(t, "GET", "simple-host.test", "/v1/me", nil, map[string]string{"Cookie": visitorCookieHost + "=" + cookie}); r.status != 401 {
		t.Errorf("cookie accepted as apex credential: %d", r.status)
	}
	// Sign-in may return to the claimed host (no DNS proof needed); not to an unclaimed one.
	oh := &OAuthHandler{database: a.database, accounts: accountapp.New(a.database), cfg: OAuthSettings{PublicBaseURL: "https://simple-host.test", SiteDomain: pcSiteDomain, ContentHost: pcContentHost, CNAMETarget: "cname." + pcSiteDomain}}
	if _, site, _, purpose, err := oh.sanitizeReturnTo(context.Background(), "https://"+dom+"/rsvp.html"); err != nil || purpose != "site" || site.String != siteID {
		t.Errorf("return_to claimed host: %v %q", err, purpose)
	}
	for _, bad := range []string{"https://unclaimed.simple-host.test/", "https://www.simple-host.test/", "https://" + pcContentHost + "/x/y/"} {
		if _, _, _, _, err := oh.sanitizeReturnTo(context.Background(), bad); err == nil {
			t.Errorf("return_to %s accepted", bad)
		}
	}
	// Releasing the address frees the name and restores the legacy redirect.
	if r := a.at(t, "DELETE", "simple-host.test", "/v1/sites/clay/domain", nil, map[string]string{"X-API-Key": olive.key}); r.status != 204 {
		t.Fatalf("release: %d", r.status)
	}
	if r := a.at(t, "GET", dom, "/", nil, nil); r.status != http.StatusMovedPermanently {
		t.Fatalf("released host: %d", r.status)
	}
}

func (a *privateApp) siteIDOwner(t *testing.T, p person) string {
	id, _ := a.userID(t, p)
	return id
}

// connectResource runs the connector flow for a whole-server token (a /v1
// credential), not an /mcp-only one.
func (a *privateApp) connectResource(t *testing.T, p person, resource string) string {
	t.Helper()
	clientID := a.registerClient(t, testRedirect)
	verifier, challenge := newVerifier()
	q := authorizeQuery(clientID, testRedirect, challenge)
	q.Set("resource", resource)
	code := codeFrom(t, a.consent(t, q, p.key, "allow"))
	r := a.form(t, "/oauth/token", url.Values{
		"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {testRedirect},
		"client_id": {clientID}, "code_verifier": {verifier}, "resource": {resource},
	}, nil)
	if r.status != http.StatusOK {
		t.Fatalf("token: %d %s", r.status, r.body)
	}
	return r.json(t)["access_token"].(string)
}

func itoa(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// A visitor-supplied value must never reach the owner's spreadsheet as a
// formula: text starting with = + - @ tab or CR is neutralised, numbers are not.
func TestCSVSafeNeutralisesFormulas(t *testing.T) {
	cases := map[string]string{
		"=cmd|'/C calc'!A1":        "'=cmd|'/C calc'!A1",
		"+HYPERLINK(\"http://x\")": "'+HYPERLINK(\"http://x\")",
		"-2+3":                     "'-2+3",
		"@SUM(A1)":                 "'@SUM(A1)",
		"\t=1+1":                   "'\t=1+1",
		"hello":                    "hello",
		"":                         "",
	}
	for in, want := range cases {
		if got := csvSafe(in); got != want {
			t.Errorf("csvSafe(%q) = %q, want %q", in, got, want)
		}
	}
	if got := csvSafe("=bad-header"); got != "'=bad-header" {
		t.Errorf("csvSafe header = %q", got)
	}
	// Security review (Grok 7): a leading LEFT-TO-RIGHT MARK (U+200E, a
	// Unicode format character) must not hide the formula that follows it.
	if got := csvSafe("‎=HYPERLINK(\"https://evil.example\",\"click\")"); got != "'‎=HYPERLINK(\"https://evil.example\",\"click\")" {
		t.Errorf("csvSafe did not escape past a leading format character: %q", got)
	}
	if got := csvSafe("  =HYPERLINK(1)"); got != "'  =HYPERLINK(1)" {
		t.Errorf("csvSafe did not escape past leading whitespace: %q", got)
	}
	if got := csvSafe("plain text"); got != "plain text" {
		t.Errorf("csvSafe changed ordinary text: %q", got)
	}
}

// An establish code presented on the wrong host is burned: it cannot then be
// replayed on the host it was issued for. The normal hand-off still works once.
func TestEstablishTokenWrongHostBurns(t *testing.T) {
	a := newPrivateApp(t)
	olive, vic := a.newPerson(t, "olive"), a.newPerson(t, "vic")
	a.deploy(t, olive, "clay")
	a.deploy(t, olive, "pots")
	const dom, other = "burn-clay.simple-host.test", "burn-pots.simple-host.test"
	for site, d := range map[string]string{"clay": dom, "pots": other} {
		if r := a.at(t, "POST", "simple-host.test", "/v1/sites/"+site+"/domain", map[string]string{"domain": d}, map[string]string{"X-API-Key": olive.key}); r.status != 200 {
			t.Fatalf("claim %s: %d %s", d, r.status, r.body)
		}
	}
	siteID := a.siteID(t, olive, "clay")
	uid, _ := a.userID(t, vic)
	ctx := context.Background()
	now := time.Now()
	issue := func(sidByte, once string) {
		sid, _ := hex.DecodeString(strings.Repeat(sidByte, 32))
		if _, err := accountsrepo.InsertVisitorSession(ctx, a.database, sid, uid, siteID, dom, now.Add(time.Hour), now.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = accountsrepo.DeleteVisitorSession(ctx, a.database, sid) })
		if err := accountsrepo.InsertEstablishToken(ctx, a.database, once, sid, dom, "https://"+dom+"/", testNonceHash(testNonce), now.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	establish := func(host, once string) resp {
		return a.at(t, "GET", host, "/v1/visitor/establish?once="+once, nil, nonceCookie(testNonce))
	}

	// Wrong host: rejected, then the right host is rejected too (burned).
	burn := "burn-" + uid
	issue("cd", burn)
	if r := establish(other, burn); r.status != http.StatusBadRequest || len(r.header.Values("Set-Cookie")) != 0 {
		t.Fatalf("wrong host accepted: %d %v", r.status, r.header.Values("Set-Cookie"))
	}
	if r := establish(dom, burn); r.status != http.StatusBadRequest || len(r.header.Values("Set-Cookie")) != 0 {
		t.Fatalf("burned code replayed on right host: %d %v", r.status, r.header.Values("Set-Cookie"))
	}

	// Normal flow: right host works once, then the code is spent.
	good := "good-" + uid
	issue("ef", good)
	if r := establish(strings.ToUpper(dom), good); r.status != http.StatusFound || len(r.header.Values("Set-Cookie")) != 2 {
		t.Fatalf("normal establish: %d %v", r.status, r.header.Values("Set-Cookie"))
	}
	if r := establish(dom, good); r.status != http.StatusBadRequest {
		t.Fatalf("code reused: %d", r.status)
	}
}
