package connector

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	. "github.com/vsriram/simple-host/host/access/featuretest"

	_ "github.com/lib/pq"

	accountapp "github.com/vsriram/simple-host/host/access"
	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
)

func mcpToolNames(t *testing.T, r Resp) []string {
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
	a := NewApp(t)
	r := a.Do(t, http.MethodPost, "/mcp", rt.JSONBody(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize"}), nil)
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
	bad := a.Do(t, http.MethodPost, "/mcp", rt.JSONBody(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "ping"}), map[string]string{"Authorization": "Bearer shat_nope"})
	if bad.Status != http.StatusUnauthorized || !strings.Contains(bad.Header.Get("WWW-Authenticate"), `error="invalid_token"`) {
		t.Fatalf("bad token: %d %q", bad.Status, bad.Header.Get("WWW-Authenticate"))
	}
}

func TestConnectorRegistration(t *testing.T) {
	a := NewApp(t)
	h := map[string]string{"Content-Type": "application/json"}
	for _, body := range []map[string]any{
		{"redirect_uris": []string{"http://evil.example/cb"}},
		{"redirect_uris": []string{}},
		{"redirect_uris": []string{"https://ok.example/cb"}, "grant_types": []string{"password"}},
		{"redirect_uris": []string{"https://ok.example/cb"}, "token_endpoint_auth_method": "private_key_jwt"},
	} {
		if r := a.Do(t, http.MethodPost, "/oauth/register", rt.JSONBody(body), h); r.Status != http.StatusBadRequest {
			t.Errorf("accepted %v: %d", body, r.Status)
		}
	}
	conf := a.Do(t, http.MethodPost, "/oauth/register", rt.JSONBody(map[string]any{"redirect_uris": []string{"https://ok.example/cb"}, "client_name": "Conf"}), h).JSON(t)
	if conf["token_endpoint_auth_method"] != "client_secret_basic" || !strings.HasPrefix(conf["client_secret"].(string), PrefixSecret) {
		t.Fatalf("default registration should be confidential: %v", conf)
	}
	var stored string
	_ = a.Database.QueryRow(`SELECT client_secret_hash FROM oauth_clients WHERE client_id = $1`, conf["client_id"]).Scan(&stored)
	if stored == conf["client_secret"] || stored != accountapp.HashSecret(conf["client_secret"].(string)) {
		t.Fatal("client secret not stored as a hash")
	}
}

// TestConnectorCursorFlow replays what Cursor sends (2026-10-15: a real
// Cursor user got 400 from /oauth/register for its cursor:// redirect).
func TestConnectorCursorFlow(t *testing.T) {
	a := NewApp(t)
	ann := a.NewPerson(t, "ann")
	const redirect = "cursor://anysphere.cursor-mcp/oauth/callback"
	reg := a.Do(t, http.MethodPost, "/oauth/register", rt.JSONBody(map[string]any{
		"client_name": "Cursor", "redirect_uris": []string{redirect}, "token_endpoint_auth_method": "none",
		"grant_types": []string{"authorization_code", "refresh_token"}, "response_types": []string{"code"},
	}), map[string]string{"Content-Type": "application/json"})
	if reg.Status != http.StatusCreated {
		t.Fatalf("register: %d %s", reg.Status, reg.Body)
	}
	clientID := reg.JSON(t)["client_id"].(string)

	// PKCE stays mandatory for an app-scheme client.
	noPKCE := AuthorizeQuery(clientID, redirect, "")
	noPKCE.Del("code_challenge")
	noPKCE.Del("code_challenge_method")
	if r := a.Do(t, http.MethodGet, "/oauth/authorize?"+noPKCE.Encode(), nil, nil); r.Status != http.StatusFound || !strings.Contains(r.Header.Get("Location"), "error=invalid_request") {
		t.Fatalf("authorize without PKCE: %d %s", r.Status, r.Header.Get("Location"))
	}

	_, challenge := NewVerifier()
	page := a.Do(t, http.MethodGet, "/oauth/authorize?"+AuthorizeQuery(clientID, redirect, challenge).Encode(), nil, nil)
	m := ConnectDataRe.FindSubmatch(page.Body)
	if page.Status != http.StatusOK || m == nil {
		t.Fatalf("authorize page: %d", page.Status)
	}
	var data map[string]any
	_ = json.Unmarshal(m[1], &data)
	if data["client_name"] != "Cursor" || data["redirect_host"] != "cursor://anysphere.cursor-mcp" {
		t.Fatalf("consent data: %v", data)
	}

	tok := a.Connect(t, ann, clientID, redirect)
	if !strings.HasPrefix(tok["access_token"].(string), PrefixAccess) {
		t.Fatalf("tokens: %v", tok)
	}
	if list := a.Rpc(t, tok["access_token"].(string), "tools/list", map[string]any{}); !strings.Contains(string(list.Body), `"who_am_i"`) {
		t.Fatalf("tools/list: %s", list.Body)
	}
}

func TestConnectorAuthorizeRefusals(t *testing.T) {
	a := NewApp(t)
	ann := a.NewPerson(t, "ann")
	clientID := a.RegisterClient(t, TestRedirect)
	_, challenge := NewVerifier()

	// A redirect URI the client did not register is shown to the person and
	// never followed: following it would be an open redirect.
	q := AuthorizeQuery(clientID, "https://attacker.example/cb", challenge)
	if r := a.Do(t, http.MethodGet, "/oauth/authorize?"+q.Encode(), nil, nil); r.Status != http.StatusBadRequest || r.Header.Get("Location") != "" {
		t.Fatalf("unregistered redirect: %d %q", r.Status, r.Header.Get("Location"))
	}
	q = AuthorizeQuery("shc_unknown", TestRedirect, challenge)
	if r := a.Do(t, http.MethodGet, "/oauth/authorize?"+q.Encode(), nil, nil); r.Status != http.StatusBadRequest || r.Header.Get("Location") != "" {
		t.Fatalf("unknown client: %d", r.Status)
	}
	for name, mutate := range map[string]func(url.Values){
		"plain PKCE":       func(v url.Values) { v.Set("code_challenge_method", "plain") },
		"missing PKCE":     func(v url.Values) { v.Del("code_challenge"); v.Del("code_challenge_method") },
		"token response":   func(v url.Values) { v.Set("response_type", "token") },
		"foreign audience": func(v url.Values) { v.Set("resource", "https://other.example/mcp") },
	} {
		q := AuthorizeQuery(clientID, TestRedirect, challenge)
		mutate(q)
		r := a.Do(t, http.MethodGet, "/oauth/authorize?"+q.Encode(), nil, nil)
		loc, _ := url.Parse(r.Header.Get("Location"))
		if r.Status != http.StatusFound || loc == nil || loc.Query().Get("error") == "" || loc.Query().Get("state") != "st4te" {
			t.Errorf("%s: %d %q", name, r.Status, r.Header.Get("Location"))
		}
	}

	q = AuthorizeQuery(clientID, TestRedirect, challenge)
	page := a.Do(t, http.MethodGet, "/oauth/authorize?"+q.Encode(), nil, nil)
	var data map[string]any
	_ = json.Unmarshal(ConnectDataRe.FindSubmatch(page.Body)[1], &data)
	decide := func(key, csrf, origin string) int {
		h := map[string]string{"Content-Type": "application/json", "X-API-Key": key}
		if origin != "" {
			h["Origin"] = origin
		}
		return a.Do(t, http.MethodPost, "/oauth/authorize/decision", rt.JSONBody(map[string]string{"query": q.Encode(), "csrf": csrf, "decision": "allow"}), h).Status
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
	a := NewApp(t)
	ann := a.NewPerson(t, "ann")
	clientID := a.RegisterClient(t, TestRedirect)
	redeem := func(code, verifier, redirect string) Resp {
		return a.Form(t, "/oauth/token", url.Values{
			"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirect},
			"client_id": {clientID}, "code_verifier": {verifier},
		}, nil)
	}
	fresh := func() (string, string) {
		v, c := NewVerifier()
		return v, CodeFrom(t, a.Consent(t, AuthorizeQuery(clientID, TestRedirect, c), ann.Key, "allow"))
	}
	isInvalidGrant := func(r Resp) bool {
		return r.Status == http.StatusBadRequest && r.JSON(t)["error"] == "invalid_grant"
	}

	t.Run("wrong verifier, and the code is spent", func(t *testing.T) {
		v, code := fresh()
		other, _ := NewVerifier()
		if r := redeem(code, other, TestRedirect); !isInvalidGrant(r) {
			t.Fatalf("wrong verifier: %d %s", r.Status, r.Body)
		}
		if r := redeem(code, v, TestRedirect); !isInvalidGrant(r) {
			t.Fatalf("code usable after a failed attempt: %d", r.Status)
		}
	})
	t.Run("missing verifier", func(t *testing.T) {
		_, code := fresh()
		if r := redeem(code, "", TestRedirect); !isInvalidGrant(r) {
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
		_, _ = a.Database.Exec(`UPDATE oauth_codes SET expires_at = now() - interval '1 second' WHERE code_hash = $1`, accountapp.HashSecret(code))
		if r := redeem(code, v, TestRedirect); !isInvalidGrant(r) {
			t.Fatalf("%d", r.Status)
		}
	})
	t.Run("another client", func(t *testing.T) {
		v, code := fresh()
		other := a.RegisterClient(t, TestRedirect)
		r := a.Form(t, "/oauth/token", url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {TestRedirect}, "client_id": {other}, "code_verifier": {v}}, nil)
		if !isInvalidGrant(r) {
			t.Fatalf("%d", r.Status)
		}
	})
	t.Run("reuse revokes what the first redemption issued", func(t *testing.T) {
		v, code := fresh()
		first := redeem(code, v, TestRedirect)
		if first.Status != http.StatusOK {
			t.Fatalf("first: %d %s", first.Status, first.Body)
		}
		access := first.JSON(t)["access_token"].(string)
		if r := a.Rpc(t, access, "ping", map[string]any{}); r.Status != 200 {
			t.Fatalf("fresh token: %d", r.Status)
		}
		if r := redeem(code, v, TestRedirect); !isInvalidGrant(r) {
			t.Fatalf("reuse: %d", r.Status)
		}
		if r := a.Rpc(t, access, "ping", map[string]any{}); r.Status != http.StatusUnauthorized {
			t.Fatalf("token survived code reuse: %d", r.Status)
		}
	})
}

func TestConnectorRefreshRotationAndReuse(t *testing.T) {
	a := NewApp(t)
	ann := a.NewPerson(t, "ann")
	clientID := a.RegisterClient(t, TestRedirect)
	tokens := a.Connect(t, ann, clientID, TestRedirect)
	refresh := func(rt string) Resp {
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
	t3 := a.Connect(t, ann, clientID, TestRedirect)
	other := a.RegisterClient(t, TestRedirect)
	if r := a.Form(t, "/oauth/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {t3["refresh_token"].(string)}, "client_id": {other}}, nil); r.Status != http.StatusBadRequest {
		t.Fatalf("cross-client refresh: %d", r.Status)
	}
}

func TestConnectorToolsHaveRESTPermissions(t *testing.T) {
	a := NewApp(t)
	ann, bob := a.NewPerson(t, "ann"), a.NewPerson(t, "bob")
	clientID := a.RegisterClient(t, TestRedirect)
	a.Deploy(t, bob, "bobs-page")
	access := a.Connect(t, ann, clientID, TestRedirect)["access_token"].(string)
	for _, call := range []map[string]any{
		{"name": "add_site_editor", "arguments": map[string]any{"site": "bobs-page", "person": ann.Handle}},
		{"name": "set_site_access", "arguments": map[string]any{"site": "bobs-page", "access": "specific"}},
	} {
		text, _, isErr := ToolResultOf(t, a.Rpc(t, access, "tools/call", call))
		if !isErr || !(strings.Contains(text, "404") || strings.Contains(text, "no site named")) {
			t.Errorf("%s on another person's site: %q", call["name"], text)
		}
	}
	var n int
	_ = a.Database.QueryRow(`SELECT count(*) FROM site_editors`).Scan(&n)
	if n != 0 {
		t.Fatal("bob's site gained an editor")
	}
	// An /mcp token is not a REST credential.
	if r := a.Do(t, http.MethodGet, "/v1/me", nil, map[string]string{"Authorization": "Bearer " + access}); r.Status != http.StatusUnauthorized {
		t.Fatalf("/mcp token accepted on /v1: %d", r.Status)
	}
}

// The consent page reads the API key from localStorage, so its scripts run
// only by nonce.
func TestConsentPageScriptNonce(t *testing.T) {
	a := NewApp(t)
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

// Meta Muse cannot register itself, so its Client ID is the URL of a metadata
// document we host. That client is pre-registered, public and held to PKCE.
func TestConnectorMuseClient(t *testing.T) {
	a := NewApp(t)
	ctx := context.Background()
	for i := 0; i < 2; i++ { // idempotent, as at every boot
		if err := a.Conn.EnsureBuiltinClients(ctx); err != nil {
			t.Fatal(err)
		}
	}
	museID := a.Srv.URL + MuseClientPath
	t.Cleanup(func() { _, _ = a.Database.Exec(`DELETE FROM oauth_clients WHERE client_id = $1`, museID) })
	t.Cleanup(func() {
		_, _ = a.Database.Exec(`DELETE FROM oauth_clients WHERE client_id = $1`, MuseShortClientID)
	})

	doc := a.Do(t, http.MethodGet, MuseClientPath, nil, nil)
	if doc.Status != 200 || !strings.HasPrefix(doc.Header.Get("Content-Type"), "application/json") {
		t.Fatalf("metadata document: %d %q", doc.Status, doc.Header.Get("Content-Type"))
	}
	m := doc.JSON(t)
	if m["client_id"] != museID || m["token_endpoint_auth_method"] != "none" || m["client_secret"] != nil {
		t.Fatalf("metadata document: %v", m)
	}
	if r := m["redirect_uris"].([]any); len(r) != 1 || r[0] != MuseRedirectURI {
		t.Fatalf("redirect_uris: %v", r)
	}
	c, err := accountsrepo.GetOAuthClient(ctx, a.Database, museID)
	if err != nil || c.Dynamic || !c.PKCERequired || c.SecretHash.Valid {
		t.Fatalf("stored client: %+v %v", c, err)
	}
	// A phone keyboard cannot mangle a short id. The same client answers to it.
	short, err := accountsrepo.GetOAuthClient(ctx, a.Database, MuseShortClientID)
	if err != nil || short.Dynamic || !short.PKCERequired || short.SecretHash.Valid || len(short.RedirectURIs) != 1 || short.RedirectURIs[0] != MuseRedirectURI {
		t.Fatalf("short client: %+v %v", short, err)
	}

	ann := a.NewPerson(t, "ann")
	_, challenge := NewVerifier()
	// The whole flow works under the short id too.
	if tokens := a.Connect(t, ann, MuseShortClientID, MuseRedirectURI); tokens["access_token"] == nil {
		t.Fatalf("connect with short id: %v", tokens)
	}
	// A redirect the document does not list is shown, never followed.
	q := AuthorizeQuery(museID, "https://agent.meta.ai/api/hatch/oauth/other", challenge)
	if r := a.Do(t, http.MethodGet, "/oauth/authorize?"+q.Encode(), nil, nil); r.Status != http.StatusBadRequest || r.Header.Get("Location") != "" {
		t.Fatalf("unlisted redirect: %d %q", r.Status, r.Header.Get("Location"))
	}
	// PKCE is required.
	q = AuthorizeQuery(museID, MuseRedirectURI, challenge)
	q.Del("code_challenge")
	q.Del("code_challenge_method")
	r := a.Do(t, http.MethodGet, "/oauth/authorize?"+q.Encode(), nil, nil)
	if loc, _ := url.Parse(r.Header.Get("Location")); r.Status != http.StatusFound || loc.Query().Get("error") != "invalid_request" {
		t.Fatalf("no PKCE: %d %q", r.Status, r.Header.Get("Location"))
	}

	// The whole flow, asking only for "sites" (no offline_access), as Muse does.
	tokens := a.Connect(t, ann, museID, MuseRedirectURI)
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
