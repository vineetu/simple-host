package handler

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	_ "github.com/lib/pq"

	"github.com/vsriram/simple-host/internal/auth"
	db "github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/email"
	"github.com/vsriram/simple-host/internal/storage"
)

// ---- pure rules (no database) ---------------------------------------------------

func TestValidRedirectURI(t *testing.T) {
	good := []string{
		"https://chatgpt.com/connector_platform_oauth_redirect",
		"https://claude.ai/api/mcp/auth_callback",
		"http://localhost:33418/callback",
		"http://127.0.0.1:8080/cb",
		"http://[::1]:9000/cb",
		"https://app.example.com/cb?x=1",
	}
	bad := []string{
		"", "http://example.com/cb", "https://example.com/cb#frag", "javascript:alert(1)",
		"https://user:pw@example.com/cb", "/relative", "ftp://example.com/", "https:///nohost",
		"http://localhost.evil.com/cb", "https://exa mple.com/",
	}
	for _, u := range good {
		if !validRedirectURI(u) {
			t.Errorf("refused %q", u)
		}
	}
	for _, u := range bad {
		if validRedirectURI(u) {
			t.Errorf("accepted %q", u)
		}
	}
}

func TestPKCE(t *testing.T) {
	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	challenge := "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM" // RFC 7636 appendix B
	if !pkceS256(verifier, challenge) {
		t.Fatal("RFC 7636 example did not verify")
	}
	if pkceS256(verifier+"x", challenge) || pkceS256(challenge, challenge) {
		t.Fatal("wrong verifier accepted")
	}
	if validPKCEValue("short") || validPKCEValue(strings.Repeat("a", 129)) || validPKCEValue(strings.Repeat("a", 42)+"!") {
		t.Fatal("malformed PKCE value accepted")
	}
}

func TestNormalizeResource(t *testing.T) {
	h := &ConnectorHandler{issuer: "https://simple-host.app", mcpResource: "https://simple-host.app/mcp"}
	for in, want := range map[string]string{
		"":                             "https://simple-host.app",
		"https://simple-host.app":      "https://simple-host.app",
		"https://simple-host.app/":     "https://simple-host.app",
		"https://simple-host.app/mcp":  "https://simple-host.app/mcp",
		"https://simple-host.app/mcp/": "https://simple-host.app/mcp",
	} {
		if got, ok := h.normalizeResource(in); !ok || got != want {
			t.Errorf("%q -> %q %v", in, got, ok)
		}
	}
	for _, in := range []string{"https://evil.example/mcp", "https://simple-host.app/v1", "https://simple-host.app/mcp#x", "mcp"} {
		if _, ok := h.normalizeResource(in); ok {
			t.Errorf("accepted resource %q", in)
		}
	}
}

func TestCleanClientName(t *testing.T) {
	if got := cleanClientName("  Claude‮\n  Desktop  "); got != "Claude Desktop" {
		t.Errorf("got %q", got)
	}
	if got := cleanClientName(""); got != "An app" {
		t.Errorf("got %q", got)
	}
	if got := cleanClientName(strings.Repeat("x", 200)); len([]rune(got)) != 60 {
		t.Errorf("not truncated: %d", len(got))
	}
}

func TestConnectReturnTo(t *testing.T) {
	base := "https://simple-host.app"
	cn := strings.Repeat("A", 43)
	ok := func(s string) bool { u, _ := url.Parse(s); return connectReturnToOK(u, base) }
	if !ok("https://simple-host.app/oauth/authorize?client_id=x&state=y&cn=" + cn) {
		t.Error("consent page refused")
	}
	for _, s := range []string{
		"https://simple-host.app/oauth/authorize?client_id=x",             // no nonce hash
		"https://simple-host.app/oauth/authorize?cn=short",                // malformed
		"https://simple-host.app/oauth/authorize?cn=" + cn + "&cn=" + cn,  // repeated
		"https://evil.example/oauth/authorize?cn=" + cn,                   // other host
		"https://simple-host.app/oauth/authorize?cn=" + cn + "&token=abc", // pre-loaded token
		"https://simple-host.app/oauth/authorize?cn=" + cn + "#frag",      // fragment
		"https://simple-host.app/oauth/authorizeX?cn=" + cn,               // other path
		"http://simple-host.app/oauth/authorize?cn=" + cn,                 // other scheme
	} {
		if ok(s) {
			t.Errorf("accepted %q", s)
		}
	}
	got := ownerLandingURL("https://simple-host.app/oauth/authorize?client_id=x&cn="+cn, base, "TOK")
	if got != "https://simple-host.app/oauth/authorize?client_id=x&cn="+cn+"&token=TOK" {
		t.Errorf("landing %q", got)
	}
	// Without a nonce hash no token lands anywhere.
	for _, rt := range []string{"https://simple-host.app/oauth/authorize?client_id=x", "https://simple-host.app/"} {
		if got := ownerLandingURL(rt, base, "TOK"); got != "https://simple-host.app/" {
			t.Errorf("unbound landing for %s: %q", rt, got)
		}
	}
	if got := ownerLandingURL("https://simple-host.app/?cn="+cn, base, "TOK"); got != "https://simple-host.app/?token=TOK&cn="+cn {
		t.Errorf("dashboard landing %q", got)
	}
	d := func(s string) bool { u, _ := url.Parse(s); return dashboardReturnToOK(u, base) }
	if !d("https://simple-host.app/?cn="+cn) || d("https://simple-host.app/") || d("https://simple-host.app/?cn="+cn+"&x=1") || d("https://simple-host.app/x?cn="+cn) {
		t.Error("dashboardReturnToOK rules")
	}
	if !nonceMatches("abc", "ungWv48Bz-pBQUDeXa4iI7ADYaOWF3qctBD_YfIAFa0") || nonceMatches("abd", "ungWv48Bz-pBQUDeXa4iI7ADYaOWF3qctBD_YfIAFa0") || nonceMatches("", "") {
		t.Error("nonceMatches")
	}
}

func TestConsentCSRF(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	h := &ConnectorHandler{consentKey: []byte("k"), now: func() time.Time { return now }}
	req := authzRequest{Client: db.OAuthClient{ClientID: "c"}, RedirectURI: "https://a/cb", CodeChallenge: "x", State: "s", Resource: "r"}
	tok := h.consentCSRF(req, now.Unix())
	if !h.checkConsentCSRF(req, tok) {
		t.Fatal("fresh token refused")
	}
	other := req
	other.RedirectURI = "https://b/cb"
	if h.checkConsentCSRF(other, tok) {
		t.Fatal("token accepted for a different request")
	}
	h.now = func() time.Time { return now.Add(oauthConsentTTL + time.Second) }
	if h.checkConsentCSRF(req, tok) {
		t.Fatal("expired token accepted")
	}
}

// ---- the whole flow against Postgres ---------------------------------------------

type connectorApp struct {
	srv      *httptest.Server
	database *sql.DB
	admin    string
}

// newConnectorApp wires the server the way cmd/server/main.go does. It needs
// DB_DSN pointing at a database with db/schema.sql applied.
func newConnectorApp(t *testing.T) *connectorApp {
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
	if err := db.VerifySchema(context.Background(), database); err != nil {
		t.Fatalf("test database is behind the schema: %v", err)
	}
	disk, err := storage.NewDiskStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	adminKey, _ := auth.GenerateAPIKey()
	rowKey, _ := auth.GenerateAPIKey()
	adminID, err := db.EnsureAdminUser(context.Background(), database, rowKey)
	if err != nil {
		t.Fatal(err)
	}
	app := &connectorApp{database: database, admin: adminKey}
	mux := http.NewServeMux()
	authMW := auth.Middleware(adminKey, adminID, database)
	noticeMW := NoticeMiddleware("1.0.0")
	mailer := email.NewResendSender("", "test@example.com")

	// The issuer has to be the test server's own URL, so start it first and
	// hand it the finished handler afterwards.
	var root http.Handler
	app.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { root.ServeHTTP(w, r) }))
	t.Cleanup(app.srv.Close)

	users := NewUserHandler(database, mailer, app.srv.URL)
	users.Register(mux, authMW, noticeMW)
	sites := NewSiteHandler(database, disk, "simple-host.test", "sites.simple-host.test", "cname.simple-host.test", "", "", adminKey, nil, 0, "on", adminID, mailer, users.EmailLimiter())
	sites.Register(mux, authMW, noticeMW)
	conn := NewConnectorHandler(database, app.srv.URL, adminKey, "simple-host.test", "sites.simple-host.test", "1.0.0", mux)
	conn.Register(mux, authMW)
	root = CORS(conn.BearerAuth(mux))
	return app
}

type person struct{ email, key string }

func (a *connectorApp) newPerson(t *testing.T, label string) person {
	t.Helper()
	addr := label + "-" + strconv.FormatInt(time.Now().UnixNano(), 36) + "@example.com"
	key, _ := auth.GenerateAPIKey()
	u, err := db.CreateUser(context.Background(), a.database, addr, key, false)
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

func (a *connectorApp) do(t *testing.T, method, path string, body io.Reader, headers map[string]string) resp {
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

func jsonBody(v any) io.Reader {
	b, _ := json.Marshal(v)
	return strings.NewReader(string(b))
}

func (a *connectorApp) form(t *testing.T, path string, v url.Values, headers map[string]string) resp {
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

func (a *connectorApp) registerClient(t *testing.T, redirect string) string {
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
func (a *connectorApp) consent(t *testing.T, q url.Values, key, decision string) string {
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
func (a *connectorApp) connect(t *testing.T, p person, clientID, redirect string) map[string]any {
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

func (a *connectorApp) rpc(t *testing.T, token, method string, params any) resp {
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

func TestConnectorMetadataAndChallenge(t *testing.T) {
	a := newConnectorApp(t)
	r := a.do(t, http.MethodPost, "/mcp", jsonBody(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize"}), nil)
	want := `Bearer resource_metadata="` + a.srv.URL + `/.well-known/oauth-protected-resource/mcp"`
	if r.status != http.StatusUnauthorized || !strings.HasPrefix(r.header.Get("WWW-Authenticate"), want) {
		t.Fatalf("unauthenticated /mcp: %d %q", r.status, r.header.Get("WWW-Authenticate"))
	}
	prm := a.do(t, http.MethodGet, "/.well-known/oauth-protected-resource/mcp", nil, nil).json(t)
	if prm["resource"] != a.srv.URL+"/mcp" || prm["authorization_servers"].([]any)[0] != a.srv.URL {
		t.Fatalf("protected resource metadata: %v", prm)
	}
	asm := a.do(t, http.MethodGet, "/.well-known/oauth-authorization-server", nil, nil).json(t)
	if asm["issuer"] != a.srv.URL || asm["token_endpoint"] != a.srv.URL+"/oauth/token" ||
		asm["registration_endpoint"] != a.srv.URL+"/oauth/register" {
		t.Fatalf("authorization server metadata: %v", asm)
	}
	if m := asm["code_challenge_methods_supported"].([]any); len(m) != 1 || m[0] != "S256" {
		t.Fatalf("PKCE methods: %v", m)
	}
	pre := a.do(t, http.MethodOptions, "/mcp", nil, map[string]string{"Origin": "https://inspector.example", "Access-Control-Request-Headers": "authorization, mcp-protocol-version"})
	if pre.header.Get("Access-Control-Allow-Origin") != "*" || !strings.Contains(pre.header.Get("Access-Control-Allow-Headers"), "Authorization") {
		t.Fatalf("CORS preflight on /mcp: %v", pre.header)
	}
	bad := a.do(t, http.MethodPost, "/mcp", jsonBody(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "ping"}), map[string]string{"Authorization": "Bearer shat_nope"})
	if bad.status != http.StatusUnauthorized || !strings.Contains(bad.header.Get("WWW-Authenticate"), `error="invalid_token"`) {
		t.Fatalf("bad token: %d %q", bad.status, bad.header.Get("WWW-Authenticate"))
	}
}

func TestConnectorRegistration(t *testing.T) {
	a := newConnectorApp(t)
	h := map[string]string{"Content-Type": "application/json"}
	for _, body := range []map[string]any{
		{"redirect_uris": []string{"http://evil.example/cb"}},
		{"redirect_uris": []string{}},
		{"redirect_uris": []string{"https://ok.example/cb"}, "grant_types": []string{"password"}},
		{"redirect_uris": []string{"https://ok.example/cb"}, "token_endpoint_auth_method": "private_key_jwt"},
	} {
		if r := a.do(t, http.MethodPost, "/oauth/register", jsonBody(body), h); r.status != http.StatusBadRequest {
			t.Errorf("accepted %v: %d", body, r.status)
		}
	}
	conf := a.do(t, http.MethodPost, "/oauth/register", jsonBody(map[string]any{"redirect_uris": []string{"https://ok.example/cb"}, "client_name": "Conf"}), h).json(t)
	if conf["token_endpoint_auth_method"] != "client_secret_basic" || !strings.HasPrefix(conf["client_secret"].(string), prefixSecret) {
		t.Fatalf("default registration should be confidential: %v", conf)
	}
	var stored string
	_ = a.database.QueryRow(`SELECT client_secret_hash FROM oauth_clients WHERE client_id = $1`, conf["client_id"]).Scan(&stored)
	if stored == conf["client_secret"] || stored != hashSecret(conf["client_secret"].(string)) {
		t.Fatal("client secret not stored as a hash")
	}
}

func TestConnectorHappyPathPublishesThroughMCP(t *testing.T) {
	a := newConnectorApp(t)
	ann := a.newPerson(t, "ann")
	clientID := a.registerClient(t, testRedirect)

	verifier, challenge := newVerifier()
	q := authorizeQuery(clientID, testRedirect, challenge)
	q.Set("resource", a.srv.URL+"/mcp")
	page := a.do(t, http.MethodGet, "/oauth/authorize?"+q.Encode(), nil, nil)
	if !strings.Contains(page.header.Get("Content-Security-Policy"), "frame-ancestors 'none'") || page.header.Get("X-Frame-Options") != "DENY" {
		t.Fatal("consent page can be framed")
	}
	if !strings.Contains(string(page.body), `class="sh-header"`) || !strings.Contains(string(page.body), "Test Chat") {
		t.Fatal("consent page lacks the site chrome or the app name")
	}
	redirectTo := a.consent(t, q, ann.key, "allow")
	u, _ := url.Parse(redirectTo)
	if !strings.HasPrefix(redirectTo, testRedirect+"?") || u.Query().Get("state") != "st4te" || u.Query().Get("iss") != a.srv.URL {
		t.Fatalf("redirect %s", redirectTo)
	}
	tok := a.form(t, "/oauth/token", url.Values{
		"grant_type": {"authorization_code"}, "code": {u.Query().Get("code")}, "redirect_uri": {testRedirect},
		"client_id": {clientID}, "code_verifier": {verifier}, "resource": {a.srv.URL + "/mcp"},
	}, nil)
	if tok.status != http.StatusOK || tok.header.Get("Cache-Control") != "no-store" {
		t.Fatalf("token: %d %s", tok.status, tok.body)
	}
	tokens := tok.json(t)
	access := tokens["access_token"].(string)
	if !strings.HasPrefix(access, prefixAccess) || tokens["expires_in"].(float64) != 3600 || tokens["token_type"] != "Bearer" {
		t.Fatalf("tokens: %v", tokens)
	}
	var n int
	_ = a.database.QueryRow(`SELECT count(*) FROM oauth_tokens WHERE token_hash = $1`, access).Scan(&n)
	if n != 0 {
		t.Fatal("access token stored in the clear")
	}

	init := a.rpc(t, access, "initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "t", "version": "1"}})
	if init.status != 200 || init.json(t)["result"].(map[string]any)["protocolVersion"] != "2025-06-18" {
		t.Fatalf("initialize: %s", init.body)
	}
	list := a.rpc(t, access, "tools/list", map[string]any{})
	if !strings.Contains(string(list.body), `"deploy_site"`) {
		t.Fatalf("tools/list: %s", list.body)
	}
	text, s, isErr := toolResultOf(t, a.rpc(t, access, "tools/call", map[string]any{"name": "deploy_site", "arguments": map[string]any{
		"site": "hello-connector", "mode": "create", "files": map[string]any{"index.html": "<h1>hi</h1>"},
	}}))
	if isErr || s["active_version"].(float64) != 1 || !strings.Contains(s["url"].(string), "/hello-connector/") {
		t.Fatalf("deploy_site: %s", text)
	}
	text, s, isErr = toolResultOf(t, a.rpc(t, access, "tools/call", map[string]any{"name": "who_am_i", "arguments": map[string]any{}}))
	if isErr || s["email"] != ann.email {
		t.Fatalf("who_am_i: %s", text)
	}
	text, _, isErr = toolResultOf(t, a.rpc(t, access, "tools/call", map[string]any{"name": "read_site_file", "arguments": map[string]any{"site": "hello-connector", "path": "index.html"}}))
	if isErr || text != "<h1>hi</h1>" {
		t.Fatalf("read_site_file: %q", text)
	}

	conns := a.do(t, http.MethodGet, "/v1/me/connections", nil, map[string]string{"X-API-Key": ann.key}).json(t)["connections"].([]any)
	if len(conns) != 1 || conns[0].(map[string]any)["name"] != "Test Chat" {
		t.Fatalf("connections: %v", conns)
	}
}

func TestConnectorAuthorizeRefusals(t *testing.T) {
	a := newConnectorApp(t)
	ann := a.newPerson(t, "ann")
	clientID := a.registerClient(t, testRedirect)
	_, challenge := newVerifier()

	// A redirect URI the client did not register is shown to the person and
	// never followed: following it would be an open redirect.
	q := authorizeQuery(clientID, "https://attacker.example/cb", challenge)
	if r := a.do(t, http.MethodGet, "/oauth/authorize?"+q.Encode(), nil, nil); r.status != http.StatusBadRequest || r.header.Get("Location") != "" {
		t.Fatalf("unregistered redirect: %d %q", r.status, r.header.Get("Location"))
	}
	q = authorizeQuery("shc_unknown", testRedirect, challenge)
	if r := a.do(t, http.MethodGet, "/oauth/authorize?"+q.Encode(), nil, nil); r.status != http.StatusBadRequest || r.header.Get("Location") != "" {
		t.Fatalf("unknown client: %d", r.status)
	}
	for name, mutate := range map[string]func(url.Values){
		"plain PKCE":       func(v url.Values) { v.Set("code_challenge_method", "plain") },
		"missing PKCE":     func(v url.Values) { v.Del("code_challenge"); v.Del("code_challenge_method") },
		"token response":   func(v url.Values) { v.Set("response_type", "token") },
		"foreign audience": func(v url.Values) { v.Set("resource", "https://other.example/mcp") },
	} {
		q := authorizeQuery(clientID, testRedirect, challenge)
		mutate(q)
		r := a.do(t, http.MethodGet, "/oauth/authorize?"+q.Encode(), nil, nil)
		loc, _ := url.Parse(r.header.Get("Location"))
		if r.status != http.StatusFound || loc == nil || loc.Query().Get("error") == "" || loc.Query().Get("state") != "st4te" {
			t.Errorf("%s: %d %q", name, r.status, r.header.Get("Location"))
		}
	}

	q = authorizeQuery(clientID, testRedirect, challenge)
	page := a.do(t, http.MethodGet, "/oauth/authorize?"+q.Encode(), nil, nil)
	var data map[string]any
	_ = json.Unmarshal(connectDataRe.FindSubmatch(page.body)[1], &data)
	decide := func(key, csrf, origin string) int {
		h := map[string]string{"Content-Type": "application/json", "X-API-Key": key}
		if origin != "" {
			h["Origin"] = origin
		}
		return a.do(t, http.MethodPost, "/oauth/authorize/decision", jsonBody(map[string]string{"query": q.Encode(), "csrf": csrf, "decision": "allow"}), h).status
	}
	if s := decide(ann.key, "1.forged", a.srv.URL); s != http.StatusForbidden {
		t.Errorf("forged CSRF token: %d", s)
	}
	if s := decide(ann.key, data["csrf"].(string), "https://evil.example"); s != http.StatusForbidden {
		t.Errorf("cross-origin decision: %d", s)
	}
	if s := decide("", data["csrf"].(string), a.srv.URL); s != http.StatusUnauthorized {
		t.Errorf("decision without sign-in: %d", s)
	}
	if s := decide(a.admin, data["csrf"].(string), a.srv.URL); s != http.StatusForbidden {
		t.Errorf("admin key connected an app: %d", s)
	}
	deny := a.consent(t, q, ann.key, "deny")
	if u, _ := url.Parse(deny); u.Query().Get("error") != "access_denied" || u.Query().Get("code") != "" {
		t.Errorf("cancel: %s", deny)
	}
	// The decision endpoint never grants CORS to another origin.
	pre := a.do(t, http.MethodOptions, "/oauth/authorize/decision", nil, map[string]string{"Origin": "https://evil.example", "Access-Control-Request-Method": "POST"})
	if pre.header.Get("Access-Control-Allow-Origin") != "" {
		t.Error("decision endpoint is CORS-open")
	}
}

func TestConnectorCodeRules(t *testing.T) {
	a := newConnectorApp(t)
	ann := a.newPerson(t, "ann")
	clientID := a.registerClient(t, testRedirect)
	redeem := func(code, verifier, redirect string) resp {
		return a.form(t, "/oauth/token", url.Values{
			"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirect},
			"client_id": {clientID}, "code_verifier": {verifier},
		}, nil)
	}
	fresh := func() (string, string) {
		v, c := newVerifier()
		return v, codeFrom(t, a.consent(t, authorizeQuery(clientID, testRedirect, c), ann.key, "allow"))
	}
	isInvalidGrant := func(r resp) bool { return r.status == http.StatusBadRequest && r.json(t)["error"] == "invalid_grant" }

	t.Run("wrong verifier, and the code is spent", func(t *testing.T) {
		v, code := fresh()
		other, _ := newVerifier()
		if r := redeem(code, other, testRedirect); !isInvalidGrant(r) {
			t.Fatalf("wrong verifier: %d %s", r.status, r.body)
		}
		if r := redeem(code, v, testRedirect); !isInvalidGrant(r) {
			t.Fatalf("code usable after a failed attempt: %d", r.status)
		}
	})
	t.Run("missing verifier", func(t *testing.T) {
		_, code := fresh()
		if r := redeem(code, "", testRedirect); !isInvalidGrant(r) {
			t.Fatalf("%d", r.status)
		}
	})
	t.Run("wrong redirect_uri", func(t *testing.T) {
		v, code := fresh()
		if r := redeem(code, v, "https://chat.example.com/other"); !isInvalidGrant(r) {
			t.Fatalf("%d", r.status)
		}
	})
	t.Run("expired", func(t *testing.T) {
		v, code := fresh()
		_, _ = a.database.Exec(`UPDATE oauth_codes SET expires_at = now() - interval '1 second' WHERE code_hash = $1`, hashSecret(code))
		if r := redeem(code, v, testRedirect); !isInvalidGrant(r) {
			t.Fatalf("%d", r.status)
		}
	})
	t.Run("another client", func(t *testing.T) {
		v, code := fresh()
		other := a.registerClient(t, testRedirect)
		r := a.form(t, "/oauth/token", url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {testRedirect}, "client_id": {other}, "code_verifier": {v}}, nil)
		if !isInvalidGrant(r) {
			t.Fatalf("%d", r.status)
		}
	})
	t.Run("reuse revokes what the first redemption issued", func(t *testing.T) {
		v, code := fresh()
		first := redeem(code, v, testRedirect)
		if first.status != http.StatusOK {
			t.Fatalf("first: %d %s", first.status, first.body)
		}
		access := first.json(t)["access_token"].(string)
		if r := a.rpc(t, access, "ping", map[string]any{}); r.status != 200 {
			t.Fatalf("fresh token: %d", r.status)
		}
		if r := redeem(code, v, testRedirect); !isInvalidGrant(r) {
			t.Fatalf("reuse: %d", r.status)
		}
		if r := a.rpc(t, access, "ping", map[string]any{}); r.status != http.StatusUnauthorized {
			t.Fatalf("token survived code reuse: %d", r.status)
		}
	})
}

func TestConnectorRefreshRotationAndReuse(t *testing.T) {
	a := newConnectorApp(t)
	ann := a.newPerson(t, "ann")
	clientID := a.registerClient(t, testRedirect)
	tokens := a.connect(t, ann, clientID, testRedirect)
	refresh := func(rt string) resp {
		return a.form(t, "/oauth/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {rt}, "client_id": {clientID}}, nil)
	}
	r1 := refresh(tokens["refresh_token"].(string))
	if r1.status != 200 {
		t.Fatalf("refresh: %d %s", r1.status, r1.body)
	}
	t2 := r1.json(t)
	if t2["refresh_token"] == tokens["refresh_token"] {
		t.Fatal("refresh token did not rotate")
	}
	if r := a.rpc(t, t2["access_token"].(string), "ping", map[string]any{}); r.status != 200 {
		t.Fatalf("new access token: %d", r.status)
	}
	// The old refresh token again: reuse. The whole family is revoked.
	if r := refresh(tokens["refresh_token"].(string)); r.status != http.StatusBadRequest {
		t.Fatalf("reuse accepted: %d", r.status)
	}
	if r := a.rpc(t, t2["access_token"].(string), "ping", map[string]any{}); r.status != http.StatusUnauthorized {
		t.Fatal("access token from the family survived reuse")
	}
	if r := refresh(t2["refresh_token"].(string)); r.status != http.StatusBadRequest {
		t.Fatal("refresh token from the family survived reuse")
	}
	// A refresh token presented by another client is refused.
	t3 := a.connect(t, ann, clientID, testRedirect)
	other := a.registerClient(t, testRedirect)
	if r := a.form(t, "/oauth/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {t3["refresh_token"].(string)}, "client_id": {other}}, nil); r.status != http.StatusBadRequest {
		t.Fatalf("cross-client refresh: %d", r.status)
	}
}

func TestConnectorRevocation(t *testing.T) {
	a := newConnectorApp(t)
	ann := a.newPerson(t, "ann")
	clientID := a.registerClient(t, testRedirect)

	tokens := a.connect(t, ann, clientID, testRedirect)
	access := tokens["access_token"].(string)
	if r := a.form(t, "/oauth/revoke", url.Values{"token": {access}, "client_id": {clientID}}, nil); r.status != 200 {
		t.Fatalf("revoke: %d", r.status)
	}
	if r := a.rpc(t, access, "ping", map[string]any{}); r.status != http.StatusUnauthorized {
		t.Fatal("revoked access token accepted")
	}
	// Revoking a refresh token ends the connection.
	if r := a.form(t, "/oauth/revoke", url.Values{"token": {tokens["refresh_token"].(string)}, "client_id": {clientID}}, nil); r.status != 200 {
		t.Fatal("revoke refresh")
	}
	if r := a.form(t, "/oauth/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {tokens["refresh_token"].(string)}, "client_id": {clientID}}, nil); r.status != http.StatusBadRequest {
		t.Fatal("revoked refresh token accepted")
	}
	// Disconnecting from the account.
	t2 := a.connect(t, ann, clientID, testRedirect)
	if r := a.do(t, http.MethodDelete, "/v1/me/connections/"+clientID, nil, map[string]string{"X-API-Key": ann.key}); r.status != http.StatusNoContent {
		t.Fatalf("disconnect: %d", r.status)
	}
	if r := a.rpc(t, t2["access_token"].(string), "ping", map[string]any{}); r.status != http.StatusUnauthorized {
		t.Fatal("token survived disconnect")
	}
	// Rotating the API key disconnects every app.
	t3 := a.connect(t, ann, clientID, testRedirect)
	if r := a.do(t, http.MethodPost, "/v1/me/api-key/rotate", nil, map[string]string{"X-API-Key": ann.key}); r.status != 200 {
		t.Fatalf("rotate: %d", r.status)
	}
	if r := a.rpc(t, t3["access_token"].(string), "ping", map[string]any{}); r.status != http.StatusUnauthorized {
		t.Fatal("token survived key rotation")
	}
	// Deleting the person deletes their connections.
	bob := a.newPerson(t, "bob")
	t4 := a.connect(t, bob, clientID, testRedirect)
	_, _ = a.database.Exec(`DELETE FROM users WHERE username = $1`, bob.email)
	if r := a.rpc(t, t4["access_token"].(string), "ping", map[string]any{}); r.status != http.StatusUnauthorized {
		t.Fatal("token survived account deletion")
	}
}

func TestConnectorToolsHaveRESTPermissions(t *testing.T) {
	a := newConnectorApp(t)
	ann, bob := a.newPerson(t, "ann"), a.newPerson(t, "bob")
	clientID := a.registerClient(t, testRedirect)
	if r := a.do(t, http.MethodPost, "/v1/sites/bobs-page/files", jsonBody(map[string]any{"files": map[string]string{"index.html": "bob"}}),
		map[string]string{"X-API-Key": bob.key, "Content-Type": "application/json"}); r.status != http.StatusCreated {
		t.Fatalf("bob deploy: %d %s", r.status, r.body)
	}
	access := a.connect(t, ann, clientID, testRedirect)["access_token"].(string)
	for _, call := range []map[string]any{
		{"name": "delete_site", "arguments": map[string]any{"site": "bobs-page", "confirm_name": "bobs-page"}},
		{"name": "deploy_site", "arguments": map[string]any{"site": "bobs-page", "mode": "replace", "files": map[string]any{"index.html": "ann was here"}}},
		{"name": "rollback_site", "arguments": map[string]any{"site": "bobs-page", "version": 1}},
		{"name": "read_site_file", "arguments": map[string]any{"site": "bobs-page", "path": "index.html", "version": 1}},
		{"name": "set_visibility", "arguments": map[string]any{"site": "bobs-page", "visibility": "public"}},
	} {
		text, _, isErr := toolResultOf(t, a.rpc(t, access, "tools/call", call))
		if !isErr || !(strings.Contains(text, "404") || strings.Contains(text, "no site named")) {
			t.Errorf("%s on another person's site: %q", call["name"], text)
		}
	}
	var n int
	_ = a.database.QueryRow(`SELECT active_version FROM sites WHERE name = 'bobs-page'`).Scan(&n)
	if n != 1 {
		t.Fatal("bob's site changed")
	}
	// An /mcp token is not a REST credential; a whole-server token is, and it
	// has exactly the person's key's power.
	if r := a.do(t, http.MethodGet, "/v1/sites", nil, map[string]string{"Authorization": "Bearer " + access}); r.status != http.StatusUnauthorized {
		t.Fatalf("/mcp token accepted on /v1: %d", r.status)
	}
}

func TestConnectorConfidentialClientForGPTActions(t *testing.T) {
	a := newConnectorApp(t)
	ann, bob := a.newPerson(t, "ann"), a.newPerson(t, "bob")
	if r := a.do(t, http.MethodPost, "/v1/sites/bobs-other/files", jsonBody(map[string]any{"files": map[string]string{"index.html": "bob"}}),
		map[string]string{"X-API-Key": bob.key, "Content-Type": "application/json"}); r.status != http.StatusCreated {
		t.Fatalf("bob deploy: %d", r.status)
	}
	redirect := "https://chatgpt.com/aip/g-test/oauth/callback"
	id, secret, err := CreateOperatorClient(context.Background(), a.database, "Simple Host GPT", []string{redirect}, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = db.DeleteOAuthClient(context.Background(), a.database, id) })
	q := url.Values{"response_type": {"code"}, "client_id": {id}, "redirect_uri": {redirect}, "scope": {"sites"}, "state": {"gpt"}}
	code := codeFrom(t, a.consent(t, q, ann.key, "allow"))
	if r := a.form(t, "/oauth/token", url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirect}, "client_id": {id}, "client_secret": {"wrong"}}, nil); r.status != http.StatusUnauthorized {
		t.Fatalf("wrong secret: %d", r.status)
	}
	code = codeFrom(t, a.consent(t, q, ann.key, "allow"))
	tok := a.form(t, "/oauth/token", url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirect}, "client_id": {id}, "client_secret": {secret}}, nil)
	if tok.status != 200 {
		t.Fatalf("GPT token: %d %s", tok.status, tok.body)
	}
	access := tok.json(t)["access_token"].(string)
	bearer := map[string]string{"Authorization": "Bearer " + access, "Content-Type": "application/json"}
	if r := a.do(t, http.MethodPost, "/v1/sites/gpt-made/files", jsonBody(map[string]any{"files": map[string]string{"index.html": "gpt"}}), bearer); r.status != http.StatusCreated {
		t.Fatalf("deploy with bearer: %d %s", r.status, r.body)
	}
	if r := a.do(t, http.MethodDelete, "/v1/sites/bobs-other", nil, bearer); r.status != http.StatusNotFound {
		t.Fatalf("bearer deleted another person's site: %d", r.status)
	}
	// A verifier for a code issued without a challenge is a downgrade attempt.
	code = codeFrom(t, a.consent(t, q, ann.key, "allow"))
	v, _ := newVerifier()
	if r := a.form(t, "/oauth/token", url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirect}, "client_id": {id}, "client_secret": {secret}, "code_verifier": {v}}, nil); r.status != http.StatusBadRequest {
		t.Fatalf("PKCE downgrade accepted: %d", r.status)
	}
	// A self-registered client can never skip PKCE.
	pub := a.registerClient(t, testRedirect)
	q2 := url.Values{"response_type": {"code"}, "client_id": {pub}, "redirect_uri": {testRedirect}, "state": {"s"}}
	if r := a.do(t, http.MethodGet, "/oauth/authorize?"+q2.Encode(), nil, nil); r.status != http.StatusFound || !strings.Contains(r.header.Get("Location"), "error=invalid_request") {
		t.Fatalf("public client without PKCE: %d %s", r.status, r.header.Get("Location"))
	}
}

// A sign-in link token is redeemable only with the nonce of the browser that
// asked for it (login-CSRF fix); the typed code needs no nonce.
func TestSignInLinkNeedsTheRequestingBrowsersNonce(t *testing.T) {
	a := newConnectorApp(t)
	ann := a.newPerson(t, "ann")
	nonce := "the-requesting-browsers-nonce"
	sum := sha256.Sum256([]byte(nonce))
	hash := base64.RawURLEncoding.EncodeToString(sum[:])
	issue := func(h sql.NullString, code string) string {
		lt, _ := auth.GenerateAPIKey()
		if err := db.CreateAuthToken(context.Background(), a.database, ann.email, code, lt, time.Now().Add(time.Minute), "dashboard", sql.NullString{}, h); err != nil {
			t.Fatal(err)
		}
		return lt
	}
	verify := func(body map[string]string) resp {
		return a.do(t, http.MethodPost, "/v1/auth/verify", jsonBody(body), map[string]string{"Content-Type": "application/json"})
	}
	bound := issue(sql.NullString{String: hash, Valid: true}, "111111")
	if r := verify(map[string]string{"token": bound}); r.status != http.StatusUnauthorized {
		t.Fatalf("link token redeemed without a nonce: %d", r.status)
	}
	if r := verify(map[string]string{"token": bound, "nonce": "someone-elses-nonce"}); r.status != http.StatusUnauthorized {
		t.Fatalf("link token redeemed with the wrong nonce: %d", r.status)
	}
	if r := verify(map[string]string{"token": bound, "nonce": nonce}); r.status != http.StatusOK || r.json(t)["api_key"] != ann.key {
		t.Fatalf("link token refused with its nonce: %d %s", r.status, r.body)
	}
	unbound := issue(sql.NullString{}, "222222")
	if r := verify(map[string]string{"token": unbound, "nonce": nonce}); r.status != http.StatusUnauthorized {
		t.Fatalf("unbound link token redeemed: %d", r.status)
	}
	if r := verify(map[string]string{"email": ann.email, "code": "222222"}); r.status != http.StatusOK {
		t.Fatalf("typed code refused: %d %s", r.status, r.body)
	}
	if r := a.do(t, http.MethodPost, "/v1/auth", jsonBody(map[string]string{"email": ann.email, "nonce_hash": "not-a-hash"}), map[string]string{"Content-Type": "application/json"}); r.status != http.StatusBadRequest {
		t.Fatalf("malformed nonce_hash accepted: %d", r.status)
	}
}
