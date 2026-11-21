package accesstest

// Helpers shared by this package's tests: the connector's OAuth flow, a
// capturing mailer, the visitor sign-in and SSO hops, a passcode site and
// teams. Copied from host/server/servertest and moved onto App, so this
// package builds only Access's routes.

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/access/accesshttp"
	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/httpx"
	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
)

// Person and Resp are routetest's.
type (
	Person = rt.Person
	Resp   = rt.Resp
)

// Uniq is prefix with a unique suffix.
func Uniq(prefix string) string { return prefix + strconv.FormatInt(time.Now().UnixNano(), 36) }

// AppHost is the app's own address (the public base URL's host): account
// sign-in answers only there, while hosted pages live on rt.SiteDomain hosts.
func (a *App) AppHost() string {
	u, _ := url.Parse(a.Srv.URL)
	return u.Host
}

// Do sends one request to the app's own address as a plain client would
// (no forwarded headers); body is nil, a string, an io.Reader or JSON.
func (a *App) Do(t *testing.T, method, path string, body any, headers map[string]string) Resp {
	t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rd = strings.NewReader(b)
	case io.Reader:
		rd = b
	default:
		rd = rt.JSONBody(b)
	}
	req, err := http.NewRequest(method, a.Srv.URL+path, rd)
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
	return Resp{Status: res.StatusCode, Header: res.Header, Body: b}
}

// Form posts v as a form to the app's own address.
func (a *App) Form(t *testing.T, path string, v url.Values, headers map[string]string) Resp {
	t.Helper()
	h := map[string]string{"Content-Type": "application/x-www-form-urlencoded"}
	for k, val := range headers {
		h[k] = val
	}
	return a.Do(t, http.MethodPost, path, v.Encode(), h)
}

// SetLimits gives every Access handler of the fixture the limits l.
func (a *App) SetLimits(l config.Limits) {
	a.Access.Limits, a.Access.Passcode.Limits = l, l
	a.Users.Limits = l
	a.Conn.Limits = l
}

// WithLimits is config.Limits loaded from m (unset keys keep defaults).
func WithLimits(t *testing.T, m map[string]string) config.Limits {
	t.Helper()
	l, err := config.LoadLimits(func(k string) string { return m[k] })
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// GroupTestEdition is an edition with hosted events and random handles.
func GroupTestEdition() *edition.Edition {
	return edition.FromPolicies(edition.RoutePolicy{HostedEvents: true}, edition.PresentationPolicy{ProductName: "Test product", ProductAssets: true, CanonicalBaseURL: "https://product.example", StylePath: "product.css", StyleClass: "product"}, edition.IdentityPolicy{RandomHandles: true, PaperMetrics: true}, edition.StoragePolicy{EventSites: true, StrictArchives: true, JudgePreviewFrames: true, EventVersionRetention: true, CombinedPool: true}, edition.NotificationPolicy{EventScoped: true}, config.DefaultLimits())
}

// ---- the connector's OAuth flow ----

const TestRedirect = "https://chat.example.com/oauth/callback"

var ConnectDataRe = regexp.MustCompile(`<script type="application/json" id="connect-data">(.*?)</script>`)

// NewVerifier is a PKCE verifier and its S256 challenge.
func NewVerifier() (string, string) {
	v, _ := auth.GenerateAPIKey() // 64 hex chars: a valid verifier
	sum := sha256.Sum256([]byte(v))
	return v, base64.RawURLEncoding.EncodeToString(sum[:])
}

// RegisterClient registers a public client for redirect.
func (a *App) RegisterClient(t *testing.T, redirect string) string {
	t.Helper()
	r := a.Do(t, http.MethodPost, "/oauth/register", map[string]any{
		"client_name": "Test Chat", "redirect_uris": []string{redirect}, "token_endpoint_auth_method": "none",
	}, map[string]string{"Content-Type": "application/json"})
	if r.Status != http.StatusCreated {
		t.Fatalf("register: %d %s", r.Status, r.Body)
	}
	return r.JSON(t)["client_id"].(string)
}

// RegisterNamedClient registers a client called name, removed after the test.
func (a *App) RegisterNamedClient(t *testing.T, name string) string {
	t.Helper()
	r := a.At(t, "POST", rt.SiteDomain, "/oauth/register", map[string]any{
		"client_name": name, "redirect_uris": []string{"https://chat.example.com/cb"}, "token_endpoint_auth_method": "none",
	}, nil)
	if r.Status != 201 {
		t.Fatalf("register: %d %s", r.Status, r.Body)
	}
	id := r.JSON(t)["client_id"].(string)
	t.Cleanup(func() { _, _ = a.Database.Exec(`DELETE FROM oauth_clients WHERE client_id = $1`, id) })
	return id
}

// AuthorizeQuery is an authorization request for the sites scope.
func AuthorizeQuery(clientID, redirect, challenge string) url.Values {
	return url.Values{
		"response_type": {"code"}, "client_id": {clientID}, "redirect_uri": {redirect},
		"code_challenge": {challenge}, "code_challenge_method": {"S256"}, "state": {"st4te"}, "scope": {"sites"},
	}
}

// ConsentPage loads the consent page and returns its body and CSRF token.
func (a *App) ConsentPage(t *testing.T, q url.Values) (string, string) {
	t.Helper()
	page := a.Do(t, http.MethodGet, "/oauth/authorize?"+q.Encode(), nil, nil)
	if page.Status != http.StatusOK {
		t.Fatalf("authorize page: %d %s", page.Status, page.Body)
	}
	m := ConnectDataRe.FindSubmatch(page.Body)
	if m == nil {
		t.Fatal("consent page carries no request data")
	}
	var data map[string]any
	if err := json.Unmarshal(m[1], &data); err != nil {
		t.Fatal(err)
	}
	return string(page.Body), data["csrf"].(string)
}

// DecideConsent posts an "allow" decision (plus extra fields) with key.
func (a *App) DecideConsent(t *testing.T, q url.Values, csrf, key string, extra map[string]any) Resp {
	t.Helper()
	body := map[string]any{"query": q.Encode(), "csrf": csrf, "decision": "allow"}
	for k, v := range extra {
		body[k] = v
	}
	return a.Do(t, http.MethodPost, "/oauth/authorize/decision", body, map[string]string{
		"Content-Type": "application/json", "X-API-Key": key, "Origin": a.Srv.URL,
	})
}

// Consent runs what the consent page does and returns the redirect the
// browser would follow.
func (a *App) Consent(t *testing.T, q url.Values, key, decision string) string {
	t.Helper()
	_, csrf := a.ConsentPage(t, q)
	r := a.DecideConsent(t, q, csrf, key, map[string]any{"decision": decision})
	if r.Status != http.StatusOK {
		t.Fatalf("decision: %d %s", r.Status, r.Body)
	}
	return r.JSON(t)["redirect_to"].(string)
}

// CodeFrom is the authorization code in a redirect.
func CodeFrom(t *testing.T, redirectTo string) string {
	t.Helper()
	u, _ := url.Parse(redirectTo)
	code := u.Query().Get("code")
	if code == "" {
		t.Fatalf("no code in %s", redirectTo)
	}
	return code
}

// ExchangeCode trades code for tokens.
func (a *App) ExchangeCode(t *testing.T, clientID, redirect, verifier, code, resource string) map[string]any {
	t.Helper()
	r := a.Form(t, "/oauth/token", url.Values{
		"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirect},
		"client_id": {clientID}, "code_verifier": {verifier}, "resource": {resource},
	}, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("token: %d %s", r.Status, r.Body)
	}
	return r.JSON(t)
}

// Connect runs the whole flow for p (an /mcp token) and returns the token response.
func (a *App) Connect(t *testing.T, p Person, clientID, redirect string) map[string]any {
	t.Helper()
	return a.connect(t, p, clientID, redirect, a.Srv.URL+"/mcp")
}

// ConnectResource runs the flow for a token to resource (a /v1 credential
// when resource is the server itself) and returns the access token.
func (a *App) ConnectResource(t *testing.T, p Person, resource string) string {
	t.Helper()
	return a.connect(t, p, a.RegisterClient(t, TestRedirect), TestRedirect, resource)["access_token"].(string)
}

func (a *App) connect(t *testing.T, p Person, clientID, redirect, resource string) map[string]any {
	t.Helper()
	verifier, challenge := NewVerifier()
	q := AuthorizeQuery(clientID, redirect, challenge)
	q.Set("resource", resource)
	code := CodeFrom(t, a.Consent(t, q, p.Key, "allow"))
	return a.ExchangeCode(t, clientID, redirect, verifier, code, resource)
}

// Rpc sends one MCP request with token.
func (a *App) Rpc(t *testing.T, token, method string, params any) Resp {
	t.Helper()
	return a.Do(t, http.MethodPost, "/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params},
		map[string]string{"Content-Type": "application/json", "Authorization": "Bearer " + token, "MCP-Protocol-Version": "2025-06-18"})
}

// ToolResultOf is a tools/call answer's text, structured content and isError.
func ToolResultOf(t *testing.T, r Resp) (string, map[string]any, bool) {
	t.Helper()
	res, ok := r.JSON(t)["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result: %d %s", r.Status, r.Body)
	}
	text := res["content"].([]any)[0].(map[string]any)["text"].(string)
	s, _ := res["structuredContent"].(map[string]any)
	return text, s, res["isError"].(bool)
}

// KeysOf lists the keys of key's account.
func (a *App) KeysOf(t *testing.T, key string) []map[string]any {
	t.Helper()
	r := a.Do(t, http.MethodGet, "/v1/me/keys", nil, map[string]string{"X-API-Key": key})
	if r.Status != http.StatusOK {
		t.Fatalf("list keys: %d %s", r.Status, r.Body)
	}
	raw, _ := r.JSON(t)["keys"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, k := range raw {
		out = append(out, k.(map[string]any))
	}
	return out
}

// ReviewerSignIn posts the reviewer sign-in form.
func (a *App) ReviewerSignIn(t *testing.T, email, password string, headers map[string]string) Resp {
	t.Helper()
	h := map[string]string{"Content-Type": "application/json", "Origin": a.Srv.URL}
	for k, v := range headers {
		h[k] = v
	}
	return a.Do(t, http.MethodPost, "/oauth/reviewer-signin", map[string]string{"email": email, "password": password}, h)
}

// AuthorizeData is the consent page's request data for clientID.
func (a *App) AuthorizeData(t *testing.T, clientID string) map[string]any {
	t.Helper()
	_, challenge := NewVerifier()
	page := a.Do(t, http.MethodGet, "/oauth/authorize?"+AuthorizeQuery(clientID, TestRedirect, challenge).Encode(), nil, nil)
	m := ConnectDataRe.FindSubmatch(page.Body)
	if page.Status != http.StatusOK || m == nil {
		t.Fatalf("authorize page: %d", page.Status)
	}
	var data map[string]any
	_ = json.Unmarshal(m[1], &data)
	return data
}

// ---- mail ----

// Mailbox captures sign-in codes and notices.
type Mailbox struct {
	Mu      sync.Mutex
	Codes   map[string]string // address -> last sign-in code
	Notices []string          // "to|subject|text"
}

func (m *Mailbox) SendSignInCode(to, code, _ string) error {
	m.Mu.Lock()
	defer m.Mu.Unlock()
	if m.Codes == nil {
		m.Codes = map[string]string{}
	}
	m.Codes[to] = code
	return nil
}

func (m *Mailbox) SendNotice(to, subject, text string) error {
	m.Mu.Lock()
	defer m.Mu.Unlock()
	m.Notices = append(m.Notices, to+"|"+subject+"|"+text)
	return nil
}

// Code is the last sign-in code sent to address.
func (m *Mailbox) Code(address string) string {
	m.Mu.Lock()
	defer m.Mu.Unlock()
	return m.Codes[address]
}

// To is every notice sent to address.
func (m *Mailbox) To(address string) []string {
	m.Mu.Lock()
	defer m.Mu.Unlock()
	var out []string
	for _, n := range m.Notices {
		if strings.HasPrefix(n, address+"|") {
			out = append(out, n)
		}
	}
	return out
}

// NewAccountApp is an App with a capturing mailbox, synchronous sign-in
// alerts and a roomy email limiter.
func NewAccountApp(t *testing.T, eds ...*edition.Edition) (*App, *Mailbox) {
	t.Helper()
	mb := &Mailbox{}
	a := NewAppMailer(t, mb, eds...)
	a.Users.Alerts.Run = func(f func()) { f() }
	a.Users.EmailLimiter = httpx.NewRateLimiter(1000, 1000)
	return a, mb
}

var (
	confirmCodeRe = regexp.MustCompile(`confirmation code: (\d{6})`)
	currentCodeRe = regexp.MustCompile(`together with the one sent to the new address:\s+(\d{6})`)
)

// RequestEmailChange asks to move key's account to newAddr and returns the
// codes mailed to the new and the current address.
func RequestEmailChange(t *testing.T, a *App, mb *Mailbox, key map[string]string, current, newAddr string) (newCode, curCode string) {
	t.Helper()
	r := a.At(t, "POST", rt.SiteDomain, "/v1/me/email", map[string]string{"email": newAddr}, key)
	if r.Status != 202 || r.JSON(t)["current_email"] != current {
		t.Fatalf("request change: %d %s", r.Status, r.Body)
	}
	got := mb.To(strings.ToLower(newAddr))
	m := confirmCodeRe.FindStringSubmatch(got[len(got)-1])
	if m == nil {
		t.Fatalf("no code for the new address: %v", got)
	}
	cur := mb.To(current)
	c := currentCodeRe.FindStringSubmatch(cur[len(cur)-1])
	if c == nil {
		t.Fatalf("no code for the current address: %v", cur)
	}
	return m[1], c[1]
}

// SignInWith signs address in by email code from a browser with ua.
func SignInWith(t *testing.T, a *App, mb *Mailbox, address, ua string) {
	t.Helper()
	if r := a.At(t, "POST", a.AppHost(), "/v1/auth", map[string]string{"email": address}, map[string]string{"User-Agent": ua}); r.Status != 202 {
		t.Fatalf("sign-in request: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "POST", a.AppHost(), "/v1/auth/verify", map[string]string{"email": address, "code": mb.Code(address)}, map[string]string{"User-Agent": ua}); r.Status != 200 {
		t.Fatalf("verify: %d %s", r.Status, r.Body)
	}
}

const (
	UaMacChrome = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36"
	UaIPhone    = "Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1"
)

// ---- visitor sign-in (OAuth) and SSO ----

const TestNonce = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// NonceHash is how a visitor sign-in stores nonce n.
func NonceHash(n string) sql.NullString {
	sum := sha256.Sum256([]byte(n))
	return sql.NullString{String: base64.RawURLEncoding.EncodeToString(sum[:]), Valid: true}
}

// NonceCookie is the visitor sign-in nonce cookie n.
func NonceCookie(n string) map[string]string {
	return map[string]string{"Cookie": accesshttp.VisitorNonceCookieHost + "=" + n}
}

// CookieOf is the visitor session cookie raw.
func CookieOf(raw string) map[string]string {
	return map[string]string{"Cookie": rt.VisitorCookie + "=" + raw}
}

// FakeProvider is a Google that signs in g-<code> as <code>@example.test.
type FakeProvider struct{}

func (FakeProvider) Name() string { return "google" }
func (FakeProvider) AuthCodeURL(state, _ string) string {
	return "https://accounts.example.test/auth?state=" + url.QueryEscape(state)
}
func (FakeProvider) Exchange(_ context.Context, code, _ string) (accounts.ProviderIdentity, error) {
	return accounts.ProviderIdentity{Provider: "google", UserID: "g-" + code, Email: code + "@example.test", EmailVerified: true}, nil
}

// SSOOIDCProvider is FakeProvider as a company (OIDC) sign-in.
type SSOOIDCProvider struct{ FakeProvider }

func (SSOOIDCProvider) Name() string { return "oidc" }
func (SSOOIDCProvider) Exchange(ctx context.Context, code, verifier string) (accounts.ProviderIdentity, error) {
	id, err := (FakeProvider{}).Exchange(ctx, code, verifier)
	id.Provider = "oidc"
	return id, err
}

// WithOAuth registers the OAuth sign-in routes with FakeProvider as Google.
func (a *App) WithOAuth(t *testing.T) *accesshttp.OAuthHandler {
	t.Helper()
	oh := &accesshttp.OAuthHandler{
		Database:      a.Database,
		Limits:        a.Access.Limits,
		Cfg:           accesshttp.OAuthSettings{PublicBaseURL: "https://" + rt.SiteDomain, SiteDomain: rt.SiteDomain, ContentHost: rt.ContentHost, CNAMETarget: "cname." + rt.SiteDomain},
		Accounts:      accounts.New(a.Database),
		SignIn:        accounts.NewProviderSignIn(accounts.New(a.Database), map[string]accounts.Provider{"google": FakeProvider{}}),
		IPLimiter:     httpx.NewRateLimiter(10000, 10000),
		SSOKeyLimiter: httpx.NewRateLimiter(10000, 10000),
		LookupIP:      func(context.Context, string) ([]net.IP, error) { return nil, nil },
		LookupCNAME:   func(context.Context, string) (string, error) { return "cname." + rt.SiteDomain + ".", nil },
	}
	oh.SetPersonSiteResolver(a.Hosts.ReturnSite)
	oh.SetPasscodeCheck(a.Access.PasscodeLetsIn)
	oh.Register(a.Mux)
	return oh
}

// newSSOApp is an App with OAuth sign-in and the SSO hop on the app's own address.
func newSSOApp(t *testing.T) (*App, *accesshttp.OAuthHandler) {
	t.Helper()
	a := NewApp(t)
	o := a.WithOAuth(t)
	o.Cfg.PublicBaseURL = a.Srv.URL
	a.Access.SetSSOHop(o)
	return a, o
}

// SignInStart starts a visitor sign-in on host and returns the provider
// state and the nonce cookie.
func (a *App) SignInStart(t *testing.T, host, returnTo string) (state, nonce string) {
	t.Helper()
	r := a.At(t, "GET", host, "/v1/visitor/oauth/google?return_to="+url.QueryEscape(returnTo), nil, nil)
	loc, _ := url.Parse(r.Header.Get("Location"))
	if r.Status != http.StatusFound || loc == nil || loc.Host != "accounts.example.test" {
		t.Fatalf("start on %s: %d %q %s", host, r.Status, r.Header.Get("Location"), r.Body)
	}
	sc := r.Header.Values("Set-Cookie")
	if len(sc) != 1 || !strings.HasPrefix(sc[0], accesshttp.VisitorNonceCookieHost+"=") {
		t.Fatalf("start cookie: %v", sc)
	}
	nonce = strings.TrimPrefix(strings.SplitN(sc[0], ";", 2)[0], accesshttp.VisitorNonceCookieHost+"=")
	return loc.Query().Get("state"), nonce
}

// SignInCallback finishes the provider leg as who and returns the
// establish address on host.
func (a *App) SignInCallback(t *testing.T, state, who, host string) *url.URL {
	t.Helper()
	r := a.At(t, "GET", rt.SiteDomain, "/v1/auth/oauth/google/callback?state="+url.QueryEscape(state)+"&code="+who, nil, nil)
	loc, _ := url.Parse(r.Header.Get("Location"))
	if r.Status != http.StatusFound || loc == nil || !strings.EqualFold(loc.Host, host) || loc.Path != "/v1/visitor/establish" {
		t.Fatalf("callback: %d %q %s", r.Status, r.Header.Get("Location"), r.Body)
	}
	return loc
}

// ResponseCookie is the value of cookie name set by r.
func ResponseCookie(t *testing.T, r Resp, name string) string {
	t.Helper()
	for _, c := range (&http.Response{Header: r.Header}).Cookies() {
		if c.Name == name {
			return c.Value
		}
	}
	t.Fatalf("missing %s cookie (status %d)", name, r.Status)
	return ""
}

// SSOPass is the SSO pass cookie for key's account.
func (a *App) SSOPass(t *testing.T, key string) string {
	t.Helper()
	r := a.At(t, "POST", a.AppHost(), "/v1/sso/session", nil, map[string]string{"X-API-Key": key, "Origin": "https://" + a.AppHost(), "Sec-Fetch-Site": "same-origin"})
	if r.Status != 204 {
		t.Fatalf("bootstrap: %d %s", r.Status, r.Body)
	}
	return ResponseCookie(t, r, accesshttp.SSOCookieHost)
}

// SSOStartAt starts the SSO hop on host and returns its state and nonce.
func (a *App) SSOStartAt(t *testing.T, host string) (state, nonce string) {
	t.Helper()
	r := a.At(t, "GET", host, "/v1/sso/start?return_to="+url.QueryEscape("https://"+host+"/page?ok=1"), nil, nil)
	if r.Status != 302 {
		t.Fatalf("start: %d %s", r.Status, r.Body)
	}
	u, _ := url.Parse(r.Header.Get("Location"))
	return u.Query().Get("state"), ResponseCookie(t, r, accesshttp.VisitorNonceCookieHost)
}

// SSOHop is the hop on the app's address with pass.
func (a *App) SSOHop(t *testing.T, state, pass string) Resp {
	t.Helper()
	return a.At(t, "GET", a.AppHost(), "/v1/sso/hop?state="+state, nil, map[string]string{"Cookie": accesshttp.SSOCookieHost + "=" + pass, "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "document"})
}

// ConsentToken is the SSO consent page's form token.
func ConsentToken(t *testing.T, r Resp) string {
	t.Helper()
	m := regexp.MustCompile(`name="form_token" value="([^"]+)"`).FindSubmatch(r.Body)
	if len(m) != 2 {
		t.Fatalf("not consent: %d %s", r.Status, r.Body)
	}
	return string(m[1])
}

// SSOConsent posts the SSO consent form.
func (a *App) SSOConsent(t *testing.T, state, pass, token, origin, fetchSite string) Resp {
	t.Helper()
	return a.At(t, "POST", a.AppHost(), "/v1/sso/consent", url.Values{"state": {state}, "form_token": {token}}.Encode(), map[string]string{"Content-Type": "application/x-www-form-urlencoded", "Cookie": accesshttp.SSOCookieHost + "=" + pass, "Origin": origin, "Sec-Fetch-Site": fetchSite})
}

// FinishSSO runs the whole hop to host with pass and returns the visitor
// cookie and the state used.
func (a *App) FinishSSO(t *testing.T, host, pass string) (string, string) {
	t.Helper()
	state, nonce := a.SSOStartAt(t, host)
	r := a.SSOHop(t, state, pass)
	if r.Status == 200 {
		r = a.SSOConsent(t, state, pass, ConsentToken(t, r), "https://"+a.AppHost(), "same-origin")
	}
	if r.Status != 302 {
		t.Fatalf("hop: %d %s", r.Status, r.Body)
	}
	u, _ := url.Parse(r.Header.Get("Location"))
	r = a.At(t, "GET", host, u.RequestURI(), nil, NonceCookie(nonce))
	if r.Status != 302 {
		t.Fatalf("establish: %d %s", r.Status, r.Body)
	}
	if r.Header.Get("Location") != "https://"+host+"/page?ok=1" {
		t.Fatalf("return changed: %s", r.Header.Get("Location"))
	}
	return ResponseCookie(t, r, accesshttp.VisitorCookieHost), state
}

// ---- passcodes ----

// PasscodeCanary is in every file of a PasscodeSite.
const PasscodeCanary = "CANARY-7f3a91-do-not-leak"

// PasscodeSite is olive's site "trip" (a canary in every kind of file) on
// an App with passcodes on.
type PasscodeSite struct {
	*App
	Olive        Person
	SiteID, Host string
	Person       string // olive's person host
	Okey         map[string]string
	PageDir      string
}

// NewPasscodeSite builds the App and olive's site.
func NewPasscodeSite(t *testing.T) *PasscodeSite {
	t.Helper()
	return NewPasscodeSiteOn(t, NewApp(t))
}

// NewPasscodeSiteOn is NewPasscodeSite on a.
func NewPasscodeSiteOn(t *testing.T, a *App) *PasscodeSite {
	t.Helper()
	a.EnablePasscodes(t)
	p := &PasscodeSite{App: a, Olive: a.NewPerson(t, "olive")}
	p.Okey = p.Olive.Header()
	p.SiteID = rt.Deploy(t, a.Database, a.Disk, p.Olive, "trip", map[string]string{
		"index.html":      "<h1>trip " + PasscodeCanary + "</h1>",
		"app.js":          "var s='" + PasscodeCanary + "';",
		"data.json":       `{"x":"` + PasscodeCanary + `"}`,
		"img.svg":         `<svg xmlns="http://www.w3.org/2000/svg"><text>` + PasscodeCanary + `</text></svg>`,
		"days/index.html": "day " + PasscodeCanary,
		"404.html":        "missing " + PasscodeCanary,
		"robots.txt":      "User-agent: *\nAllow: /\n# " + PasscodeCanary,
	})
	a.MarkReady(t, p.Olive)
	p.Person = p.Olive.Handle + "." + rt.SiteDomain
	p.Host = p.Olive.SiteHost("trip")
	p.PageDir = a.Disk.SiteDir(p.Olive.ID, "trip")
	return p
}

// Lock sets the site's passcode.
func (p *PasscodeSite) Lock(t *testing.T, code string) Resp {
	t.Helper()
	return p.At(t, "PUT", rt.SiteDomain, "/v1/sites/trip/lock", map[string]any{"passcode": code}, p.Okey)
}

// Unlock posts the gate's form from host.
func (p *PasscodeSite) Unlock(t *testing.T, host, code, next string, extra map[string]string) Resp {
	t.Helper()
	form := url.Values{"passcode": {code}, "next": {next}}.Encode()
	h := map[string]string{"Origin": "https://" + host, "Content-Type": "application/x-www-form-urlencoded", "Sec-Fetch-Site": "same-origin"}
	for k, v := range extra {
		h[k] = v
	}
	return p.At(t, "POST", host, "/v1/site-unlock", form, h)
}

// Grant names viewers of the site.
func (p *PasscodeSite) Grant(t *testing.T, emails ...string) Resp {
	t.Helper()
	return p.At(t, "POST", rt.SiteDomain, "/v1/sites/trip/viewers", map[string]any{"emails": emails}, p.Okey)
}

// CookieHeader is the unlock cookie r set, as a Cookie header value.
func CookieHeader(t *testing.T, r Resp) string {
	t.Helper()
	for _, c := range (&http.Response{Header: r.Header}).Cookies() {
		if strings.Contains(c.Name, "sh_pass_") {
			return c.Name + "=" + c.Value
		}
	}
	t.Fatalf("no unlock cookie in %v", r.Header)
	return ""
}

// MarkReady gives p's handle its site-host certificate (Deploy does it too).
func (a *App) MarkReady(t *testing.T, p Person) {
	t.Helper()
	markReady(t, a.certDir, p.Handle)
}

// ---- teams ----

var teamSeq atomic.Int64

func teamKey(p Person) map[string]string { return p.Header() }

// newTeam has p create a team named after label, returning its handle.
func newTeam(t *testing.T, a *App, p Person, label string) string {
	t.Helper()
	name := fmt.Sprintf("%s-%s-%d", label, strconv.FormatInt(time.Now().UnixNano()%1e11, 36), teamSeq.Add(1))
	r := a.At(t, "POST", rt.SiteDomain, "/v1/teams", map[string]string{"name": name}, teamKey(p))
	if r.Status != http.StatusCreated || r.JSON(t)["handle"] != name {
		t.Fatalf("create team: %d %s", r.Status, r.Body)
	}
	t.Cleanup(func() { _, _ = a.Database.Exec(`DELETE FROM users WHERE handle = $1 AND is_team`, name) })
	return name
}

// teamPerson is team as a Person (no key) for repo-level deploys.
func teamPerson(t *testing.T, a *App, team string) Person {
	t.Helper()
	var id string
	if err := a.Database.QueryRow(`SELECT id FROM users WHERE handle = $1 AND is_team`, team).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return Person{ID: id, Handle: team}
}

// invite has by invite email to team.
func invite(t *testing.T, a *App, by Person, team, email string) Resp {
	t.Helper()
	return a.At(t, "POST", rt.SiteDomain, "/v1/teams/"+team+"/members", map[string]string{"email": email}, teamKey(by))
}

// invitationTo is the id of p's invitation to team (GET /v1/me), or "".
func invitationTo(t *testing.T, a *App, p Person, team string) string {
	t.Helper()
	r := a.At(t, "GET", rt.SiteDomain, "/v1/me", nil, teamKey(p))
	list, _ := r.JSON(t)["team_invitations"].([]any)
	for _, v := range list {
		if v := v.(map[string]any); v["team"] == team {
			return v["id"].(string)
		}
	}
	return ""
}

func answer(t *testing.T, a *App, p Person, id, what string) Resp {
	t.Helper()
	return a.At(t, "POST", rt.SiteDomain, "/v1/me/team-invitations/"+id, map[string]string{"answer": what}, teamKey(p))
}

// addMember has by invite who, and who accept.
func addMember(t *testing.T, a *App, by Person, team string, who Person) {
	t.Helper()
	if r := invite(t, a, by, team, who.Email); r.Status != http.StatusAccepted {
		t.Fatalf("invite: %d %s", r.Status, r.Body)
	}
	if r := answer(t, a, who, invitationTo(t, a, who, team), "accept"); r.Status != http.StatusOK {
		t.Fatalf("accept: %d %s", r.Status, r.Body)
	}
}
