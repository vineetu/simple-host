// Package connector is connecting an AI app (F23): standard OAuth with
// self-registration and PKCE. The person signs in, approves once, and the
// app's token then acts as them everywhere a key would. Codes, client
// secrets and tokens are stored as hex SHA-256 and keep the old service's
// prefixes and lengths, so copied connections keep working.
package connector

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/lib/pq"

	access "simplehost/access/core"
	"simplehost/app"
	"simplehost/core/db"
	"simplehost/core/web"
)

const (
	accessLife  = time.Hour
	refreshLife = 90 * 24 * time.Hour
	codeLife    = 10 * time.Minute
	scope       = "sites"

	// Meta Muse neither registers itself nor publishes metadata, so it is
	// registered here under the id people paste into it.
	museID       = "meta-muse"
	museName     = "Meta Muse"
	museRedirect = "https://agent.meta.ai/api/hatch/oauth/callback"
)

type handler struct {
	a      *app.App
	issuer string
}

// Mount adds routes 13 and 55 to 60, and registers Meta Muse.
func Mount(a *app.App) {
	h := &handler{a, "https://" + a.Config.AppDomain}
	if _, err := a.DB.Exec(`INSERT INTO oauth_clients (id, name, redirect_uris) VALUES ($1, $2, $3)
		ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, redirect_uris = EXCLUDED.redirect_uris`, museID, museName, pq.Array([]string{museRedirect})); err != nil {
		log.Printf("connector: register Meta Muse: %v", err)
	}
	p := a.Routes.Platform
	p.Handle("GET /.well-known/oauth-protected-resource", web.Func(h.resource))
	p.Handle("GET /.well-known/oauth-authorization-server", web.Func(h.server))
	p.Handle("POST /oauth/register", web.Func(h.register))
	p.Handle("GET /oauth/authorize", web.Func(h.authorize))
	p.Handle("POST /oauth/authorize", web.Func(h.approve))
	p.Handle("POST /oauth/token", web.Func(h.token))
	p.Handle("GET /oauth/clients/meta-muse.json", web.Func(h.muse))
	p.Handle("DELETE /v1/me/apps/{id}", web.Func(h.disconnect))
}

// randomToken is prefix plus n random bytes, base64url, as the old service made them.
func randomToken(prefix string, n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return prefix + base64.RawURLEncoding.EncodeToString(b)
}

// oauthError answers in OAuth's own error form, which apps read.
func oauthError(w http.ResponseWriter, status int, code, desc string) error {
	w.Header().Set("Cache-Control", "no-store")
	web.JSON(w, status, map[string]string{"error": code, "error_description": desc})
	return nil
}

// resource is route 55: where apps sign in for /mcp.
func (h *handler) resource(w http.ResponseWriter, r *http.Request) error {
	web.JSON(w, http.StatusOK, map[string]any{"resource": h.issuer + "/mcp", "authorization_servers": []string{h.issuer},
		"bearer_methods_supported": []string{"header"}, "scopes_supported": []string{scope}})
	return nil
}

// server is route 56: the sign-in server's details.
func (h *handler) server(w http.ResponseWriter, r *http.Request) error {
	web.JSON(w, http.StatusOK, map[string]any{
		"issuer": h.issuer, "authorization_endpoint": h.issuer + "/oauth/authorize", "token_endpoint": h.issuer + "/oauth/token",
		"registration_endpoint": h.issuer + "/oauth/register", "scopes_supported": []string{scope},
		"response_types_supported": []string{"code"}, "grant_types_supported": []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":      []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"none", "client_secret_basic", "client_secret_post"},
	})
	return nil
}

// muse is route 60: the Meta Muse metadata document.
func (h *handler) muse(w http.ResponseWriter, r *http.Request) error {
	w.Header().Set("Cache-Control", "public, max-age=3600")
	web.JSON(w, http.StatusOK, map[string]any{"client_id": museID, "client_name": museName, "redirect_uris": []string{museRedirect},
		"grant_types": []string{"authorization_code", "refresh_token"}, "response_types": []string{"code"},
		"token_endpoint_auth_method": "none", "scope": scope})
	return nil
}

// validRedirect: https anywhere, http only back to this computer (apps such
// as Claude Code listen there), or an app's own scheme. Never fragments.
func validRedirect(s string) bool {
	u, err := url.Parse(s)
	if err != nil || !u.IsAbs() || u.Fragment != "" {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		return u.Host != ""
	case "http":
		host := u.Hostname()
		return host == "localhost" || host == "127.0.0.1" || host == "::1"
	case "javascript", "data", "file", "vbscript":
		return false
	}
	return true
}

// register is route 57: an app registers itself (RFC 7591).
func (h *handler) register(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Name      string   `json:"client_name"`
		Redirects []string `json:"redirect_uris"`
		Method    string   `json:"token_endpoint_auth_method"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in) != nil {
		return oauthError(w, http.StatusBadRequest, "invalid_client_metadata", "send the registration as JSON")
	}
	if len(in.Redirects) == 0 || !allValid(in.Redirects) {
		return oauthError(w, http.StatusBadRequest, "invalid_redirect_uri", "redirect_uris must be https, http to localhost, or an app's own scheme, without a fragment")
	}
	if in.Method == "" {
		in.Method = "client_secret_basic"
	}
	if in.Method != "none" && in.Method != "client_secret_basic" && in.Method != "client_secret_post" {
		return oauthError(w, http.StatusBadRequest, "invalid_client_metadata", "token_endpoint_auth_method must be none, client_secret_basic or client_secret_post")
	}
	if len(in.Name) > 100 {
		in.Name = in.Name[:100]
	}
	id := randomToken("shc_", 16)
	out := map[string]any{"client_id": id, "client_id_issued_at": time.Now().Unix(), "client_name": in.Name,
		"redirect_uris": in.Redirects, "token_endpoint_auth_method": in.Method,
		"grant_types": []string{"authorization_code", "refresh_token"}, "response_types": []string{"code"}}
	var secretHash any
	if in.Method != "none" {
		secret := randomToken("shcs_", 32)
		secretHash, out["client_secret"], out["client_secret_expires_at"] = access.Hash(secret), secret, 0
	}
	meta, _ := json.Marshal(map[string]string{"token_endpoint_auth_method": in.Method})
	if _, err := h.a.DB.ExecContext(r.Context(), `INSERT INTO oauth_clients (id, name, redirect_uris, secret_hash, metadata) VALUES ($1, $2, $3, $4, $5)`,
		id, in.Name, pq.Array(in.Redirects), secretHash, meta); err != nil {
		return err
	}
	w.Header().Set("Cache-Control", "no-store")
	web.JSON(w, http.StatusCreated, out)
	return nil
}

func allValid(uris []string) bool {
	for _, u := range uris {
		if !validRedirect(u) {
			return false
		}
	}
	return true
}

type client struct {
	id, name   string
	redirects  []string
	secretHash sql.NullString
}

func (h *handler) client(ctx context.Context, id string) (client, error) {
	c := client{id: id}
	err := h.a.DB.QueryRowContext(ctx, `SELECT name, redirect_uris, secret_hash FROM oauth_clients WHERE id = $1`, id).
		Scan(&c.name, pq.Array(&c.redirects), &c.secretHash)
	return c, err
}

// request is an authorization request, checked far enough that errors can
// go back to the app. Before that they are shown to the person.
func (h *handler) request(r *http.Request, q url.Values) (client, error) {
	c, err := h.client(r.Context(), q.Get("client_id"))
	if err == sql.ErrNoRows {
		return c, web.Refuse(http.StatusBadRequest, "unknown_app", "This app is not registered here. Connect it again from the app.")
	}
	if err != nil {
		return c, err
	}
	if !slices.Contains(c.redirects, q.Get("redirect_uri")) {
		return c, web.Refuse(http.StatusBadRequest, "bad_redirect", "The app sent an address it did not register. Connect it again from the app.")
	}
	return c, nil
}

// back sends the browser to the app's redirect address with params.
func (h *handler) back(w http.ResponseWriter, r *http.Request, q url.Values, params url.Values) error {
	u, _ := url.Parse(q.Get("redirect_uri"))
	v := u.Query()
	for k := range params {
		v.Set(k, params.Get(k))
	}
	if s := q.Get("state"); s != "" {
		v.Set("state", s)
	}
	v.Set("iss", h.issuer)
	u.RawQuery = v.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
	return nil
}

// authorize is route 58 (GET): sign in if needed, then the approval page.
func (h *handler) authorize(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	c, err := h.request(r, q)
	if err != nil {
		return err
	}
	if q.Get("response_type") != "code" {
		return h.back(w, r, q, url.Values{"error": {"unsupported_response_type"}})
	}
	if q.Get("code_challenge") != "" && q.Get("code_challenge_method") != "S256" {
		return h.back(w, r, q, url.Values{"error": {"invalid_request"}, "error_description": {"code_challenge_method must be S256"}})
	}
	who := web.CallerOf(r)
	if who.Via != "session" {
		http.Redirect(w, r, "/signin?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusFound)
		return nil
	}
	name := c.name
	if name == "" {
		name = "An AI app"
	}
	u, _ := url.Parse(q.Get("redirect_uri"))
	fields := map[string]string{}
	for _, k := range []string{"client_id", "redirect_uri", "state", "code_challenge", "scope"} {
		fields[k] = q.Get(k)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "frame-ancestors 'none'")
	w.Header().Set("X-Frame-Options", "DENY")
	return approvalPage.Execute(w, map[string]any{"App": name, "Email": who.Email, "Host": u.Host, "Fields": fields})
}

// approve is route 58 (POST): the person's answer on the approval page.
func (h *handler) approve(w http.ResponseWriter, r *http.Request) error {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		return web.Refuse(http.StatusBadRequest, "bad_form", "Go back to the app and connect again.")
	}
	q := r.PostForm
	if _, err := h.request(r, q); err != nil {
		return err
	}
	who := web.CallerOf(r)
	if who.Via != "session" {
		return web.Refuse(http.StatusUnauthorized, "sign_in", "Sign in first, then approve the app.")
	}
	if o := r.Header.Get("Origin"); o != "" && o != h.issuer {
		return web.Refuse(http.StatusForbidden, "bad_origin", "Approve the app on the Simple Host page itself.")
	}
	if q.Get("decision") != "approve" {
		return h.back(w, r, q, url.Values{"error": {"access_denied"}})
	}
	sc := q.Get("scope")
	if sc == "" {
		sc = scope
	}
	code := randomToken("shac_", 32)
	if _, err := h.a.DB.ExecContext(r.Context(), `INSERT INTO oauth_codes (code_hash, client_id, person_id, redirect_uri, code_challenge, scope, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`, access.Hash(code), q.Get("client_id"), who.ID, q.Get("redirect_uri"),
		q.Get("code_challenge"), sc, time.Now().Add(codeLife)); err != nil {
		return err
	}
	return h.back(w, r, q, url.Values{"code": {code}})
}

// token is route 59: a code or a refresh token for a fresh pair of tokens.
func (h *handler) token(w http.ResponseWriter, r *http.Request) error {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		return oauthError(w, http.StatusBadRequest, "invalid_request", "send a form body")
	}
	f := r.PostForm
	id, secret, basic := r.BasicAuth()
	if !basic {
		id, secret = f.Get("client_id"), f.Get("client_secret")
	}
	c, err := h.client(r.Context(), id)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if err != nil || c.secretHash.Valid && subtle.ConstantTimeCompare([]byte(access.Hash(secret)), []byte(c.secretHash.String)) != 1 {
		return oauthError(w, http.StatusUnauthorized, "invalid_client", "unknown client or wrong client secret")
	}
	switch f.Get("grant_type") {
	case "authorization_code":
		return h.redeem(w, r, c)
	case "refresh_token":
		return h.refresh(w, r, c)
	}
	return oauthError(w, http.StatusBadRequest, "unsupported_grant_type", "use authorization_code or refresh_token")
}

// redeem spends a code once, whatever happens next, then checks it.
func (h *handler) redeem(w http.ResponseWriter, r *http.Request, c client) error {
	f := r.PostForm
	var clientID, personID, redirect, challenge, sc string
	err := h.a.DB.QueryRowContext(r.Context(), `UPDATE oauth_codes SET used_at = now() WHERE code_hash = $1 AND used_at IS NULL AND expires_at > now()
		RETURNING client_id, person_id, redirect_uri, code_challenge, scope`, access.Hash(f.Get("code"))).Scan(&clientID, &personID, &redirect, &challenge, &sc)
	if err == sql.ErrNoRows {
		return oauthError(w, http.StatusBadRequest, "invalid_grant", "unknown, used or expired code")
	}
	if err != nil {
		return err
	}
	verifier := f.Get("code_verifier")
	sum := sha256.Sum256([]byte(verifier))
	pkce := base64.RawURLEncoding.EncodeToString(sum[:])
	switch {
	case clientID != c.id, redirect != f.Get("redirect_uri"):
		return oauthError(w, http.StatusBadRequest, "invalid_grant", "the code was issued to another app or address")
	case challenge != "" && subtle.ConstantTimeCompare([]byte(pkce), []byte(challenge)) != 1, challenge == "" && verifier != "":
		return oauthError(w, http.StatusBadRequest, "invalid_grant", "PKCE verification failed")
	}
	var grant string
	err = h.a.DB.QueryRowContext(r.Context(), `INSERT INTO oauth_grants (person_id, client_id, scope) VALUES ($1, $2, $3)
		ON CONFLICT (person_id, client_id) DO UPDATE SET scope = EXCLUDED.scope RETURNING id`, personID, c.id, sc).Scan(&grant)
	if err != nil {
		return err
	}
	return h.issue(w, r, grant, personID, sc)
}

// refresh spends a refresh token and issues a new pair.
func (h *handler) refresh(w http.ResponseWriter, r *http.Request, c client) error {
	var grant, personID, sc string
	err := h.a.DB.QueryRowContext(r.Context(), `DELETE FROM oauth_tokens t USING oauth_grants g
		WHERE t.hash = $1 AND t.kind = 'refresh' AND t.expires_at > now() AND g.id = t.grant_id AND g.client_id = $2
		RETURNING t.grant_id, t.person_id, g.scope`, access.Hash(r.PostForm.Get("refresh_token")), c.id).Scan(&grant, &personID, &sc)
	if err == sql.ErrNoRows {
		return oauthError(w, http.StatusBadRequest, "invalid_grant", "unknown or expired refresh token; connect the app again")
	}
	if err != nil {
		return err
	}
	return h.issue(w, r, grant, personID, sc)
}

func (h *handler) issue(w http.ResponseWriter, r *http.Request, grant, personID, sc string) error {
	at, rt := randomToken("shat_", 32), randomToken("shrt_", 32)
	now := time.Now()
	if _, err := h.a.DB.ExecContext(r.Context(), `INSERT INTO oauth_tokens (hash, kind, grant_id, person_id, expires_at)
		VALUES ($1, 'access', $3, $4, $5), ($2, 'refresh', $3, $4, $6)`, access.Hash(at), access.Hash(rt), grant, personID,
		now.Add(accessLife), now.Add(refreshLife)); err != nil {
		return err
	}
	w.Header().Set("Cache-Control", "no-store")
	web.JSON(w, http.StatusOK, map[string]any{"access_token": at, "token_type": "Bearer", "expires_in": int(accessLife.Seconds()),
		"refresh_token": rt, "scope": sc})
	return nil
}

// disconnect is route 13: the app's tokens stop at once.
func (h *handler) disconnect(w http.ResponseWriter, r *http.Request) error {
	c := web.CallerOf(r)
	if c.ID == "" {
		return web.Refuse(http.StatusUnauthorized, "sign_in", "Sign in to the dashboard first.")
	}
	res, err := h.a.DB.ExecContext(r.Context(), `DELETE FROM oauth_grants WHERE id::text = $1 AND person_id = $2`, r.PathValue("id"), c.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return web.NotFound
	}
	web.JSON(w, http.StatusOK, map[string]bool{"disconnected": true})
	return nil
}

// App is one connected app (for GET /v1/me).
type App struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Connected time.Time `json:"connected_at"`
}

// Apps is the person's connected apps, oldest first.
func Apps(ctx context.Context, q db.Q, personID string) ([]App, error) {
	rows, err := q.QueryContext(ctx, `SELECT g.id, c.name, g.created_at FROM oauth_grants g JOIN oauth_clients c ON c.id = g.client_id
		WHERE g.person_id = $1 ORDER BY g.created_at`, personID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []App{}
	for rows.Next() {
		var a App
		if err := rows.Scan(&a.ID, &a.Name, &a.Connected); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

var approvalPage = template.Must(template.New("approve").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Connect {{.App}}</title>
<style>
body { margin: 0; padding: 48px 16px; background: #fafafa; color: #1a1a1a; font: 16px/1.5 -apple-system, system-ui, sans-serif; }
main { max-width: 420px; margin: 0 auto; background: #fff; border: 1px solid #e5e5e5; border-radius: 12px; padding: 28px; }
h1 { font-size: 20px; margin: 0 0 12px; }
p.small { color: #555; font-size: 14px; }
button { font: inherit; padding: 10px 18px; border-radius: 8px; border: 1px solid #d0d0d0; background: #fff; color: #1a1a1a; cursor: pointer; margin-right: 8px; }
button.yes { background: #1a1a1a; color: #fff; border-color: #1a1a1a; }
</style></head><body><main>
<h1>Connect {{.App}}?</h1>
<p>{{.App}} will act as you ({{.Email}}) on Simple Host. It can do anything you can: publish, change and delete your sites and their data.</p>
<p class="small">It sends you back to {{.Host}}. You can disconnect it at any time in your dashboard.</p>
<form method="post" action="/oauth/authorize">
{{range $k, $v := .Fields}}<input type="hidden" name="{{$k}}" value="{{$v}}">
{{end}}<button class="yes" name="decision" value="approve">Connect</button><button name="decision" value="cancel">Cancel</button>
</form></main></body></html>
`))
