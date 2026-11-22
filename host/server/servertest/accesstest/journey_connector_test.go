package accesstest

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/host/access/connector"

	"github.com/vsriram/simple-host/host/access/auth"
	. "github.com/vsriram/simple-host/host/access/featuretest"
	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	model "github.com/vsriram/simple-host/host/model"
	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"

	"github.com/vsriram/simple-host/host/access/accesshttp"

	accountapp "github.com/vsriram/simple-host/host/access"
)

const reviewerTestPassword = "correct horse battery staple"

// TestReviewerSignInRefusesPasswordAccount: a password account under the
// operator's configured REVIEW_ACCOUNT_EMAIL -- admin-created here, so its
// users.username really is that address (a self sign-up's username is now
// a synthetic value and could never be found by that address in the first
// place: see TestPasswordSelfSignupNeverHijacksEmailSignIn) -- never
// proved it controls that address, so the reviewer must not be handed it
// (and the account's own password must not open the reviewer's
// connected-app access) even though the email and reviewer password both
// match.
func TestReviewerSignInRefusesPasswordAccount(t *testing.T) {
	a := NewApp(t)
	a.Users.SetPasswordSignIn(true, true)
	reviewer := "reviewer-" + strconv.FormatInt(time.Now().UnixNano(), 36) + "@example.com"
	t.Cleanup(func() { _, _ = a.Database.Exec(`DELETE FROM users WHERE username = $1`, reviewer) })
	encoded, _ := accesshttp.HashReviewerPassword(reviewerTestPassword)
	if !a.Conn.EnableReviewerSignIn(reviewer, encoded) {
		t.Fatal("could not enable")
	}

	if _, _, err := accountapp.New(a.Database).CreatePasswordAccount(context.Background(), reviewer, "", true, false, accountapp.AssignHandle); err != nil {
		t.Fatalf("admin-created password account under the reviewer address: %v", err)
	}

	if r := a.ReviewerSignIn(t, reviewer, reviewerTestPassword, nil); r.Status != http.StatusForbidden || strings.Contains(string(r.Body), "api_key") {
		t.Fatalf("reviewer sign-in reused a password account: %d %s", r.Status, r.Body)
	}
}

// Account keys are stored only as SHA-256; rotating kills every key the
// account holds; internal credentials die with their request.
func TestAPIKeysStoredHashed(t *testing.T) {
	a := NewApp(t)
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

func TestConnectorConfidentialClientForGPTActions(t *testing.T) {
	a := NewApp(t)
	ann := a.NewPerson(t, "ann")
	redirect := "https://chatgpt.com/aip/g-test/oauth/callback"
	id, secret, err := connector.CreateOperatorClient(context.Background(), a.Database, "Simple Host GPT", []string{redirect}, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = accountsrepo.DeleteOAuthClient(context.Background(), a.Database, id) })
	q := url.Values{"response_type": {"code"}, "client_id": {id}, "redirect_uri": {redirect}, "scope": {"sites"}, "state": {"gpt"}}
	code := CodeFrom(t, a.Consent(t, q, ann.Key, "allow"))
	if r := a.Form(t, "/oauth/token", url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirect}, "client_id": {id}, "client_secret": {"wrong"}}, nil); r.Status != http.StatusUnauthorized {
		t.Fatalf("wrong secret: %d", r.Status)
	}
	code = CodeFrom(t, a.Consent(t, q, ann.Key, "allow"))
	tok := a.Form(t, "/oauth/token", url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirect}, "client_id": {id}, "client_secret": {secret}}, nil)
	if tok.Status != 200 {
		t.Fatalf("GPT token: %d %s", tok.Status, tok.Body)
	}
	bearer := map[string]string{"Authorization": "Bearer " + tok.JSON(t)["access_token"].(string)}
	if r := a.Do(t, http.MethodGet, "/v1/me", nil, bearer); r.Status != http.StatusOK || r.JSON(t)["username"] != ann.Email {
		t.Fatalf("bearer on /v1/me: %d %s", r.Status, r.Body)
	}
	// A verifier for a code issued without a challenge is a downgrade attempt.
	code = CodeFrom(t, a.Consent(t, q, ann.Key, "allow"))
	v, _ := NewVerifier()
	if r := a.Form(t, "/oauth/token", url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirect}, "client_id": {id}, "client_secret": {secret}, "code_verifier": {v}}, nil); r.Status != http.StatusBadRequest {
		t.Fatalf("PKCE downgrade accepted: %d", r.Status)
	}
	// A self-registered client can never skip PKCE.
	pub := a.RegisterClient(t, TestRedirect)
	q2 := url.Values{"response_type": {"code"}, "client_id": {pub}, "redirect_uri": {TestRedirect}, "state": {"s"}}
	if r := a.Do(t, http.MethodGet, "/oauth/authorize?"+q2.Encode(), nil, nil); r.Status != http.StatusFound || !strings.Contains(r.Header.Get("Location"), "error=invalid_request") {
		t.Fatalf("public client without PKCE: %d %s", r.Status, r.Header.Get("Location"))
	}
}

func TestConnectorHappyPathThroughMCP(t *testing.T) {
	a := NewApp(t)
	ann := a.NewPerson(t, "ann")
	clientID := a.RegisterClient(t, TestRedirect)

	verifier, challenge := NewVerifier()
	q := AuthorizeQuery(clientID, TestRedirect, challenge)
	q.Set("resource", a.Srv.URL+"/mcp")
	page := a.Do(t, http.MethodGet, "/oauth/authorize?"+q.Encode(), nil, nil)
	if !strings.Contains(page.Header.Get("Content-Security-Policy"), "frame-ancestors 'none'") || page.Header.Get("X-Frame-Options") != "DENY" {
		t.Fatal("consent page can be framed")
	}
	redirectTo := a.Consent(t, q, ann.Key, "allow")
	u, _ := url.Parse(redirectTo)
	if !strings.HasPrefix(redirectTo, TestRedirect+"?") || u.Query().Get("state") != "st4te" || u.Query().Get("iss") != a.Srv.URL {
		t.Fatalf("redirect %s", redirectTo)
	}
	tok := a.Form(t, "/oauth/token", url.Values{
		"grant_type": {"authorization_code"}, "code": {u.Query().Get("code")}, "redirect_uri": {TestRedirect},
		"client_id": {clientID}, "code_verifier": {verifier}, "resource": {a.Srv.URL + "/mcp"},
	}, nil)
	if tok.Status != http.StatusOK || tok.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("token: %d %s", tok.Status, tok.Body)
	}
	tokens := tok.JSON(t)
	access := tokens["access_token"].(string)
	if !strings.HasPrefix(access, connector.PrefixAccess) || tokens["expires_in"].(float64) != 3600 || tokens["token_type"] != "Bearer" {
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
	if !strings.Contains(string(list.Body), `"who_am_i"`) {
		t.Fatalf("tools/list: %s", list.Body)
	}
	// The email is left out unless the person asks for it (2026-10-28).
	text, s, isErr := ToolResultOf(t, a.Rpc(t, access, "tools/call", map[string]any{"name": "who_am_i", "arguments": map[string]any{}}))
	if isErr || s["email"] != "" || strings.Contains(text, ann.Email) {
		t.Fatalf("who_am_i: %s", text)
	}
	text, s, isErr = ToolResultOf(t, a.Rpc(t, access, "tools/call", map[string]any{"name": "who_am_i", "arguments": map[string]any{"include_email": true}}))
	if isErr || s["email"] != ann.Email {
		t.Fatalf("who_am_i include_email: %s", text)
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

// An ordinary Host ignores the hackathon consent choice. mode=events does not
// become an events grant, and the tool list stays the website inventory.
func TestConnectorHostIgnoresEventManagementMode(t *testing.T) {
	a := NewApp(t)
	for _, path := range []string{"/.well-known/oauth-protected-resource/mcp", "/.well-known/oauth-authorization-server"} {
		scopes, _ := a.Do(t, http.MethodGet, path, nil, nil).JSON(t)["scopes_supported"].([]any)
		if len(scopes) != 1 || scopes[0] != "sites" {
			t.Fatalf("%s scopes: %v", path, scopes)
		}
	}
	p := a.NewPerson(t, "host")
	clientID := a.RegisterClient(t, TestRedirect)
	verifier, challenge := NewVerifier()
	q := AuthorizeQuery(clientID, TestRedirect, challenge)
	q.Set("resource", a.Srv.URL+"/mcp")
	_, csrf := a.ConsentPage(t, q)
	decided := a.DecideConsent(t, q, csrf, p.Key, map[string]any{"mode": "events", "team_id": "00000000-0000-4000-8000-000000000000"})
	if decided.Status != http.StatusOK {
		t.Fatalf("decision: %d %s", decided.Status, decided.Body)
	}
	tok := a.ExchangeCode(t, clientID, TestRedirect, verifier, CodeFrom(t, decided.JSON(t)["redirect_to"].(string)), a.Srv.URL+"/mcp")
	if tok["scope"] != "sites" {
		t.Fatalf("scope: %v", tok["scope"])
	}
	access := tok["access_token"].(string)
	names := mcpToolNames(t, a.Rpc(t, access, "tools/list", map[string]any{}))
	if !hasTool(names, "create_site") || hasTool(names, "hack_list_events") {
		t.Fatalf("ordinary tools: %v", names)
	}
	if _, _, isErr := ToolResultOf(t, a.Rpc(t, access, "tools/call", map[string]any{"name": "who_am_i", "arguments": map[string]any{}})); isErr {
		t.Fatal("who_am_i failed on an ordinary connection")
	}
}

func TestConnectorRevocation(t *testing.T) {
	a := NewApp(t)
	ann := a.NewPerson(t, "ann")
	clientID := a.RegisterClient(t, TestRedirect)

	tokens := a.Connect(t, ann, clientID, TestRedirect)
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
	t2 := a.Connect(t, ann, clientID, TestRedirect)
	if r := a.Do(t, http.MethodDelete, "/v1/me/connections/"+clientID, nil, map[string]string{"X-API-Key": ann.Key}); r.Status != http.StatusNoContent {
		t.Fatalf("disconnect: %d", r.Status)
	}
	if r := a.Rpc(t, t2["access_token"].(string), "ping", map[string]any{}); r.Status != http.StatusUnauthorized {
		t.Fatal("token survived disconnect")
	}
	// Rotating the API key disconnects every app.
	t3 := a.Connect(t, ann, clientID, TestRedirect)
	if r := a.Do(t, http.MethodPost, "/v1/me/api-key/rotate", nil, map[string]string{"X-API-Key": ann.Key}); r.Status != 200 {
		t.Fatalf("rotate: %d", r.Status)
	}
	if r := a.Rpc(t, t3["access_token"].(string), "ping", map[string]any{}); r.Status != http.StatusUnauthorized {
		t.Fatal("token survived key rotation")
	}
	// Deleting the person deletes their connections.
	bob := a.NewPerson(t, "bob")
	t4 := a.Connect(t, bob, clientID, TestRedirect)
	_, _ = a.Database.Exec(`DELETE FROM users WHERE username = $1`, bob.Email)
	if r := a.Rpc(t, t4["access_token"].(string), "ping", map[string]any{}); r.Status != http.StatusUnauthorized {
		t.Fatal("token survived account deletion")
	}
}

// A photo link ends with the connection grant behind it: disconnected,
// signed out everywhere, account suspended or deleted, or the grant moved
// to another person. An expired access token alone does not end it (the
// grant still stands), but it does end an archive link.
func TestPhotoLinkEndsWithItsGrant(t *testing.T) {
	a := NewApp(t)
	for _, change := range []string{"access_expired", "disconnect", "sign_out_everywhere", "suspended", "user_deleted", "different_user"} {
		t.Run(change, func(t *testing.T) {
			owner := a.NewPerson(t, "photo-grant")
			client := a.RegisterClient(t, TestRedirect)
			access := a.Connect(t, owner, client, TestRedirect)["access_token"].(string)
			grant, err := accountsrepo.GetOAuthToken(context.Background(), a.Database, accountapp.HashSecret(access))
			if err != nil {
				t.Fatal(err)
			}
			getLink, saveLink := first(mintLink(t, a, access, "", "blog", false)), first(mintLink(t, a, access, "", "blog", false))
			archive := first(mintLink(t, a, access, "", "blog", true))
			exec := func(query string, args ...any) {
				t.Helper()
				if _, err := a.Database.Exec(query, args...); err != nil {
					t.Fatal(err)
				}
			}
			switch change {
			case "access_expired":
				exec(`UPDATE oauth_tokens SET expires_at=now()-interval '1 second' WHERE token_hash=$1`, accountapp.HashSecret(access))
			case "disconnect":
				if r := a.Do(t, "DELETE", "/v1/me/connections/"+client, nil, owner.Header()); r.Status != 204 {
					t.Fatalf("disconnect: %d %s", r.Status, r.Body)
				}
			case "sign_out_everywhere":
				if r := a.Do(t, "POST", "/v1/me/api-key/rotate", nil, owner.Header()); r.Status != 200 {
					t.Fatalf("sign out everywhere: %d %s", r.Status, r.Body)
				}
			case "suspended":
				exec(`UPDATE users SET suspended_at=now() WHERE id=$1`, grant.UserID)
			case "user_deleted":
				exec(`DELETE FROM users WHERE id=$1`, grant.UserID)
			case "different_user":
				exec(`UPDATE oauth_grants SET user_id=$1 WHERE id=$2`, a.NewPerson(t, "photo-grant-other").ID, grant.GrantID)
			}
			want := 403
			if change == "access_expired" {
				want = 200
				if r := putUpload(t, a, archive); r.Status != 403 || r.Code() != "upload_link_signed_out" {
					t.Fatalf("archive after access expiry: %d %s", r.Status, r.Body)
				}
			}
			if r := photoLink(t, a, "GET", getLink, ""); r.Status != want {
				t.Fatalf("lookup: %d %s (want %d)", r.Status, r.Body, want)
			}
			if r := photoLink(t, a, "POST", saveLink, ""); r.Status != want {
				t.Fatalf("save: %d %s (want %d)", r.Status, r.Body, want)
			}
		})
	}
}

func TestReviewerSignInEndToEnd(t *testing.T) {
	a := NewApp(t)
	clientID := a.RegisterClient(t, TestRedirect)
	reviewer := "reviewer-" + strconv.FormatInt(time.Now().UnixNano(), 36) + "@example.com"
	t.Cleanup(func() { _, _ = a.Database.Exec(`DELETE FROM users WHERE username = $1`, reviewer) })
	encoded, _ := accesshttp.HashReviewerPassword(reviewerTestPassword)

	// Off by default: no endpoint, no option on the page.
	if r := a.ReviewerSignIn(t, reviewer, reviewerTestPassword, nil); r.Status != http.StatusNotFound {
		t.Fatalf("disabled reviewer sign-in answered %d", r.Status)
	}
	if _, shown := a.AuthorizeData(t, clientID)["reviewer_signin"]; shown {
		t.Fatal("consent page offers reviewer sign-in while it is off")
	}

	if !a.Conn.EnableReviewerSignIn(reviewer, encoded) {
		t.Fatal("could not enable")
	}
	if a.AuthorizeData(t, clientID)["reviewer_signin"] != true {
		t.Fatal("consent page does not offer reviewer sign-in")
	}

	// Another person's email with the reviewer password opens nothing, and
	// the reviewer's email with any other password opens nothing.
	other := a.NewPerson(t, "someone")
	for _, c := range [][2]string{{reviewer, "wrong password, long enough"}, {other.Email, reviewerTestPassword}, {"", reviewerTestPassword}, {reviewer, ""}} {
		if r := a.ReviewerSignIn(t, c[0], c[1], nil); r.Status != http.StatusUnauthorized || strings.Contains(string(r.Body), "api_key") {
			t.Fatalf("%q/%q: %d %s", c[0], c[1], r.Status, r.Body)
		}
	}
	// Cross-site posts are refused before any password check.
	if r := a.ReviewerSignIn(t, reviewer, reviewerTestPassword, map[string]string{"Origin": "https://evil.example"}); r.Status != http.StatusForbidden {
		t.Fatalf("cross-origin: %d", r.Status)
	}

	// The right pair signs in as an ordinary account, created on first use.
	r := a.ReviewerSignIn(t, "  "+strings.ToUpper(reviewer)+" ", reviewerTestPassword, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("reviewer sign-in: %d %s", r.Status, r.Body)
	}
	key := r.JSON(t)["api_key"].(string)
	me := a.Do(t, http.MethodGet, "/v1/me", nil, map[string]string{"X-API-Key": key}).JSON(t)
	if me["username"] != reviewer || me["is_admin"] != false || me["handle"] == "" {
		t.Fatalf("reviewer account: %v", me)
	}

	// And it connects an app end to end.
	access := a.Connect(t, Person{Email: reviewer, Key: key}, clientID, TestRedirect)["access_token"].(string)
	text, _, isErr := ToolResultOf(t, a.Rpc(t, access, "tools/call", map[string]any{"name": "who_am_i", "arguments": map[string]any{}}))
	if isErr {
		t.Fatalf("who_am_i as the reviewer: %s", text)
	}

	// A second sign-in returns the same account, not a new one. Keys are
	// stored hashed, so it hands out a new key and the first keeps working.
	again := a.ReviewerSignIn(t, reviewer, reviewerTestPassword, nil)
	if again.Status != http.StatusOK {
		t.Fatalf("second sign-in: %d", again.Status)
	}
	key2, _ := again.JSON(t)["api_key"].(string)
	for _, k := range []string{key, key2} {
		if me := a.Do(t, http.MethodGet, "/v1/me", nil, map[string]string{"X-API-Key": k}).JSON(t); me["username"] != reviewer {
			t.Fatalf("key after second sign-in: %v", me)
		}
	}
}

// A sign-in link token is redeemable only with the nonce of the browser that
// asked for it (login-CSRF fix); the typed code needs no nonce.
func TestSignInLinkNeedsTheRequestingBrowsersNonce(t *testing.T) {
	a := NewApp(t)
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
	verify := func(body map[string]string) Resp {
		return a.Do(t, http.MethodPost, "/v1/auth/verify", rt.JSONBody(body), map[string]string{"Content-Type": "application/json"})
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
	if r := a.Do(t, http.MethodPost, "/v1/auth", rt.JSONBody(map[string]string{"email": ann.Email, "nonce_hash": "not-a-hash"}), map[string]string{"Content-Type": "application/json"}); r.Status != http.StatusBadRequest {
		t.Fatalf("malformed nonce_hash accepted: %d", r.Status)
	}
}

// An archive link deploys once, as its maker, for 15 minutes; it ends when
// the connection is removed or the key it was made with is revoked.
func TestUploadLinkSingleUseAndEnds(t *testing.T) {
	a := NewApp(t)
	owner := a.NewPerson(t, "upload-owner")
	client := a.RegisterClient(t, TestRedirect)
	token := a.Connect(t, owner, client, TestRedirect)["access_token"].(string)

	link, expires := mintLink(t, a, token, "", "archive", true)
	if left := time.Until(expires); left > 15*time.Minute || left < 14*time.Minute {
		t.Fatalf("archive link lifetime: %v", left)
	}
	if r := putUpload(t, a, link); r.Status != http.StatusCreated || r.JSON(t)["user"] != owner.ID || r.JSON(t)["site"] != "archive" {
		t.Fatalf("upload: %d %s", r.Status, r.Body)
	}
	if r := putUpload(t, a, link); r.Status != 404 || r.Code() != "upload_link_invalid" {
		t.Fatalf("reuse: %d %s", r.Status, r.Body)
	}

	r := a.Do(t, "POST", "/v1/me/keys", map[string]any{"name": "upload", "scope": "deploy"}, owner.Header())
	if r.Status != http.StatusCreated {
		t.Fatalf("deploy key: %d %s", r.Status, r.Body)
	}
	keyID, deployKey := r.JSON(t)["id"].(string), r.JSON(t)["api_key"].(string)
	if r := putUpload(t, a, first(mintLink(t, a, "", deployKey, "archive", true))); r.Status != http.StatusCreated {
		t.Fatalf("deploy-only key's link: %d %s", r.Status, r.Body)
	}

	link = first(mintLink(t, a, token, "", "archive", true))
	if r := a.Do(t, "DELETE", "/v1/me/connections/"+client, nil, owner.Header()); r.Status != 204 {
		t.Fatalf("disconnect: %d %s", r.Status, r.Body)
	}
	if r := putUpload(t, a, link); r.Status != 403 || r.Code() != "upload_link_signed_out" {
		t.Fatalf("after disconnect: %d %s", r.Status, r.Body)
	}
	link = first(mintLink(t, a, "", deployKey, "archive", true))
	if r := a.Do(t, "DELETE", "/v1/me/keys/"+keyID, nil, owner.Header()); r.Status != 204 {
		t.Fatalf("revoke: %d %s", r.Status, r.Body)
	}
	if r := putUpload(t, a, link); r.Status != 403 || r.Code() != "upload_link_signed_out" {
		t.Fatalf("after key revoke: %d %s", r.Status, r.Body)
	}
}

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

func first(s string, _ time.Time) string { return s }

// mintLink calls create_upload_link for site with an OAuth token (or, with
// key set, with an API key on /mcp) and returns the link's token and expiry.
func mintLink(t *testing.T, a *App, token, key, site string, archive bool) (string, time.Time) {
	t.Helper()
	params := map[string]any{"name": "create_upload_link", "arguments": map[string]any{"site": site, "archive": archive}}
	var r Resp
	if key != "" {
		r = a.Do(t, "POST", "/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": params},
			map[string]string{"X-API-Key": key, "Content-Type": "application/json"})
	} else {
		r = a.Rpc(t, token, "tools/call", params)
	}
	_, out, failed := ToolResultOf(t, r)
	if failed {
		t.Fatalf("mint: %d %s", r.Status, r.Body)
	}
	field := "photos_url"
	if archive {
		field = "upload_url"
	}
	u, err := url.Parse(out[field].(string))
	if err != nil {
		t.Fatal(err)
	}
	expires, err := time.Parse(time.RFC3339, out["expires_at"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if archive {
		return strings.TrimPrefix(u.Path, "/v1/uploads/"), expires
	}
	return strings.TrimPrefix(u.Fragment, "t="), expires
}

func putUpload(t *testing.T, a *App, token string) Resp {
	t.Helper()
	return a.Do(t, "PUT", "/v1/uploads/"+token, "archive", map[string]string{"Content-Type": "application/gzip"})
}

func photoLink(t *testing.T, a *App, method, token, query string) Resp {
	t.Helper()
	var body any
	if method == "POST" {
		body = "photos"
	}
	return a.Do(t, method, "/v1/photo-links"+query, body, map[string]string{"X-Photo-Token": token, "Origin": a.Srv.URL, "Content-Type": "multipart/form-data; boundary=x"})
}
