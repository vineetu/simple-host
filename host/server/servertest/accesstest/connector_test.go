package accesstest

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	_ "github.com/lib/pq"

	accountapp "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/access/accesshttp"
	"github.com/vsriram/simple-host/host/access/auth"
	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	model "github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/server/servertest"
)

func mcpToolNames(t *testing.T, r servertest.Resp) []string {
	t.Helper()
	if r.Status != http.StatusOK {
		t.Fatalf("mcp: %d %s", r.Status, r.Body)
	}
	raw, _ := r.JSON(t)["result"].(map[string]any)["tools"].([]any)
	names := make([]string, 0, len(raw))
	for _, item := range raw {
		names = append(names, item.(map[string]any)["name"].(string))
	}
	return names
}

func hasTool(names []string, want string) bool {
	for _, name := range names {
		if name == want {
			return true
		}
	}
	return false
}

func TestConnectorMetadataAndChallenge(t *testing.T) {
	a := servertest.NewConnectorApp(t)
	r := a.Do(t, http.MethodPost, "/mcp", servertest.JSONBody(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize"}), nil)
	want := `Bearer resource_metadata="` + a.Srv.URL + `/.well-known/oauth-protected-resource/mcp"`
	if r.Status != http.StatusUnauthorized || !strings.HasPrefix(r.Header.Get("WWW-Authenticate"), want) {
		t.Fatalf("unauthenticated /mcp: %d %q", r.Status, r.Header.Get("WWW-Authenticate"))
	}
	prm := a.Do(t, http.MethodGet, "/.well-known/oauth-protected-resource/mcp", nil, nil).JSON(t)
	if prm["resource"] != a.Srv.URL+"/mcp" || prm["authorization_servers"].([]any)[0] != a.Srv.URL {
		t.Fatalf("protected resource metadata: %v", prm)
	}
	asm := a.Do(t, http.MethodGet, "/.well-known/oauth-authorization-server", nil, nil).JSON(t)
	if asm["issuer"] != a.Srv.URL || asm["token_endpoint"] != a.Srv.URL+"/oauth/token" ||
		asm["registration_endpoint"] != a.Srv.URL+"/oauth/register" {
		t.Fatalf("authorization server metadata: %v", asm)
	}
	if m := asm["code_challenge_methods_supported"].([]any); len(m) != 1 || m[0] != "S256" {
		t.Fatalf("PKCE methods: %v", m)
	}
	pre := a.Do(t, http.MethodOptions, "/mcp", nil, map[string]string{"Origin": "https://inspector.example", "Access-Control-Request-Headers": "authorization, mcp-protocol-version"})
	if pre.Header.Get("Access-Control-Allow-Origin") != "*" || !strings.Contains(pre.Header.Get("Access-Control-Allow-Headers"), "Authorization") {
		t.Fatalf("CORS preflight on /mcp: %v", pre.Header)
	}
	bad := a.Do(t, http.MethodPost, "/mcp", servertest.JSONBody(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "ping"}), map[string]string{"Authorization": "Bearer shat_nope"})
	if bad.Status != http.StatusUnauthorized || !strings.Contains(bad.Header.Get("WWW-Authenticate"), `error="invalid_token"`) {
		t.Fatalf("bad token: %d %q", bad.Status, bad.Header.Get("WWW-Authenticate"))
	}
}

func TestConnectorRegistration(t *testing.T) {
	a := servertest.NewConnectorApp(t)
	h := map[string]string{"Content-Type": "application/json"}
	for _, body := range []map[string]any{
		{"redirect_uris": []string{"http://evil.example/cb"}},
		{"redirect_uris": []string{}},
		{"redirect_uris": []string{"https://ok.example/cb"}, "grant_types": []string{"password"}},
		{"redirect_uris": []string{"https://ok.example/cb"}, "token_endpoint_auth_method": "private_key_jwt"},
	} {
		if r := a.Do(t, http.MethodPost, "/oauth/register", servertest.JSONBody(body), h); r.Status != http.StatusBadRequest {
			t.Errorf("accepted %v: %d", body, r.Status)
		}
	}
	conf := a.Do(t, http.MethodPost, "/oauth/register", servertest.JSONBody(map[string]any{"redirect_uris": []string{"https://ok.example/cb"}, "client_name": "Conf"}), h).JSON(t)
	if conf["token_endpoint_auth_method"] != "client_secret_basic" || !strings.HasPrefix(conf["client_secret"].(string), accesshttp.PrefixSecret) {
		t.Fatalf("default registration should be confidential: %v", conf)
	}
	var stored string
	_ = a.Database.QueryRow(`SELECT client_secret_hash FROM oauth_clients WHERE client_id = $1`, conf["client_id"]).Scan(&stored)
	if stored == conf["client_secret"] || stored != accesshttp.HashSecret(conf["client_secret"].(string)) {
		t.Fatal("client secret not stored as a hash")
	}
}

// TestConnectorCursorFlow replays what Cursor sends (2026-10-15: a real
// Cursor user got 400 from /oauth/register for its cursor:// redirect).
func TestConnectorCursorFlow(t *testing.T) {
	a := servertest.NewConnectorApp(t)
	ann := a.NewPerson(t, "ann")
	const redirect = "cursor://anysphere.cursor-mcp/oauth/callback"
	reg := a.Do(t, http.MethodPost, "/oauth/register", servertest.JSONBody(map[string]any{
		"client_name": "Cursor", "redirect_uris": []string{redirect}, "token_endpoint_auth_method": "none",
		"grant_types": []string{"authorization_code", "refresh_token"}, "response_types": []string{"code"},
	}), map[string]string{"Content-Type": "application/json"})
	if reg.Status != http.StatusCreated {
		t.Fatalf("register: %d %s", reg.Status, reg.Body)
	}
	clientID := reg.JSON(t)["client_id"].(string)

	// PKCE stays mandatory for an app-scheme client.
	noPKCE := servertest.AuthorizeQuery(clientID, redirect, "")
	noPKCE.Del("code_challenge")
	noPKCE.Del("code_challenge_method")
	if r := a.Do(t, http.MethodGet, "/oauth/authorize?"+noPKCE.Encode(), nil, nil); r.Status != http.StatusFound || !strings.Contains(r.Header.Get("Location"), "error=invalid_request") {
		t.Fatalf("authorize without PKCE: %d %s", r.Status, r.Header.Get("Location"))
	}

	_, challenge := servertest.NewVerifier()
	page := a.Do(t, http.MethodGet, "/oauth/authorize?"+servertest.AuthorizeQuery(clientID, redirect, challenge).Encode(), nil, nil)
	m := servertest.ConnectDataRe.FindSubmatch(page.Body)
	if page.Status != http.StatusOK || m == nil {
		t.Fatalf("authorize page: %d", page.Status)
	}
	var data map[string]any
	_ = json.Unmarshal(m[1], &data)
	if data["client_name"] != "Cursor" || data["redirect_host"] != "cursor://anysphere.cursor-mcp" {
		t.Fatalf("consent data: %v", data)
	}

	tok := a.Connect(t, ann, clientID, redirect)
	if !strings.HasPrefix(tok["access_token"].(string), accesshttp.PrefixAccess) {
		t.Fatalf("tokens: %v", tok)
	}
	if init := a.Rpc(t, tok["access_token"].(string), "tools/list", map[string]any{}); !strings.Contains(string(init.Body), `"create_site"`) {
		t.Fatalf("tools/list: %s", init.Body)
	}
}

func TestConnectorHappyPathPublishesThroughMCP(t *testing.T) {
	a := servertest.NewConnectorApp(t)
	ann := a.NewPerson(t, "ann")
	clientID := a.RegisterClient(t, servertest.TestRedirect)

	verifier, challenge := servertest.NewVerifier()
	q := servertest.AuthorizeQuery(clientID, servertest.TestRedirect, challenge)
	q.Set("resource", a.Srv.URL+"/mcp")
	page := a.Do(t, http.MethodGet, "/oauth/authorize?"+q.Encode(), nil, nil)
	if !strings.Contains(page.Header.Get("Content-Security-Policy"), "frame-ancestors 'none'") || page.Header.Get("X-Frame-Options") != "DENY" {
		t.Fatal("consent page can be framed")
	}
	if !strings.Contains(string(page.Body), `class="sh-header"`) || !strings.Contains(string(page.Body), "Test Chat") {
		t.Fatal("consent page lacks the site chrome or the app name")
	}
	redirectTo := a.Consent(t, q, ann.Key, "allow")
	u, _ := url.Parse(redirectTo)
	if !strings.HasPrefix(redirectTo, servertest.TestRedirect+"?") || u.Query().Get("state") != "st4te" || u.Query().Get("iss") != a.Srv.URL {
		t.Fatalf("redirect %s", redirectTo)
	}
	tok := a.Form(t, "/oauth/token", url.Values{
		"grant_type": {"authorization_code"}, "code": {u.Query().Get("code")}, "redirect_uri": {servertest.TestRedirect},
		"client_id": {clientID}, "code_verifier": {verifier}, "resource": {a.Srv.URL + "/mcp"},
	}, nil)
	if tok.Status != http.StatusOK || tok.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("token: %d %s", tok.Status, tok.Body)
	}
	tokens := tok.JSON(t)
	access := tokens["access_token"].(string)
	if !strings.HasPrefix(access, accesshttp.PrefixAccess) || tokens["expires_in"].(float64) != 3600 || tokens["token_type"] != "Bearer" {
		t.Fatalf("tokens: %v", tokens)
	}
	var n int
	_ = a.Database.QueryRow(`SELECT count(*) FROM oauth_tokens WHERE token_hash = $1`, access).Scan(&n)
	if n != 0 {
		t.Fatal("access token stored in the clear")
	}

	init := a.Rpc(t, access, "initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "t", "version": "1"}})
	if init.Status != 200 || init.JSON(t)["result"].(map[string]any)["protocolVersion"] != "2025-06-18" {
		t.Fatalf("initialize: %s", init.Body)
	}
	list := a.Rpc(t, access, "tools/list", map[string]any{})
	if !strings.Contains(string(list.Body), `"create_site"`) {
		t.Fatalf("tools/list: %s", list.Body)
	}
	text, s, isErr := servertest.ToolResultOf(t, a.Rpc(t, access, "tools/call", map[string]any{"name": "create_site", "arguments": map[string]any{
		"site": "hello-connector", "files": map[string]any{"index.html": "<h1>hi</h1>"},
	}}))
	if isErr || s["active_version"].(float64) != 1 || !strings.Contains(s["url"].(string), "/hello-connector/") {
		t.Fatalf("create_site: %s", text)
	}
	// The email is left out unless the person asks for it (2026-10-28).
	text, s, isErr = servertest.ToolResultOf(t, a.Rpc(t, access, "tools/call", map[string]any{"name": "who_am_i", "arguments": map[string]any{}}))
	if isErr || s["email"] != "" || strings.Contains(text, ann.Email) {
		t.Fatalf("who_am_i: %s", text)
	}
	text, s, isErr = servertest.ToolResultOf(t, a.Rpc(t, access, "tools/call", map[string]any{"name": "who_am_i", "arguments": map[string]any{"include_email": true}}))
	if isErr || s["email"] != ann.Email {
		t.Fatalf("who_am_i include_email: %s", text)
	}
	text, _, isErr = servertest.ToolResultOf(t, a.Rpc(t, access, "tools/call", map[string]any{"name": "read_site_file", "arguments": map[string]any{"site": "hello-connector", "path": "index.html"}}))
	if isErr || text != "<h1>hi</h1>" {
		t.Fatalf("read_site_file: %q", text)
	}

	conns := a.Do(t, http.MethodGet, "/v1/me/connections", nil, map[string]string{"X-API-Key": ann.Key}).JSON(t)["connections"].([]any)
	if len(conns) != 1 || conns[0].(map[string]any)["name"] != "Test Chat" {
		t.Fatalf("connections: %v", conns)
	}
	// The consent request's user agent, summarised, tells two connections of
	// the same app apart (this test client sends Go's own).
	if d := conns[0].(map[string]any)["device"]; d != "Go-http-client" {
		t.Fatalf("connection device: %v", d)
	}
}

func TestConnectorAuthorizeRefusals(t *testing.T) {
	a := servertest.NewConnectorApp(t)
	ann := a.NewPerson(t, "ann")
	clientID := a.RegisterClient(t, servertest.TestRedirect)
	_, challenge := servertest.NewVerifier()

	// A redirect URI the client did not register is shown to the person and
	// never followed: following it would be an open redirect.
	q := servertest.AuthorizeQuery(clientID, "https://attacker.example/cb", challenge)
	if r := a.Do(t, http.MethodGet, "/oauth/authorize?"+q.Encode(), nil, nil); r.Status != http.StatusBadRequest || r.Header.Get("Location") != "" {
		t.Fatalf("unregistered redirect: %d %q", r.Status, r.Header.Get("Location"))
	}
	q = servertest.AuthorizeQuery("shc_unknown", servertest.TestRedirect, challenge)
	if r := a.Do(t, http.MethodGet, "/oauth/authorize?"+q.Encode(), nil, nil); r.Status != http.StatusBadRequest || r.Header.Get("Location") != "" {
		t.Fatalf("unknown client: %d", r.Status)
	}
	for name, mutate := range map[string]func(url.Values){
		"plain PKCE":       func(v url.Values) { v.Set("code_challenge_method", "plain") },
		"missing PKCE":     func(v url.Values) { v.Del("code_challenge"); v.Del("code_challenge_method") },
		"token response":   func(v url.Values) { v.Set("response_type", "token") },
		"foreign audience": func(v url.Values) { v.Set("resource", "https://other.example/mcp") },
	} {
		q := servertest.AuthorizeQuery(clientID, servertest.TestRedirect, challenge)
		mutate(q)
		r := a.Do(t, http.MethodGet, "/oauth/authorize?"+q.Encode(), nil, nil)
		loc, _ := url.Parse(r.Header.Get("Location"))
		if r.Status != http.StatusFound || loc == nil || loc.Query().Get("error") == "" || loc.Query().Get("state") != "st4te" {
			t.Errorf("%s: %d %q", name, r.Status, r.Header.Get("Location"))
		}
	}

	q = servertest.AuthorizeQuery(clientID, servertest.TestRedirect, challenge)
	page := a.Do(t, http.MethodGet, "/oauth/authorize?"+q.Encode(), nil, nil)
	var data map[string]any
	_ = json.Unmarshal(servertest.ConnectDataRe.FindSubmatch(page.Body)[1], &data)
	decide := func(key, csrf, origin string) int {
		h := map[string]string{"Content-Type": "application/json", "X-API-Key": key}
		if origin != "" {
			h["Origin"] = origin
		}
		return a.Do(t, http.MethodPost, "/oauth/authorize/decision", servertest.JSONBody(map[string]string{"query": q.Encode(), "csrf": csrf, "decision": "allow"}), h).Status
	}
	if s := decide(ann.Key, "1.forged", a.Srv.URL); s != http.StatusForbidden {
		t.Errorf("forged CSRF token: %d", s)
	}
	if s := decide(ann.Key, data["csrf"].(string), "https://evil.example"); s != http.StatusForbidden {
		t.Errorf("cross-origin decision: %d", s)
	}
	if s := decide("", data["csrf"].(string), a.Srv.URL); s != http.StatusUnauthorized {
		t.Errorf("decision without sign-in: %d", s)
	}
	if s := decide(a.Admin, data["csrf"].(string), a.Srv.URL); s != http.StatusForbidden {
		t.Errorf("admin key connected an app: %d", s)
	}
	deny := a.Consent(t, q, ann.Key, "deny")
	if u, _ := url.Parse(deny); u.Query().Get("error") != "access_denied" || u.Query().Get("code") != "" {
		t.Errorf("cancel: %s", deny)
	}
	// The decision endpoint never grants CORS to another origin.
	pre := a.Do(t, http.MethodOptions, "/oauth/authorize/decision", nil, map[string]string{"Origin": "https://evil.example", "Access-Control-Request-Method": "POST"})
	if pre.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Error("decision endpoint is CORS-open")
	}
}

func TestConnectorCodeRules(t *testing.T) {
	a := servertest.NewConnectorApp(t)
	ann := a.NewPerson(t, "ann")
	clientID := a.RegisterClient(t, servertest.TestRedirect)
	redeem := func(code, verifier, redirect string) servertest.Resp {
		return a.Form(t, "/oauth/token", url.Values{
			"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirect},
			"client_id": {clientID}, "code_verifier": {verifier},
		}, nil)
	}
	fresh := func() (string, string) {
		v, c := servertest.NewVerifier()
		return v, servertest.CodeFrom(t, a.Consent(t, servertest.AuthorizeQuery(clientID, servertest.TestRedirect, c), ann.Key, "allow"))
	}
	isInvalidGrant := func(r servertest.Resp) bool {
		return r.Status == http.StatusBadRequest && r.JSON(t)["error"] == "invalid_grant"
	}

	t.Run("wrong verifier, and the code is spent", func(t *testing.T) {
		v, code := fresh()
		other, _ := servertest.NewVerifier()
		if r := redeem(code, other, servertest.TestRedirect); !isInvalidGrant(r) {
			t.Fatalf("wrong verifier: %d %s", r.Status, r.Body)
		}
		if r := redeem(code, v, servertest.TestRedirect); !isInvalidGrant(r) {
			t.Fatalf("code usable after a failed attempt: %d", r.Status)
		}
	})
	t.Run("missing verifier", func(t *testing.T) {
		_, code := fresh()
		if r := redeem(code, "", servertest.TestRedirect); !isInvalidGrant(r) {
			t.Fatalf("%d", r.Status)
		}
	})
	t.Run("wrong redirect_uri", func(t *testing.T) {
		v, code := fresh()
		if r := redeem(code, v, "https://chat.example.com/other"); !isInvalidGrant(r) {
			t.Fatalf("%d", r.Status)
		}
	})
	t.Run("expired", func(t *testing.T) {
		v, code := fresh()
		_, _ = a.Database.Exec(`UPDATE oauth_codes SET expires_at = now() - interval '1 second' WHERE code_hash = $1`, accesshttp.HashSecret(code))
		if r := redeem(code, v, servertest.TestRedirect); !isInvalidGrant(r) {
			t.Fatalf("%d", r.Status)
		}
	})
	t.Run("another client", func(t *testing.T) {
		v, code := fresh()
		other := a.RegisterClient(t, servertest.TestRedirect)
		r := a.Form(t, "/oauth/token", url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {servertest.TestRedirect}, "client_id": {other}, "code_verifier": {v}}, nil)
		if !isInvalidGrant(r) {
			t.Fatalf("%d", r.Status)
		}
	})
	t.Run("reuse revokes what the first redemption issued", func(t *testing.T) {
		v, code := fresh()
		first := redeem(code, v, servertest.TestRedirect)
		if first.Status != http.StatusOK {
			t.Fatalf("first: %d %s", first.Status, first.Body)
		}
		access := first.JSON(t)["access_token"].(string)
		if r := a.Rpc(t, access, "ping", map[string]any{}); r.Status != 200 {
			t.Fatalf("fresh token: %d", r.Status)
		}
		if r := redeem(code, v, servertest.TestRedirect); !isInvalidGrant(r) {
			t.Fatalf("reuse: %d", r.Status)
		}
		if r := a.Rpc(t, access, "ping", map[string]any{}); r.Status != http.StatusUnauthorized {
			t.Fatalf("token survived code reuse: %d", r.Status)
		}
	})
}

func TestConnectorRefreshRotationAndReuse(t *testing.T) {
	a := servertest.NewConnectorApp(t)
	ann := a.NewPerson(t, "ann")
	clientID := a.RegisterClient(t, servertest.TestRedirect)
	tokens := a.Connect(t, ann, clientID, servertest.TestRedirect)
	refresh := func(rt string) servertest.Resp {
		return a.Form(t, "/oauth/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {rt}, "client_id": {clientID}}, nil)
	}
	r1 := refresh(tokens["refresh_token"].(string))
	if r1.Status != 200 {
		t.Fatalf("refresh: %d %s", r1.Status, r1.Body)
	}
	t2 := r1.JSON(t)
	if t2["refresh_token"] == tokens["refresh_token"] {
		t.Fatal("refresh token did not rotate")
	}
	if r := a.Rpc(t, t2["access_token"].(string), "ping", map[string]any{}); r.Status != 200 {
		t.Fatalf("new access token: %d", r.Status)
	}
	// The old refresh token again: reuse. The whole family is revoked.
	if r := refresh(tokens["refresh_token"].(string)); r.Status != http.StatusBadRequest {
		t.Fatalf("reuse accepted: %d", r.Status)
	}
	if r := a.Rpc(t, t2["access_token"].(string), "ping", map[string]any{}); r.Status != http.StatusUnauthorized {
		t.Fatal("access token from the family survived reuse")
	}
	if r := refresh(t2["refresh_token"].(string)); r.Status != http.StatusBadRequest {
		t.Fatal("refresh token from the family survived reuse")
	}
	// A refresh token presented by another client is refused.
	t3 := a.Connect(t, ann, clientID, servertest.TestRedirect)
	other := a.RegisterClient(t, servertest.TestRedirect)
	if r := a.Form(t, "/oauth/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {t3["refresh_token"].(string)}, "client_id": {other}}, nil); r.Status != http.StatusBadRequest {
		t.Fatalf("cross-client refresh: %d", r.Status)
	}
}

func TestConnectorRevocation(t *testing.T) {
	a := servertest.NewConnectorApp(t)
	ann := a.NewPerson(t, "ann")
	clientID := a.RegisterClient(t, servertest.TestRedirect)

	tokens := a.Connect(t, ann, clientID, servertest.TestRedirect)
	access := tokens["access_token"].(string)
	if r := a.Form(t, "/oauth/revoke", url.Values{"token": {access}, "client_id": {clientID}}, nil); r.Status != 200 {
		t.Fatalf("revoke: %d", r.Status)
	}
	if r := a.Rpc(t, access, "ping", map[string]any{}); r.Status != http.StatusUnauthorized {
		t.Fatal("revoked access token accepted")
	}
	// Revoking a refresh token ends the connection.
	if r := a.Form(t, "/oauth/revoke", url.Values{"token": {tokens["refresh_token"].(string)}, "client_id": {clientID}}, nil); r.Status != 200 {
		t.Fatal("revoke refresh")
	}
	if r := a.Form(t, "/oauth/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {tokens["refresh_token"].(string)}, "client_id": {clientID}}, nil); r.Status != http.StatusBadRequest {
		t.Fatal("revoked refresh token accepted")
	}
	// Disconnecting from the account.
	t2 := a.Connect(t, ann, clientID, servertest.TestRedirect)
	if r := a.Do(t, http.MethodDelete, "/v1/me/connections/"+clientID, nil, map[string]string{"X-API-Key": ann.Key}); r.Status != http.StatusNoContent {
		t.Fatalf("disconnect: %d", r.Status)
	}
	if r := a.Rpc(t, t2["access_token"].(string), "ping", map[string]any{}); r.Status != http.StatusUnauthorized {
		t.Fatal("token survived disconnect")
	}
	// Rotating the API key disconnects every app.
	t3 := a.Connect(t, ann, clientID, servertest.TestRedirect)
	if r := a.Do(t, http.MethodPost, "/v1/me/api-key/rotate", nil, map[string]string{"X-API-Key": ann.Key}); r.Status != 200 {
		t.Fatalf("rotate: %d", r.Status)
	}
	if r := a.Rpc(t, t3["access_token"].(string), "ping", map[string]any{}); r.Status != http.StatusUnauthorized {
		t.Fatal("token survived key rotation")
	}
	// Deleting the person deletes their connections.
	bob := a.NewPerson(t, "bob")
	t4 := a.Connect(t, bob, clientID, servertest.TestRedirect)
	_, _ = a.Database.Exec(`DELETE FROM users WHERE username = $1`, bob.Email)
	if r := a.Rpc(t, t4["access_token"].(string), "ping", map[string]any{}); r.Status != http.StatusUnauthorized {
		t.Fatal("token survived account deletion")
	}
}

func TestConnectorToolsHaveRESTPermissions(t *testing.T) {
	a := servertest.NewConnectorApp(t)
	ann, bob := a.NewPerson(t, "ann"), a.NewPerson(t, "bob")
	clientID := a.RegisterClient(t, servertest.TestRedirect)
	if r := a.Do(t, http.MethodPost, "/v1/sites/bobs-page/files", servertest.JSONBody(map[string]any{"files": map[string]string{"index.html": "bob"}}),
		map[string]string{"X-API-Key": bob.Key, "Content-Type": "application/json"}); r.Status != http.StatusCreated {
		t.Fatalf("bob deploy: %d %s", r.Status, r.Body)
	}
	access := a.Connect(t, ann, clientID, servertest.TestRedirect)["access_token"].(string)
	for _, call := range []map[string]any{
		{"name": "delete_site", "arguments": map[string]any{"site": "bobs-page", "confirm_name": "bobs-page"}},
		{"name": "update_site", "arguments": map[string]any{"site": "bobs-page", "files": map[string]any{"index.html": "ann was here"}}},
		{"name": "rollback_site", "arguments": map[string]any{"site": "bobs-page", "version": 1}},
		{"name": "read_site_file", "arguments": map[string]any{"site": "bobs-page", "path": "index.html", "version": 1}},
		{"name": "set_visibility", "arguments": map[string]any{"site": "bobs-page", "visibility": "public"}},
	} {
		text, _, isErr := servertest.ToolResultOf(t, a.Rpc(t, access, "tools/call", call))
		if !isErr || !(strings.Contains(text, "404") || strings.Contains(text, "no site named")) {
			t.Errorf("%s on another person's site: %q", call["name"], text)
		}
	}
	var n int
	_ = a.Database.QueryRow(`SELECT active_version FROM sites WHERE name = 'bobs-page'`).Scan(&n)
	if n != 1 {
		t.Fatal("bob's site changed")
	}
	// An /mcp token is not a REST credential; a whole-server token is, and it
	// has exactly the person's key's power.
	if r := a.Do(t, http.MethodGet, "/v1/sites", nil, map[string]string{"Authorization": "Bearer " + access}); r.Status != http.StatusUnauthorized {
		t.Fatalf("/mcp token accepted on /v1: %d", r.Status)
	}
}

func TestConnectorConfidentialClientForGPTActions(t *testing.T) {
	a := servertest.NewConnectorApp(t)
	ann, bob := a.NewPerson(t, "ann"), a.NewPerson(t, "bob")
	if r := a.Do(t, http.MethodPost, "/v1/sites/bobs-other/files", servertest.JSONBody(map[string]any{"files": map[string]string{"index.html": "bob"}}),
		map[string]string{"X-API-Key": bob.Key, "Content-Type": "application/json"}); r.Status != http.StatusCreated {
		t.Fatalf("bob deploy: %d", r.Status)
	}
	redirect := "https://chatgpt.com/aip/g-test/oauth/callback"
	id, secret, err := accesshttp.CreateOperatorClient(context.Background(), a.Database, "Simple Host GPT", []string{redirect}, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = accountsrepo.DeleteOAuthClient(context.Background(), a.Database, id) })
	q := url.Values{"response_type": {"code"}, "client_id": {id}, "redirect_uri": {redirect}, "scope": {"sites"}, "state": {"gpt"}}
	code := servertest.CodeFrom(t, a.Consent(t, q, ann.Key, "allow"))
	if r := a.Form(t, "/oauth/token", url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirect}, "client_id": {id}, "client_secret": {"wrong"}}, nil); r.Status != http.StatusUnauthorized {
		t.Fatalf("wrong secret: %d", r.Status)
	}
	code = servertest.CodeFrom(t, a.Consent(t, q, ann.Key, "allow"))
	tok := a.Form(t, "/oauth/token", url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirect}, "client_id": {id}, "client_secret": {secret}}, nil)
	if tok.Status != 200 {
		t.Fatalf("GPT token: %d %s", tok.Status, tok.Body)
	}
	access := tok.JSON(t)["access_token"].(string)
	bearer := map[string]string{"Authorization": "Bearer " + access, "Content-Type": "application/json"}
	if r := a.Do(t, http.MethodPost, "/v1/sites/gpt-made/files", servertest.JSONBody(map[string]any{"files": map[string]string{"index.html": "gpt"}}), bearer); r.Status != http.StatusCreated {
		t.Fatalf("deploy with bearer: %d %s", r.Status, r.Body)
	}
	if r := a.Do(t, http.MethodDelete, "/v1/sites/bobs-other", nil, bearer); r.Status != http.StatusNotFound {
		t.Fatalf("bearer deleted another person's site: %d", r.Status)
	}
	// A verifier for a code issued without a challenge is a downgrade attempt.
	code = servertest.CodeFrom(t, a.Consent(t, q, ann.Key, "allow"))
	v, _ := servertest.NewVerifier()
	if r := a.Form(t, "/oauth/token", url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirect}, "client_id": {id}, "client_secret": {secret}, "code_verifier": {v}}, nil); r.Status != http.StatusBadRequest {
		t.Fatalf("PKCE downgrade accepted: %d", r.Status)
	}
	// A self-registered client can never skip PKCE.
	pub := a.RegisterClient(t, servertest.TestRedirect)
	q2 := url.Values{"response_type": {"code"}, "client_id": {pub}, "redirect_uri": {servertest.TestRedirect}, "state": {"s"}}
	if r := a.Do(t, http.MethodGet, "/oauth/authorize?"+q2.Encode(), nil, nil); r.Status != http.StatusFound || !strings.Contains(r.Header.Get("Location"), "error=invalid_request") {
		t.Fatalf("public client without PKCE: %d %s", r.Status, r.Header.Get("Location"))
	}
}

// A sign-in link token is redeemable only with the nonce of the browser that
// asked for it (login-CSRF fix); the typed code needs no nonce.
func TestSignInLinkNeedsTheRequestingBrowsersNonce(t *testing.T) {
	a := servertest.NewConnectorApp(t)
	ann := a.NewPerson(t, "ann")
	nonce := "the-requesting-browsers-nonce"
	sum := sha256.Sum256([]byte(nonce))
	hash := base64.RawURLEncoding.EncodeToString(sum[:])
	issue := func(h sql.NullString, code string) string {
		lt, _ := auth.GenerateAPIKey()
		if err := accountsrepo.CreateAuthToken(context.Background(), a.Database, ann.Email, code, lt, time.Now().Add(time.Minute), "dashboard", sql.NullString{}, h); err != nil {
			t.Fatal(err)
		}
		return lt
	}
	verify := func(body map[string]string) servertest.Resp {
		return a.Do(t, http.MethodPost, "/v1/auth/verify", servertest.JSONBody(body), map[string]string{"Content-Type": "application/json"})
	}
	bound := issue(sql.NullString{String: hash, Valid: true}, "111111")
	if r := verify(map[string]string{"token": bound}); r.Status != http.StatusUnauthorized {
		t.Fatalf("link token redeemed without a nonce: %d", r.Status)
	}
	if r := verify(map[string]string{"token": bound, "nonce": "someone-elses-nonce"}); r.Status != http.StatusUnauthorized {
		t.Fatalf("link token redeemed with the wrong nonce: %d", r.Status)
	}
	if r := verify(map[string]string{"token": bound, "nonce": nonce}); r.Status != http.StatusOK {
		t.Fatalf("link token refused with its nonce: %d %s", r.Status, r.Body)
	} else if k, _ := r.JSON(t)["api_key"].(string); a.Do(t, http.MethodGet, "/v1/me", nil, map[string]string{"X-API-Key": k}).JSON(t)["username"] != ann.Email {
		t.Fatalf("key from link sign-in does not work")
	}
	unbound := issue(sql.NullString{}, "222222")
	if r := verify(map[string]string{"token": unbound, "nonce": nonce}); r.Status != http.StatusUnauthorized {
		t.Fatalf("unbound link token redeemed: %d", r.Status)
	}
	if r := verify(map[string]string{"email": ann.Email, "code": "222222"}); r.Status != http.StatusOK {
		t.Fatalf("typed code refused: %d %s", r.Status, r.Body)
	}
	if r := a.Do(t, http.MethodPost, "/v1/auth", servertest.JSONBody(map[string]string{"email": ann.Email, "nonce_hash": "not-a-hash"}), map[string]string{"Content-Type": "application/json"}); r.Status != http.StatusBadRequest {
		t.Fatalf("malformed nonce_hash accepted: %d", r.Status)
	}
}

// Account keys are stored only as SHA-256; rotating kills every key the
// account holds; internal credentials die with their request.
func TestAPIKeysStoredHashed(t *testing.T) {
	a := servertest.NewConnectorApp(t)
	p := a.NewPerson(t, "hashed")
	count := func(v string) int {
		var n int
		if err := a.Database.QueryRow(`SELECT count(*) FROM api_keys WHERE key_hash = $1`, v).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if count(p.Key) != 0 || count(accountsrepo.HashAPIKey(p.Key)) != 1 {
		t.Fatal("api key is not stored as its hash")
	}
	me := func(k string) int {
		return a.Do(t, http.MethodGet, "/v1/me", nil, map[string]string{"X-API-Key": k}).Status
	}
	u, err := accountapp.New(a.Database).GetUserByAPIKey(context.Background(), p.Key, 180*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	second, _ := auth.GenerateAPIKey()
	if err := accountsrepo.AddAPIKey(context.Background(), a.Database, u.ID, second, model.KeyNameAgent); err != nil {
		t.Fatal(err)
	}
	if me(p.Key) != http.StatusOK || me(second) != http.StatusOK {
		t.Fatal("both keys should work before rotation")
	}
	r := a.Do(t, http.MethodPost, "/v1/me/api-key/rotate", nil, map[string]string{"X-API-Key": p.Key})
	if r.Status != http.StatusOK {
		t.Fatalf("rotate: %d %s", r.Status, r.Body)
	}
	fresh, _ := r.JSON(t)["api_key"].(string)
	if me(p.Key) != http.StatusUnauthorized || me(second) != http.StatusUnauthorized || me(fresh) != http.StatusOK {
		t.Fatal("rotation must replace every key with the new one")
	}
	// A rotation made with a key that is no longer valid fails.
	if r := a.Do(t, http.MethodPost, "/v1/me/api-key/rotate", nil, map[string]string{"X-API-Key": p.Key}); r.Status != http.StatusUnauthorized {
		t.Fatalf("rotate with dead key: %d", r.Status)
	}

	ik, revoke, err := accountsrepo.IssueInternalKey(u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if me(ik) != http.StatusOK {
		t.Fatal("internal credential should resolve while live")
	}
	if r := a.Do(t, http.MethodPost, "/v1/me/api-key/rotate", nil, map[string]string{"X-API-Key": ik}); r.Status != http.StatusBadRequest {
		t.Fatalf("internal credential must not rotate the key: %d", r.Status)
	}
	revoke()
	if me(ik) != http.StatusUnauthorized {
		t.Fatal("internal credential outlived its revoke")
	}
}

// The consent page reads the API key from localStorage, so its scripts run
// only by nonce.
func TestConsentPageScriptNonce(t *testing.T) {
	a := servertest.NewConnectorApp(t)
	r := a.Do(t, http.MethodGet, "/oauth/authorize", nil, nil)
	csp := r.Header.Get("Content-Security-Policy")
	m := regexp.MustCompile(`script-src 'self' 'nonce-([A-Za-z0-9+/=]+)';`).FindStringSubmatch(csp)
	if m == nil || strings.Contains(csp, "script-src 'self' 'unsafe-inline'") {
		t.Fatalf("consent CSP: %q", csp)
	}
	body := string(r.Body)
	if !strings.Contains(body, `<script nonce="`+m[1]+`">`) || strings.Contains(body, "<script>") {
		t.Fatalf("consent page inline scripts are not all stamped with the nonce")
	}
}

// An ordinary Host ignores the hackathon consent choice. mode=events does not
// become an events grant, and the tool list stays the website inventory.
func TestConnectorHostIgnoresEventManagementMode(t *testing.T) {
	a := servertest.NewConnectorApp(t)
	for _, path := range []string{"/.well-known/oauth-protected-resource/mcp", "/.well-known/oauth-authorization-server"} {
		scopes, _ := a.Do(t, http.MethodGet, path, nil, nil).JSON(t)["scopes_supported"].([]any)
		if len(scopes) != 1 || scopes[0] != "sites" {
			t.Fatalf("%s scopes: %v", path, scopes)
		}
	}
	p := a.NewPerson(t, "host")
	clientID := a.RegisterClient(t, servertest.TestRedirect)
	verifier, challenge := servertest.NewVerifier()
	q := servertest.AuthorizeQuery(clientID, servertest.TestRedirect, challenge)
	q.Set("resource", a.Srv.URL+"/mcp")
	_, csrf := a.ConsentPage(t, q)
	decided := a.DecideConsent(t, q, csrf, p.Key, map[string]any{"mode": "events", "team_id": "00000000-0000-4000-8000-000000000000"})
	if decided.Status != http.StatusOK {
		t.Fatalf("decision: %d %s", decided.Status, decided.Body)
	}
	tok := a.ExchangeCode(t, clientID, servertest.TestRedirect, verifier, servertest.CodeFrom(t, decided.JSON(t)["redirect_to"].(string)), a.Srv.URL+"/mcp")
	if tok["scope"] != "sites" {
		t.Fatalf("scope: %v", tok["scope"])
	}
	access := tok["access_token"].(string)
	names := mcpToolNames(t, a.Rpc(t, access, "tools/list", map[string]any{}))
	if !hasTool(names, "create_site") || hasTool(names, "hack_list_events") {
		t.Fatalf("ordinary tools: %v", names)
	}
	if _, _, isErr := servertest.ToolResultOf(t, a.Rpc(t, access, "tools/call", map[string]any{"name": "who_am_i", "arguments": map[string]any{}})); isErr {
		t.Fatal("who_am_i failed on an ordinary connection")
	}
}

// Meta Muse cannot register itself, so its Client ID is the URL of a metadata
// document we host. That client is pre-registered, public and held to PKCE.
func TestConnectorMuseClient(t *testing.T) {
	a := servertest.NewConnectorApp(t)
	ctx := context.Background()
	for i := 0; i < 2; i++ { // idempotent, as at every boot
		if err := a.Conn.EnsureBuiltinClients(ctx); err != nil {
			t.Fatal(err)
		}
	}
	museID := a.Srv.URL + accesshttp.MuseClientPath
	t.Cleanup(func() { _, _ = a.Database.Exec(`DELETE FROM oauth_clients WHERE client_id = $1`, museID) })
	t.Cleanup(func() {
		_, _ = a.Database.Exec(`DELETE FROM oauth_clients WHERE client_id = $1`, accesshttp.MuseShortClientID)
	})

	doc := a.Do(t, http.MethodGet, accesshttp.MuseClientPath, nil, nil)
	if doc.Status != 200 || !strings.HasPrefix(doc.Header.Get("Content-Type"), "application/json") {
		t.Fatalf("metadata document: %d %q", doc.Status, doc.Header.Get("Content-Type"))
	}
	m := doc.JSON(t)
	if m["client_id"] != museID || m["token_endpoint_auth_method"] != "none" || m["client_secret"] != nil {
		t.Fatalf("metadata document: %v", m)
	}
	if r := m["redirect_uris"].([]any); len(r) != 1 || r[0] != accesshttp.MuseRedirectURI {
		t.Fatalf("redirect_uris: %v", r)
	}
	c, err := accountsrepo.GetOAuthClient(ctx, a.Database, museID)
	if err != nil || c.Dynamic || !c.PKCERequired || c.SecretHash.Valid {
		t.Fatalf("stored client: %+v %v", c, err)
	}
	// A phone keyboard cannot mangle a short id. The same client answers to it.
	short, err := accountsrepo.GetOAuthClient(ctx, a.Database, accesshttp.MuseShortClientID)
	if err != nil || short.Dynamic || !short.PKCERequired || short.SecretHash.Valid || len(short.RedirectURIs) != 1 || short.RedirectURIs[0] != accesshttp.MuseRedirectURI {
		t.Fatalf("short client: %+v %v", short, err)
	}

	ann := a.NewPerson(t, "ann")
	_, challenge := servertest.NewVerifier()
	// The whole flow works under the short id too.
	if tokens := a.Connect(t, ann, accesshttp.MuseShortClientID, accesshttp.MuseRedirectURI); tokens["access_token"] == nil {
		t.Fatalf("connect with short id: %v", tokens)
	}
	// A redirect the document does not list is shown, never followed.
	q := servertest.AuthorizeQuery(museID, "https://agent.meta.ai/api/hatch/oauth/other", challenge)
	if r := a.Do(t, http.MethodGet, "/oauth/authorize?"+q.Encode(), nil, nil); r.Status != http.StatusBadRequest || r.Header.Get("Location") != "" {
		t.Fatalf("unlisted redirect: %d %q", r.Status, r.Header.Get("Location"))
	}
	// PKCE is required.
	q = servertest.AuthorizeQuery(museID, accesshttp.MuseRedirectURI, challenge)
	q.Del("code_challenge")
	q.Del("code_challenge_method")
	r := a.Do(t, http.MethodGet, "/oauth/authorize?"+q.Encode(), nil, nil)
	if loc, _ := url.Parse(r.Header.Get("Location")); r.Status != http.StatusFound || loc.Query().Get("error") != "invalid_request" {
		t.Fatalf("no PKCE: %d %q", r.Status, r.Header.Get("Location"))
	}

	// The whole flow, asking only for "sites" (no offline_access), as Muse does.
	tokens := a.Connect(t, ann, museID, accesshttp.MuseRedirectURI)
	if tokens["refresh_token"] == nil || tokens["refresh_token"] == "" {
		t.Fatalf("no refresh token: %v", tokens)
	}
	if r := a.Rpc(t, tokens["access_token"].(string), "tools/list", map[string]any{}); r.Status != 200 {
		t.Fatalf("tools/list: %d %s", r.Status, r.Body)
	}
	rr := a.Form(t, "/oauth/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {tokens["refresh_token"].(string)}, "client_id": {museID}}, nil)
	if rr.Status != 200 || rr.JSON(t)["access_token"] == nil {
		t.Fatalf("refresh: %d %s", rr.Status, rr.Body)
	}
	// The connection is listed and can be removed by its URL-shaped ID.
	if r := a.Do(t, http.MethodDelete, "/v1/me/connections/"+url.PathEscape(museID), nil, map[string]string{"X-API-Key": ann.Key}); r.Status != http.StatusNoContent {
		t.Fatalf("disconnect: %d %s", r.Status, r.Body)
	}
	if r := a.Rpc(t, rr.JSON(t)["access_token"].(string), "ping", map[string]any{}); r.Status != http.StatusUnauthorized {
		t.Fatal("token survived disconnect")
	}
}

// Someone types their email into Muse's Client ID box: the page says what to
// paste instead. Other unknown clients keep the generic message.
func TestConnectorUnknownClientHelp(t *testing.T) {
	a := servertest.NewConnectorApp(t)
	_, challenge := servertest.NewVerifier()
	page := func(clientID, redirect string) map[string]any {
		r := a.Do(t, http.MethodGet, "/oauth/authorize?"+servertest.AuthorizeQuery(clientID, redirect, challenge).Encode(), nil, nil)
		if r.Status != http.StatusBadRequest || r.Header.Get("Location") != "" {
			t.Fatalf("%s: %d %q", clientID, r.Status, r.Header.Get("Location"))
		}
		var data map[string]any
		_ = json.Unmarshal(servertest.ConnectDataRe.FindSubmatch(r.Body)[1], &data)
		return data
	}
	d := page("someone@example.com", accesshttp.MuseRedirectURI)
	if d["muse_client_id"] != accesshttp.MuseShortClientID || d["mcp_url"] != a.Srv.URL+"/mcp" || d["muse_typo"] != nil {
		t.Fatalf("email in Client ID: %v", d)
	}
	if d := page("my name", servertest.TestRedirect); d["muse_client_id"] == nil {
		t.Fatalf("typed text in Client ID: %v", d)
	}
	// A phone keyboard appending ".com" to the hosted document's address: the
	// redirect is still Muse's own, so the page names the cause.
	if d := page(a.Srv.URL+accesshttp.MuseClientPath+".com", accesshttp.MuseRedirectURI); d["muse_client_id"] != accesshttp.MuseShortClientID || d["muse_typo"] != true {
		t.Fatalf("mangled document URL: %v", d)
	}
	if d := page("Meta-muse ", accesshttp.MuseRedirectURI); d["muse_client_id"] != accesshttp.MuseShortClientID || d["muse_typo"] != true {
		t.Fatalf("capitalised short id: %v", d)
	}
	if d := page("muse", accesshttp.MuseRedirectURI); d["muse_client_id"] != accesshttp.MuseShortClientID || d["muse_typo"] != nil {
		t.Fatalf("near miss: %v", d)
	}
	for _, id := range []string{"shc_expired", "https://other.example/client.json"} {
		if d := page(id, servertest.TestRedirect); d["muse_client_id"] != nil || !strings.Contains(d["error"].(string), "not known") {
			t.Fatalf("%s: %v", id, d)
		}
	}
}
