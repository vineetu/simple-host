package handler

// The Simple Host connector: an OAuth 2.1 authorization server and the remote
// MCP endpoint it protects, in this binary.
//
// A person adds https://<apex>/mcp once in ChatGPT, Claude or Grok. The app
// discovers this server (RFC 9728 → RFC 8414), registers itself (RFC 7591),
// and opens /oauth/authorize in a browser window. That page is the normal
// Simple Host sign-in (email code or Google) followed by one consent screen;
// "Allow" sends the app back with a single-use code, which it trades (with its
// PKCE verifier) for an access token and a rotating refresh token. Every later
// chat presents the access token on /mcp and is already signed in.
//
// The same tokens are accepted on the REST /v1 API (BearerAuth), so a ChatGPT
// GPT Action can call the API per person. A token carries exactly the power of
// that person's API key: it is converted to the key in process, and the REST
// layer does the rest, so nothing here restates a permission rule.
//
// Token audiences (RFC 8707): a token requested for <apex>/mcp works only at
// /mcp; a token requested with no resource, or for the apex itself, is for
// this whole server (/v1 and /mcp). Both are the same person and the same
// server, so neither can be replayed anywhere else.

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/vsriram/simple-host/internal/auth"
	db "github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/mcp"
)

const (
	oauthCodeTTL         = 60 * time.Second
	oauthAccessTTL       = time.Hour
	oauthRefreshTTL      = 90 * 24 * time.Hour
	oauthConsentTTL      = 30 * time.Minute
	oauthScope           = "sites"
	oauthMaxRedirectURIs = 10

	// Prefixes make a leaked token recognisable to secret scanners and to a
	// person reading a log, and let /v1 tell our token from a misplaced key.
	prefixAccess  = "shat_"
	prefixRefresh = "shrt_"
	prefixCode    = "shac_"
	prefixClient  = "shc_"
	prefixSecret  = "shcs_"
)

// ConnectorHandler serves the OAuth authorization server and /mcp.
type ConnectorHandler struct {
	database    *sql.DB
	issuer      string // e.g. https://simple-host.app
	mcpResource string // issuer + "/mcp"
	adminAPIKey string
	mcp         *mcp.Server

	// consentKey signs the CSRF token on the consent screen. Per process: a
	// restart invalidates consent pages that are open, which only means
	// clicking Allow again.
	consentKey []byte

	registerLimiter  *rateLimiter
	authorizeLimiter *rateLimiter
	tokenLimiter     *rateLimiter

	now func() time.Time
}

// NewConnectorHandler builds the connector. upstream is the bare application
// mux: MCP tool calls are served into it in process.
func NewConnectorHandler(database *sql.DB, publicBaseURL, adminAPIKey, siteDomain, contentHost, skillVersion string, upstream http.Handler) *ConnectorHandler {
	issuer := strings.TrimRight(publicBaseURL, "/")
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic("connector: no randomness: " + err.Error())
	}
	apiHost := publicBaseHost(publicBaseURL)
	if u, err := url.Parse(publicBaseURL); err == nil && u.Host != "" {
		apiHost = u.Host
	}
	if apiHost == "" {
		apiHost = siteDomain
	}
	h := &ConnectorHandler{
		database:    database,
		issuer:      issuer,
		mcpResource: issuer + "/mcp",
		adminAPIKey: adminAPIKey,
		consentKey:  key,
		// Registration: a chat app registers once per install, so a handful
		// an hour per address is plenty. Authorize and token: one person's
		// sign-in or a client's refresh loop, with room for retries.
		registerLimiter:  newRateLimiter(10, 10.0/3600),
		authorizeLimiter: newRateLimiter(30, 0.5),
		tokenLimiter:     newRateLimiter(30, 0.5),
		now:              time.Now,
	}
	h.mcp = mcp.NewServer(mcp.Config{
		Upstream:      upstream,
		APIHost:       apiHost,
		ContentOrigin: "https://" + contentHost,
		SkillVersion:  skillVersion,
		ServerName:    "simple-host",
		Version:       skillVersion,
		// An inline deploy carries the whole site in one message: the per-site
		// limit, plus JSON/base64 overhead.
		MaxBodyBytes: maxSiteArchiveSize*4/3 + (1 << 20),
	})
	return h
}

// StartSweep removes expired codes and tokens and abandoned registrations.
func (h *ConnectorHandler) StartSweep(every time.Duration) {
	h.registerLimiter.startCleanup(10*time.Minute, 2*time.Hour)
	h.authorizeLimiter.startCleanup(10*time.Minute, 30*time.Minute)
	h.tokenLimiter.startCleanup(10*time.Minute, 30*time.Minute)
	go func() {
		time.Sleep(45 * time.Second)
		for {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			if err := db.SweepOAuth(ctx, h.database); err != nil {
				log.Printf("connector sweep: %v", err)
			}
			cancel()
			time.Sleep(every)
		}
	}()
}

func (h *ConnectorHandler) Register(mux *http.ServeMux, authMiddleware func(http.Handler) http.Handler) {
	mux.HandleFunc("GET /.well-known/oauth-protected-resource", h.protectedResourceMetadata)
	mux.HandleFunc("GET /.well-known/oauth-protected-resource/mcp", h.protectedResourceMetadata)
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", h.authorizationServerMetadata)
	// Some clients derive the metadata address by inserting the MCP path.
	mux.HandleFunc("GET /.well-known/oauth-authorization-server/mcp", h.authorizationServerMetadata)

	mux.Handle("POST /oauth/register", rateLimitByIP(h.registerLimiter, http.HandlerFunc(h.register)))
	mux.Handle("GET /oauth/authorize", rateLimitByIP(h.authorizeLimiter, http.HandlerFunc(h.authorize)))
	mux.Handle("POST /oauth/authorize/decision", rateLimitByIP(h.authorizeLimiter, http.HandlerFunc(h.decide)))
	mux.Handle("POST /oauth/token", rateLimitByIP(h.tokenLimiter, http.HandlerFunc(h.token)))
	mux.Handle("POST /oauth/revoke", rateLimitByIP(h.tokenLimiter, http.HandlerFunc(h.revoke)))

	mux.HandleFunc("/mcp", h.serveMCP)

	mux.Handle("GET /v1/me/connections", authMiddleware(http.HandlerFunc(h.listConnections)))
	mux.Handle("DELETE /v1/me/connections/{client_id}", authMiddleware(http.HandlerFunc(h.deleteConnection)))
}

// ---- metadata ---------------------------------------------------------------

func (h *ConnectorHandler) protectedResourceMetadata(w http.ResponseWriter, r *http.Request) {
	resource := h.mcpResource
	if r.URL.Path == "/.well-known/oauth-protected-resource" {
		// The root document describes the whole server: tokens for it also
		// work on /v1. MCP clients read the /mcp-suffixed one.
		resource = h.issuer
	}
	w.Header().Set("Cache-Control", "public, max-age=3600")
	writeJSON(w, http.StatusOK, map[string]any{
		"resource":                 resource,
		"authorization_servers":    []string{h.issuer},
		"bearer_methods_supported": []string{"header"},
		"scopes_supported":         []string{oauthScope},
		"resource_name":            "Simple Host",
		"resource_documentation":   h.issuer + "/docs.html",
	})
}

func (h *ConnectorHandler) authorizationServerMetadata(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "public, max-age=3600")
	authMethods := []string{"none", "client_secret_post", "client_secret_basic"}
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                         h.issuer,
		"authorization_endpoint":                         h.issuer + "/oauth/authorize",
		"token_endpoint":                                 h.issuer + "/oauth/token",
		"registration_endpoint":                          h.issuer + "/oauth/register",
		"revocation_endpoint":                            h.issuer + "/oauth/revoke",
		"scopes_supported":                               []string{oauthScope},
		"response_types_supported":                       []string{"code"},
		"response_modes_supported":                       []string{"query"},
		"grant_types_supported":                          []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":               []string{"S256"},
		"token_endpoint_auth_methods_supported":          authMethods,
		"revocation_endpoint_auth_methods_supported":     authMethods,
		"authorization_response_iss_parameter_supported": true,
		"service_documentation":                          h.issuer + "/docs.html",
	})
}

// ---- helpers ------------------------------------------------------------------

func randomToken(prefix string, n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("connector: no randomness: " + err.Error())
	}
	return prefix + base64.RawURLEncoding.EncodeToString(b)
}

// hashSecret is how every connector secret is stored and looked up. The
// secrets are 256-bit random values, so a fast hash is the right one: there is
// nothing to brute-force.
func hashSecret(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func oauthError(w http.ResponseWriter, status int, code, description string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	body := map[string]string{"error": code}
	if description != "" {
		body["error_description"] = description
	}
	writeJSON(w, status, body)
}

// validRedirectURI is the registration rule: https anywhere, or http on a
// loopback address for an app running on the person's own machine. No
// fragment, no credentials in the URL.
func validRedirectURI(raw string) bool {
	if raw == "" || len(raw) > 2000 || strings.ContainsAny(raw, " \t\r\n") {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() || u.Host == "" || u.Opaque != "" || u.User != nil || u.Fragment != "" || strings.Contains(raw, "#") {
		return false
	}
	switch u.Scheme {
	case "https":
		return true
	case "http":
		host := u.Hostname()
		if host == "localhost" {
			return true
		}
		ip := net.ParseIP(host)
		return ip != nil && ip.IsLoopback()
	}
	return false
}

// cleanClientName makes a self-declared app name safe and short enough to
// show. It is only ever inserted as text, never as markup.
func cleanClientName(s string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(s) {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			continue
		}
		b.WriteRune(r)
	}
	out := strings.Join(strings.Fields(b.String()), " ")
	if r := []rune(out); len(r) > 60 {
		out = string(r[:60])
	}
	if out == "" {
		out = "An app"
	}
	return out
}

func validPKCEValue(s string) bool {
	if len(s) < 43 || len(s) > 128 {
		return false
	}
	for _, c := range s {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '.' || c == '_' || c == '~') {
			return false
		}
	}
	return true
}

// pkceS256 reports whether verifier matches challenge, in constant time.
func pkceS256(verifier, challenge string) bool {
	sum := sha256.Sum256([]byte(verifier))
	computed := base64.RawURLEncoding.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(computed), []byte(challenge)) == 1
}

// normalizeResource maps a requested resource to the audience stored on the
// grant: this server's /mcp, or the server as a whole. Anything else is not a
// resource this server serves.
func (h *ConnectorHandler) normalizeResource(raw string) (string, bool) {
	if raw == "" {
		return h.issuer, true
	}
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() || u.Fragment != "" || u.RawQuery != "" {
		return "", false
	}
	trimmed := strings.TrimRight(raw, "/")
	switch {
	case strings.EqualFold(trimmed, h.mcpResource):
		return h.mcpResource, true
	case strings.EqualFold(trimmed, h.issuer):
		return h.issuer, true
	}
	return "", false
}

// ---- dynamic client registration (RFC 7591) ------------------------------------

func (h *ConnectorHandler) register(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RedirectURIs            []string `json:"redirect_uris"`
		ClientName              string   `json:"client_name"`
		TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
		GrantTypes              []string `json:"grant_types"`
		ResponseTypes           []string `json:"response_types"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_client_metadata", "body must be a JSON client metadata document")
		return
	}
	if len(req.RedirectURIs) == 0 || len(req.RedirectURIs) > oauthMaxRedirectURIs {
		oauthError(w, http.StatusBadRequest, "invalid_redirect_uri", "between 1 and 10 redirect_uris are required")
		return
	}
	for _, u := range req.RedirectURIs {
		if !validRedirectURI(u) {
			oauthError(w, http.StatusBadRequest, "invalid_redirect_uri", "redirect URIs must be https, or http on a loopback address, with no fragment")
			return
		}
	}
	method := req.TokenEndpointAuthMethod
	if method == "" {
		method = "client_secret_basic" // RFC 7591 §2 default
	}
	if method != "none" && method != "client_secret_post" && method != "client_secret_basic" {
		oauthError(w, http.StatusBadRequest, "invalid_client_metadata", "token_endpoint_auth_method must be none, client_secret_post or client_secret_basic")
		return
	}
	for _, g := range req.GrantTypes {
		if g != "authorization_code" && g != "refresh_token" {
			oauthError(w, http.StatusBadRequest, "invalid_client_metadata", "only the authorization_code and refresh_token grants are supported")
			return
		}
	}
	for _, t := range req.ResponseTypes {
		if t != "code" {
			oauthError(w, http.StatusBadRequest, "invalid_client_metadata", "only the code response type is supported")
			return
		}
	}

	client := db.OAuthClient{
		ClientID:                randomToken(prefixClient, 16),
		Name:                    cleanClientName(req.ClientName),
		RedirectURIs:            req.RedirectURIs,
		TokenEndpointAuthMethod: method,
		PKCERequired:            true,
		Dynamic:                 true,
	}
	var secret string
	if method != "none" {
		secret = randomToken(prefixSecret, 32)
		client.SecretHash = sql.NullString{String: hashSecret(secret), Valid: true}
	}
	if err := db.InsertOAuthClient(r.Context(), h.database, client); err != nil {
		log.Printf("connector: register client: %v", err)
		oauthError(w, http.StatusInternalServerError, "server_error", "")
		return
	}
	resp := map[string]any{
		"client_id":                  client.ClientID,
		"client_id_issued_at":        h.now().Unix(),
		"client_name":                client.Name,
		"redirect_uris":              client.RedirectURIs,
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": method,
		"scope":                      oauthScope,
	}
	if secret != "" {
		resp["client_secret"] = secret
		resp["client_secret_expires_at"] = 0
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, resp)
}

// CreateOperatorClient registers a confidential client by hand (the
// `simple-host oauth-client create` command): a ChatGPT GPT Action, which is
// configured with a fixed client ID and secret rather than registering itself.
// The secret is returned once and stored only as a hash. pkceRequired=false is
// allowed only here, for platforms that cannot send PKCE.
func CreateOperatorClient(ctx context.Context, database *sql.DB, name string, redirectURIs []string, pkceRequired bool) (string, string, error) {
	for _, u := range redirectURIs {
		if !validRedirectURI(u) {
			return "", "", fmt.Errorf("invalid redirect URI %q: must be https (or http on localhost), with no fragment", u)
		}
	}
	if len(redirectURIs) > oauthMaxRedirectURIs {
		return "", "", fmt.Errorf("at most %d redirect URIs", oauthMaxRedirectURIs)
	}
	secret := randomToken(prefixSecret, 32)
	client := db.OAuthClient{
		ClientID:                randomToken(prefixClient, 16),
		SecretHash:              sql.NullString{String: hashSecret(secret), Valid: true},
		Name:                    cleanClientName(name),
		RedirectURIs:            append([]string{}, redirectURIs...),
		TokenEndpointAuthMethod: "client_secret_post",
		PKCERequired:            pkceRequired,
		Dynamic:                 false,
	}
	if client.RedirectURIs == nil {
		client.RedirectURIs = []string{}
	}
	if err := db.InsertOAuthClient(ctx, database, client); err != nil {
		return "", "", err
	}
	return client.ClientID, secret, nil
}

// SetOperatorClientRedirects replaces an operator client's redirect URIs.
func SetOperatorClientRedirects(ctx context.Context, database *sql.DB, clientID string, redirectURIs []string) error {
	for _, u := range redirectURIs {
		if !validRedirectURI(u) {
			return fmt.Errorf("invalid redirect URI %q: must be https (or http on localhost), with no fragment", u)
		}
	}
	if len(redirectURIs) > oauthMaxRedirectURIs {
		return fmt.Errorf("at most %d redirect URIs", oauthMaxRedirectURIs)
	}
	return db.SetOAuthClientRedirectURIs(ctx, database, clientID, redirectURIs)
}

// ---- authorization endpoint ------------------------------------------------------

// authzRequest is a validated authorization request.
type authzRequest struct {
	Client        db.OAuthClient
	RedirectURI   string
	State         string
	CodeChallenge string
	Resource      string
}

// authzError is a failure of an authorization request. With redirect set, it
// is reported to the app at its (validated) redirect URI; without, the person
// is shown it, because the redirect URI itself could not be trusted.
type authzError struct {
	redirect    bool
	code        string
	description string
}

func (h *ConnectorHandler) parseAuthorize(ctx context.Context, q url.Values) (authzRequest, *authzError) {
	var req authzRequest
	for k, v := range q {
		if len(v) > 1 && (k == "client_id" || k == "redirect_uri") {
			return req, &authzError{code: "invalid_request", description: "This sign-in link is not valid (a parameter is repeated)."}
		}
	}
	clientID := q.Get("client_id")
	if clientID == "" {
		return req, &authzError{code: "invalid_request", description: "This sign-in link is missing which app is asking. Go back to the app and try connecting again."}
	}
	client, err := db.GetOAuthClient(ctx, h.database, clientID)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			log.Printf("connector: get client: %v", err)
		}
		return req, &authzError{code: "invalid_client", description: "The app that sent you here is not known to Simple Host. Go back to the app and try connecting again."}
	}
	redirectURI := q.Get("redirect_uri")
	matched := false
	for _, registered := range client.RedirectURIs {
		if redirectURI != "" && subtle.ConstantTimeCompare([]byte(registered), []byte(redirectURI)) == 1 {
			matched = true
			break
		}
	}
	if !matched {
		return req, &authzError{code: "invalid_request", description: "The app asked to send you back to an address it did not register, so Simple Host stopped here. Go back to the app and try again."}
	}
	req.Client, req.RedirectURI = client, redirectURI

	// From here on the redirect URI is trusted, and errors go back to the app.
	for k, v := range q {
		if len(v) > 1 {
			return req, &authzError{redirect: true, code: "invalid_request", description: "parameter " + k + " is repeated"}
		}
	}
	if q.Get("response_type") != "code" {
		return req, &authzError{redirect: true, code: "unsupported_response_type", description: "response_type must be code"}
	}
	req.State = q.Get("state")
	if len(req.State) > 2000 {
		return req, &authzError{redirect: true, code: "invalid_request", description: "state is too long"}
	}
	challenge, method := q.Get("code_challenge"), q.Get("code_challenge_method")
	switch {
	case challenge == "" && method != "":
		return req, &authzError{redirect: true, code: "invalid_request", description: "code_challenge_method without code_challenge"}
	case challenge == "" && client.PKCERequired:
		return req, &authzError{redirect: true, code: "invalid_request", description: "PKCE is required: send code_challenge with code_challenge_method=S256"}
	case challenge != "" && method != "S256":
		return req, &authzError{redirect: true, code: "invalid_request", description: "only code_challenge_method=S256 is supported"}
	case challenge != "" && !validPKCEValue(challenge):
		return req, &authzError{redirect: true, code: "invalid_request", description: "code_challenge is malformed"}
	}
	req.CodeChallenge = challenge
	resource, ok := h.normalizeResource(q.Get("resource"))
	if !ok {
		return req, &authzError{redirect: true, code: "invalid_target", description: "resource must be " + h.mcpResource}
	}
	req.Resource = resource
	// scope: there is one, "sites", and it is always what is granted. Other
	// requested scopes are ignored rather than refused, so a client that
	// asks for something generic still connects; it can do no more than a
	// "sites" token does.
	return req, nil
}

// redirectWith builds the redirect back to the app, keeping any query the
// registered URI already had.
func (h *ConnectorHandler) redirectWith(redirectURI string, params map[string]string) string {
	u, err := url.Parse(redirectURI)
	if err != nil {
		return redirectURI
	}
	q := u.Query()
	for k, v := range params {
		if v != "" {
			q.Set(k, v)
		}
	}
	q.Set("iss", h.issuer)
	u.RawQuery = q.Encode()
	return u.String()
}

func (h *ConnectorHandler) errorRedirect(req authzRequest, e *authzError) string {
	return h.redirectWith(req.RedirectURI, map[string]string{"error": e.code, "error_description": e.description, "state": req.State})
}

// consentCSRF binds a consent decision to the page this server rendered for
// the same request, within oauthConsentTTL. The decision is also authenticated
// with the person's API key in a header, which a cross-site page cannot send.
func (h *ConnectorHandler) consentCSRF(req authzRequest, issued int64) string {
	mac := hmac.New(sha256.New, h.consentKey)
	for _, part := range []string{req.Client.ClientID, req.RedirectURI, req.CodeChallenge, req.State, req.Resource, strconv.FormatInt(issued, 10)} {
		mac.Write([]byte(part))
		mac.Write([]byte{0})
	}
	return strconv.FormatInt(issued, 10) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (h *ConnectorHandler) checkConsentCSRF(req authzRequest, token string) bool {
	issuedStr, _, ok := strings.Cut(token, ".")
	if !ok {
		return false
	}
	issued, err := strconv.ParseInt(issuedStr, 10, 64)
	if err != nil {
		return false
	}
	age := h.now().Unix() - issued
	if age < 0 || age > int64(oauthConsentTTL/time.Second) {
		return false
	}
	return hmac.Equal([]byte(h.consentCSRF(req, issued)), []byte(token))
}

// consentHeaders: the consent screen is never framed (clickjacking an Allow
// button is the attack), never cached, and leaks nothing in a Referer.
func consentHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; "+
		"img-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
}

func (h *ConnectorHandler) authorize(w http.ResponseWriter, r *http.Request) {
	req, aerr := h.parseAuthorize(r.Context(), r.URL.Query())
	if aerr != nil && aerr.redirect {
		w.Header().Set("Cache-Control", "no-store")
		http.Redirect(w, r, h.errorRedirect(req, aerr), http.StatusFound)
		return
	}
	data := map[string]any{"ok": aerr == nil}
	status := http.StatusOK
	if aerr != nil {
		data["error"] = aerr.description
		status = http.StatusBadRequest
	} else {
		host := req.RedirectURI
		if u, err := url.Parse(req.RedirectURI); err == nil {
			host = u.Hostname()
		}
		data["client_name"] = req.Client.Name
		data["redirect_host"] = host
		data["csrf"] = h.consentCSRF(req, h.now().Unix())
	}
	h.renderConsent(w, r, status, data)
}

func (h *ConnectorHandler) renderConsent(w http.ResponseWriter, r *http.Request, status int, data map[string]any) {
	page, err := chromePage("connect.html", chromeDataFor(r, ""))
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	// json.Marshal escapes <, > and & so the data cannot close the script.
	payload, _ := json.Marshal(data)
	page = bytes.Replace(page, []byte("<!--sh:connect-data-->"),
		append(append([]byte(`<script type="application/json" id="connect-data">`), payload...), []byte(`</script>`)...), 1)
	consentHeaders(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(page)
}

// resolveConsentUser authenticates the person deciding, by the API key the
// sign-in on this page (or the dashboard) left in the browser.
func (h *ConnectorHandler) resolveConsentUser(ctx context.Context, key string) (db.User, int, string) {
	if key == "" {
		return db.User{}, http.StatusUnauthorized, "sign in first"
	}
	if subtle.ConstantTimeCompare([]byte(key), []byte(h.adminAPIKey)) == 1 {
		return db.User{}, http.StatusForbidden, "The instance admin key cannot connect apps. Sign in with a personal account."
	}
	user, err := db.GetUserByAPIKey(ctx, h.database, key)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return db.User{}, http.StatusUnauthorized, "sign in again"
		}
		return db.User{}, http.StatusInternalServerError, "internal server error"
	}
	if user.IsAdmin {
		return db.User{}, http.StatusForbidden, "Admin accounts cannot connect apps. Sign in with a personal account."
	}
	return user, 0, ""
}

// decide records Allow or Cancel from the consent screen and returns where to
// send the browser. It is the only place an authorization code is minted.
func (h *ConnectorHandler) decide(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	// Same-origin only. The API key header already rules out a cross-site
	// form, and this rules out a cross-site script as a second fence.
	if o := r.Header.Get("Origin"); o != "" && !strings.EqualFold(strings.TrimRight(o, "/"), h.issuer) {
		writeJSON(w, http.StatusForbidden, errorResponse{Error: "cross-origin request refused"})
		return
	}
	if s := r.Header.Get("Sec-Fetch-Site"); s != "" && s != "same-origin" {
		writeJSON(w, http.StatusForbidden, errorResponse{Error: "cross-site request refused"})
		return
	}
	var body struct {
		Query    string `json:"query"`
		CSRF     string `json:"csrf"`
		Decision string `json:"decision"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}
	q, err := url.ParseQuery(strings.TrimPrefix(body.Query, "?"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request"})
		return
	}
	q.Del("token") // the sign-in link token, if the page still carried it
	req, aerr := h.parseAuthorize(r.Context(), q)
	if aerr != nil && !aerr.redirect {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: aerr.description})
		return
	}
	if !h.checkConsentCSRF(req, body.CSRF) {
		writeJSON(w, http.StatusForbidden, errorResponse{Error: "This page has expired. Go back to the app and connect again."})
		return
	}
	if aerr != nil {
		writeJSON(w, http.StatusOK, map[string]string{"redirect_to": h.errorRedirect(req, aerr)})
		return
	}
	switch body.Decision {
	case "deny":
		writeJSON(w, http.StatusOK, map[string]string{"redirect_to": h.errorRedirect(req, &authzError{code: "access_denied", description: "The person cancelled."})})
		return
	case "allow":
	default:
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "decision must be allow or deny"})
		return
	}
	user, status, msg := h.resolveConsentUser(r.Context(), r.Header.Get("X-API-Key"))
	if status != 0 {
		writeJSON(w, status, errorResponse{Error: msg})
		return
	}
	code := randomToken(prefixCode, 32)
	if err := db.InsertOAuthCode(r.Context(), h.database, hashSecret(code), db.OAuthCode{
		ClientID:      req.Client.ClientID,
		UserID:        user.ID,
		RedirectURI:   req.RedirectURI,
		CodeChallenge: req.CodeChallenge,
		Scope:         oauthScope,
		Resource:      req.Resource,
		ExpiresAt:     h.now().Add(oauthCodeTTL),
	}); err != nil {
		log.Printf("connector: insert code: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	log.Printf("connector: consent user_id=%s client_id=%s", user.ID, req.Client.ClientID)
	writeJSON(w, http.StatusOK, map[string]string{"redirect_to": h.redirectWith(req.RedirectURI, map[string]string{"code": code, "state": req.State})})
}

// ---- token endpoint ----------------------------------------------------------------

// authenticateClient applies RFC 6749 §2.3 to a token or revocation request.
// A confidential client must prove its secret (Basic or in the body); a
// public client identifies itself by client_id alone and is held to PKCE.
func (h *ConnectorHandler) authenticateClient(r *http.Request) (db.OAuthClient, bool, string) {
	formID, formSecret := r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
	basicID, basicSecret, hasBasic := r.BasicAuth()
	if hasBasic {
		// Basic credentials are form-encoded before base64 (RFC 6749 §2.3.1).
		if v, err := url.QueryUnescape(basicID); err == nil {
			basicID = v
		}
		if v, err := url.QueryUnescape(basicSecret); err == nil {
			basicSecret = v
		}
		if formSecret != "" || (formID != "" && formID != basicID) {
			return db.OAuthClient{}, false, "use one client authentication method"
		}
		formID, formSecret = basicID, basicSecret
	}
	if formID == "" {
		return db.OAuthClient{}, false, "client_id is required"
	}
	client, err := db.GetOAuthClient(r.Context(), h.database, formID)
	if err != nil {
		// Hash anyway so an unknown client costs what a known one does.
		_ = hashSecret(formSecret)
		return db.OAuthClient{}, false, "unknown client"
	}
	if client.SecretHash.Valid {
		if formSecret == "" || subtle.ConstantTimeCompare([]byte(hashSecret(formSecret)), []byte(client.SecretHash.String)) != 1 {
			return db.OAuthClient{}, false, "client authentication failed"
		}
	}
	return client, true, ""
}

func (h *ConnectorHandler) token(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_request", "body must be application/x-www-form-urlencoded")
		return
	}
	for k, v := range r.PostForm {
		if len(v) > 1 {
			oauthError(w, http.StatusBadRequest, "invalid_request", "parameter "+k+" is repeated")
			return
		}
	}
	client, ok, why := h.authenticateClient(r)
	if !ok {
		if _, _, basic := r.BasicAuth(); basic {
			w.Header().Set("WWW-Authenticate", `Basic realm="simple-host"`)
		}
		oauthError(w, http.StatusUnauthorized, "invalid_client", why)
		return
	}
	_ = db.TouchOAuthClient(r.Context(), h.database, client.ClientID)
	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		h.redeemCode(w, r, client)
	case "refresh_token":
		h.refresh(w, r, client)
	case "":
		oauthError(w, http.StatusBadRequest, "invalid_request", "grant_type is required")
	default:
		oauthError(w, http.StatusBadRequest, "unsupported_grant_type", "")
	}
}

func (h *ConnectorHandler) redeemCode(w http.ResponseWriter, r *http.Request, client db.OAuthClient) {
	code := r.PostForm.Get("code")
	if code == "" || !strings.HasPrefix(code, prefixCode) {
		oauthError(w, http.StatusBadRequest, "invalid_grant", "unknown code")
		return
	}
	codeHash := hashSecret(code)
	tx, err := h.database.BeginTx(r.Context(), nil)
	if err != nil {
		oauthError(w, http.StatusInternalServerError, "server_error", "")
		return
	}
	defer tx.Rollback()

	stored, err := db.ConsumeOAuthCode(r.Context(), tx, codeHash)
	if errors.Is(err, db.ErrOAuthCodeUsed) {
		// A code presented twice was intercepted or replayed. Whatever the
		// first redemption issued is revoked (RFC 6749 §4.1.2).
		_ = tx.Rollback()
		if stored.GrantID.Valid {
			if derr := db.DeleteOAuthGrant(r.Context(), h.database, stored.GrantID.String); derr != nil {
				log.Printf("connector: revoke on code reuse: %v", derr)
			}
		}
		log.Printf("connector: authorization code reused client_id=%s; grant revoked", stored.ClientID)
		oauthError(w, http.StatusBadRequest, "invalid_grant", "code already used")
		return
	}
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			log.Printf("connector: consume code: %v", err)
			oauthError(w, http.StatusInternalServerError, "server_error", "")
			return
		}
		oauthError(w, http.StatusBadRequest, "invalid_grant", "unknown code")
		return
	}
	// The code is spent from here on, whatever happens next: every failure
	// below commits the spend, so a failed guess cannot be retried.
	fail := func(desc string) {
		_ = tx.Commit()
		oauthError(w, http.StatusBadRequest, "invalid_grant", desc)
	}
	if stored.ClientID != client.ClientID {
		fail("code was issued to another client")
		return
	}
	if h.now().After(stored.ExpiresAt) {
		fail("code expired")
		return
	}
	if r.PostForm.Get("redirect_uri") != stored.RedirectURI {
		fail("redirect_uri does not match the authorization request")
		return
	}
	verifier := r.PostForm.Get("code_verifier")
	if stored.CodeChallenge != "" {
		if !validPKCEValue(verifier) || !pkceS256(verifier, stored.CodeChallenge) {
			fail("PKCE verification failed")
			return
		}
	} else if verifier != "" {
		// A verifier for a code issued without a challenge is a downgrade
		// attempt (RFC 9700 §2.1.1).
		fail("code_verifier sent for a code issued without code_challenge")
		return
	}
	if res := r.PostForm.Get("resource"); res != "" {
		if norm, ok := h.normalizeResource(res); !ok || norm != stored.Resource {
			_ = tx.Commit()
			oauthError(w, http.StatusBadRequest, "invalid_target", "resource does not match the authorization request")
			return
		}
	}
	user, err := db.GetUserByID(r.Context(), tx, stored.UserID)
	if err != nil || user.IsAdmin {
		fail("account unavailable")
		return
	}
	grantID, err := db.InsertOAuthGrant(r.Context(), tx, stored.UserID, client.ClientID, stored.Scope, stored.Resource)
	if err != nil {
		log.Printf("connector: insert grant: %v", err)
		oauthError(w, http.StatusInternalServerError, "server_error", "")
		return
	}
	if err := db.SetOAuthCodeGrant(r.Context(), tx, codeHash, grantID); err != nil {
		oauthError(w, http.StatusInternalServerError, "server_error", "")
		return
	}
	h.issueTokens(w, r, tx, grantID, stored.Scope)
}

func (h *ConnectorHandler) refresh(w http.ResponseWriter, r *http.Request, client db.OAuthClient) {
	presented := r.PostForm.Get("refresh_token")
	if !strings.HasPrefix(presented, prefixRefresh) {
		oauthError(w, http.StatusBadRequest, "invalid_grant", "unknown refresh token")
		return
	}
	tokenHash := hashSecret(presented)
	tx, err := h.database.BeginTx(r.Context(), nil)
	if err != nil {
		oauthError(w, http.StatusInternalServerError, "server_error", "")
		return
	}
	defer tx.Rollback()
	tok, err := db.GetOAuthToken(r.Context(), tx, tokenHash)
	if err != nil || tok.Kind != "refresh" {
		oauthError(w, http.StatusBadRequest, "invalid_grant", "unknown refresh token")
		return
	}
	if tok.ClientID != client.ClientID {
		oauthError(w, http.StatusBadRequest, "invalid_grant", "refresh token was issued to another client")
		return
	}
	if h.now().After(tok.ExpiresAt) {
		oauthError(w, http.StatusBadRequest, "invalid_grant", "refresh token expired")
		return
	}
	fresh, err := db.MarkRefreshTokenUsed(r.Context(), tx, tokenHash)
	if err != nil {
		oauthError(w, http.StatusInternalServerError, "server_error", "")
		return
	}
	if !fresh {
		// A rotated-out refresh token came back: either the client or a thief
		// holds a copy. The whole family goes, so the thief's copy dies too
		// (OAuth 2.1 §4.3.1). The person reconnects once.
		_ = tx.Rollback()
		if derr := db.DeleteOAuthGrant(r.Context(), h.database, tok.GrantID); derr != nil {
			log.Printf("connector: revoke on refresh reuse: %v", derr)
		}
		log.Printf("connector: refresh token reused client_id=%s user_id=%s; connection revoked", tok.ClientID, tok.UserID)
		oauthError(w, http.StatusBadRequest, "invalid_grant", "refresh token already used; the connection was revoked")
		return
	}
	if scope := r.PostForm.Get("scope"); scope != "" {
		for _, s := range strings.Fields(scope) {
			if s != tok.Scope {
				oauthError(w, http.StatusBadRequest, "invalid_scope", "scope exceeds the original grant")
				return
			}
		}
	}
	if res := r.PostForm.Get("resource"); res != "" {
		if norm, ok := h.normalizeResource(res); !ok || norm != tok.Resource {
			oauthError(w, http.StatusBadRequest, "invalid_target", "resource does not match the original grant")
			return
		}
	}
	user, err := db.GetUserByID(r.Context(), tx, tok.UserID)
	if err != nil || user.IsAdmin {
		oauthError(w, http.StatusBadRequest, "invalid_grant", "account unavailable")
		return
	}
	_ = db.TouchOAuthGrant(r.Context(), tx, tok.GrantID)
	h.issueTokens(w, r, tx, tok.GrantID, tok.Scope)
}

// issueTokens mints an access token and a refresh token in grantID, commits
// the transaction, and writes the token response.
func (h *ConnectorHandler) issueTokens(w http.ResponseWriter, r *http.Request, tx *sql.Tx, grantID, scope string) {
	access := randomToken(prefixAccess, 32)
	refresh := randomToken(prefixRefresh, 32)
	now := h.now()
	if err := db.InsertOAuthToken(r.Context(), tx, hashSecret(access), grantID, "access", now.Add(oauthAccessTTL)); err != nil {
		log.Printf("connector: insert access token: %v", err)
		oauthError(w, http.StatusInternalServerError, "server_error", "")
		return
	}
	if err := db.InsertOAuthToken(r.Context(), tx, hashSecret(refresh), grantID, "refresh", now.Add(oauthRefreshTTL)); err != nil {
		log.Printf("connector: insert refresh token: %v", err)
		oauthError(w, http.StatusInternalServerError, "server_error", "")
		return
	}
	if err := tx.Commit(); err != nil {
		oauthError(w, http.StatusInternalServerError, "server_error", "")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token":  access,
		"token_type":    "Bearer",
		"expires_in":    int(oauthAccessTTL / time.Second),
		"refresh_token": refresh,
		"scope":         scope,
	})
}

// revoke is RFC 7009. It answers 200 whether or not the token was known, so it
// cannot be used to test tokens.
func (h *ConnectorHandler) revoke(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_request", "")
		return
	}
	client, ok, why := h.authenticateClient(r)
	if !ok {
		oauthError(w, http.StatusUnauthorized, "invalid_client", why)
		return
	}
	token := r.PostForm.Get("token")
	w.Header().Set("Cache-Control", "no-store")
	if token == "" {
		oauthError(w, http.StatusBadRequest, "invalid_request", "token is required")
		return
	}
	tokenHash := hashSecret(token)
	if tok, err := db.GetOAuthToken(r.Context(), h.database, tokenHash); err == nil && tok.ClientID == client.ClientID {
		if tok.Kind == "refresh" {
			err = db.DeleteOAuthGrant(r.Context(), h.database, tok.GrantID)
		} else {
			err = db.DeleteOAuthToken(r.Context(), h.database, tokenHash)
		}
		if err != nil {
			log.Printf("connector: revoke: %v", err)
			oauthError(w, http.StatusServiceUnavailable, "temporarily_unavailable", "")
			return
		}
	}
	w.WriteHeader(http.StatusOK)
}

// ---- bearer tokens as identity -------------------------------------------------------

// userForAccessToken resolves an access token to its person. audiences lists
// the grant resources acceptable where it is being presented.
func (h *ConnectorHandler) userForAccessToken(ctx context.Context, token string, audiences ...string) (db.User, bool) {
	tok, err := db.GetOAuthToken(ctx, h.database, hashSecret(token))
	if err != nil || tok.Kind != "access" || h.now().After(tok.ExpiresAt) {
		return db.User{}, false
	}
	audienceOK := false
	for _, a := range audiences {
		if tok.Resource == a {
			audienceOK = true
		}
	}
	if !audienceOK {
		return db.User{}, false
	}
	user, err := db.GetUserByID(ctx, h.database, tok.UserID)
	if err != nil || user.IsAdmin || user.APIKey == "" {
		return db.User{}, false
	}
	_ = db.TouchOAuthGrant(ctx, h.database, tok.GrantID)
	return user, true
}

func bearerToken(r *http.Request) (string, bool) {
	authz := r.Header.Get("Authorization")
	if len(authz) < 7 || !strings.EqualFold(authz[:7], "bearer ") {
		return "", false
	}
	return strings.TrimSpace(authz[7:]), true
}

// BearerAuth lets a connector access token stand in for the person's API key
// on the REST API. The token is exchanged for the key in process, so the
// request then meets exactly the checks, limits and permissions a request
// with that key meets. A bearer value that is not one of this server's
// tokens is left alone (the key middleware then explains X-API-Key).
func (h *ConnectorHandler) BearerAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v1/") || r.Header.Get("X-API-Key") != "" {
			next.ServeHTTP(w, r)
			return
		}
		token, ok := bearerToken(r)
		if !ok || !strings.HasPrefix(token, prefixAccess) {
			next.ServeHTTP(w, r)
			return
		}
		user, ok := h.userForAccessToken(r.Context(), token, h.issuer)
		if !ok {
			w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token", resource_metadata="`+h.issuer+`/.well-known/oauth-protected-resource"`)
			writeJSON(w, http.StatusUnauthorized, map[string]string{
				"error": "the access token is invalid, expired or revoked; refresh it or reconnect",
				"code":  "invalid_token",
			})
			return
		}
		r2 := r.Clone(r.Context())
		r2.Header.Del("Authorization")
		r2.Header.Set("X-API-Key", user.APIKey)
		next.ServeHTTP(w, r2)
	})
}

// ---- /mcp -------------------------------------------------------------------------------

func (h *ConnectorHandler) mcpUnauthorized(w http.ResponseWriter, presented bool) {
	challenge := `Bearer resource_metadata="` + h.issuer + `/.well-known/oauth-protected-resource/mcp", scope="` + oauthScope + `"`
	if presented {
		challenge = `Bearer error="invalid_token", error_description="The access token is invalid, expired or revoked", resource_metadata="` +
			h.issuer + `/.well-known/oauth-protected-resource/mcp", scope="` + oauthScope + `"`
	}
	w.Header().Set("WWW-Authenticate", challenge)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "sign in to Simple Host to use this connector"})
}

func (h *ConnectorHandler) serveMCP(w http.ResponseWriter, r *http.Request) {
	var apiKey string
	if token, ok := bearerToken(r); ok {
		user, valid := h.userForAccessToken(r.Context(), token, h.mcpResource, h.issuer)
		if !valid {
			h.mcpUnauthorized(w, true)
			return
		}
		apiKey = user.APIKey
	} else if key := r.Header.Get("X-API-Key"); key != "" {
		// Coding agents that already hold a key can use the endpoint too.
		if subtle.ConstantTimeCompare([]byte(key), []byte(h.adminAPIKey)) != 1 {
			if _, err := db.GetUserByAPIKey(r.Context(), h.database, key); err != nil {
				h.mcpUnauthorized(w, true)
				return
			}
		}
		apiKey = key
	} else {
		h.mcpUnauthorized(w, false)
		return
	}
	h.mcp.ServeHTTP(w, r.WithContext(mcp.WithCaller(r.Context(), mcp.Caller{APIKey: apiKey})))
}

// ---- connected apps --------------------------------------------------------------------

func (h *ConnectorHandler) listConnections(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
		return
	}
	conns, err := db.ListOAuthConnections(r.Context(), h.database, user.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	out := make([]map[string]any, 0, len(conns))
	for _, c := range conns {
		out = append(out, map[string]any{
			"client_id":    c.ClientID,
			"name":         c.ClientName,
			"connected_at": c.ConnectedAt,
			"last_used_at": c.LastUsedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"connections": out})
}

func (h *ConnectorHandler) deleteConnection(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
		return
	}
	n, err := db.DeleteOAuthGrantsForClient(r.Context(), h.database, user.ID, r.PathValue("client_id"))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	if n == 0 {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "no connected app with that id"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
