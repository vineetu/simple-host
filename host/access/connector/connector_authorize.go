package connector

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/vsriram/simple-host/host/access/auth"
	authrules "github.com/vsriram/simple-host/host/access/rules"

	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
)

// ---- authorization endpoint ------------------------------------------------------

// authzRequest is a validated authorization request.
type authzRequest struct {
	Client        model.OAuthClient
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
	// museHelp: the client ID was not one, most likely a person typing
	// something else into Meta Muse's Client ID box. The page says what to paste.
	museHelp bool
	// museTypo: the client ID is one of ours mangled by a keyboard or
	// autocorrect (museKeyboardMangled) rather than something unrelated typed
	// in by hand. The page names the cause.
	museTypo bool
}

// parseAuthorize validates an authorization request in two halves: until the
// client and its redirect URI are trusted, errors are shown to the person;
// after, they go back to the app with its state.
func (h *Handler) parseAuthorize(ctx context.Context, q url.Values) (authzRequest, *authzError) {
	var req authzRequest
	client, redirectURI, aerr := h.authorizeClient(ctx, q)
	if aerr != nil {
		return req, aerr
	}
	req.Client, req.RedirectURI = client, redirectURI
	// From here on the redirect URI is trusted, and errors go back to the app.
	if len(q["state"]) == 1 && len(q.Get("state")) <= 2000 {
		req.State = q.Get("state")
	}
	for k, v := range q {
		if len(v) > 1 {
			return req, &authzError{redirect: true, code: "invalid_request", description: "parameter " + k + " is repeated"}
		}
	}
	// state first, so every error below carries it back to the app.
	req.State = q.Get("state")
	if len(req.State) > 2000 {
		req.State = ""
		return req, &authzError{redirect: true, code: "invalid_request", description: "state is too long"}
	}
	if q.Get("response_type") != "code" {
		return req, &authzError{redirect: true, code: "unsupported_response_type", description: "response_type must be code"}
	}
	challenge := q.Get("code_challenge")
	if problem := pkceProblem(challenge, q.Get("code_challenge_method"), client.PKCERequired); problem != "" {
		return req, &authzError{redirect: true, code: "invalid_request", description: problem}
	}
	req.CodeChallenge = challenge
	resource, ok := h.normalizeResource(q.Get("resource"))
	if !ok {
		return req, &authzError{redirect: true, code: "invalid_target", description: "resource must be " + h.mcpResource}
	}
	req.Resource = resource
	// scope: requested scopes are ignored rather than refused, so a client
	// that asks for something generic still reaches the consent page. What
	// is granted is decided there: "sites" on an ordinary instance; on the
	// hosted hackathon platform, "sites events" for one personal connection.
	return req, nil
}

// authorizeClient is the registered client the request names and the
// redirect URI it registered that the request asks for.
func (h *Handler) authorizeClient(ctx context.Context, q url.Values) (model.OAuthClient, string, *authzError) {
	if len(q["client_id"]) > 1 || len(q["redirect_uri"]) > 1 {
		return model.OAuthClient{}, "", &authzError{code: "invalid_request", description: "This sign-in link is not valid (a parameter is repeated)."}
	}
	clientID := q.Get("client_id")
	if clientID == "" {
		return model.OAuthClient{}, "", &authzError{code: "invalid_request", description: "This sign-in link is missing which app is asking. Go back to the app and try connecting again."}
	}
	client, err := GetOAuthClient(ctx, h.database, clientID)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			log.Printf("connector: get client: %v", err)
		}
		return client, "", unknownClientError(clientID, q.Get("redirect_uri"))
	}
	redirectURI := q.Get("redirect_uri")
	if !registeredRedirect(client, redirectURI) {
		return client, "", &authzError{code: "invalid_request", description: "The app asked to send you back to an address it did not register, so Simple Host stopped here. Go back to the app and try again."}
	}
	return client, redirectURI, nil
}

// unknownClientError is the page for a client ID Simple Host does not know.
// The redirect URI tells us which app it was; the client ID itself may be
// anything the person typed (an email), so only its shape is logged.
func unknownClientError(clientID, redirect string) *authzError {
	museHelp := redirect == MuseRedirectURI || !(strings.HasPrefix(clientID, PrefixClient) || strings.Contains(clientID, "://"))
	log.Printf("connector: unknown client_id (%s) redirect_uri=%q", clientIDShape(clientID), truncate(redirect, 300))
	if museHelp {
		return &authzError{code: "invalid_client", museHelp: true, museTypo: museKeyboardMangled(clientID), description: "The Client ID entered in the app is not one Simple Host knows."}
	}
	return &authzError{code: "invalid_client", description: "The app that sent you here is not known to Simple Host. Go back to the app and try connecting again."}
}

// registeredRedirect: redirectURI is one the client registered (a loopback
// one on any port).
func registeredRedirect(client model.OAuthClient, redirectURI string) bool {
	if redirectURI == "" {
		return false
	}
	for _, registered := range client.RedirectURIs {
		if subtle.ConstantTimeCompare([]byte(registered), []byte(redirectURI)) == 1 || loopbackPortMatch(registered, redirectURI) {
			return true
		}
	}
	return false
}

// pkceProblem is what is wrong with a request's PKCE challenge, "" when
// nothing is: only S256, and required for clients that must use it.
func pkceProblem(challenge, method string, required bool) string {
	switch {
	case challenge == "" && method != "":
		return "code_challenge_method without code_challenge"
	case challenge == "" && required:
		return "PKCE is required: send code_challenge with code_challenge_method=S256"
	case challenge != "" && method != "S256":
		return "only code_challenge_method=S256 is supported"
	case challenge != "" && !ValidPKCEValue(challenge):
		return "code_challenge is malformed"
	}
	return ""
}

// scopesSupported is the scope list published in discovery. Hosted adds
// events, the marker of an event-management grant. The challenge itself
// stays "sites": clients already ask for that, and the consent page chooses.
func (h *Handler) scopesSupported() []string {
	if h.teamConnections {
		return []string{authrules.OAuthScope, authrules.GroupManagementScope}
	}
	return []string{authrules.OAuthScope}
}

// redirectWith builds the redirect back to the app, keeping any query the
// registered URI already had.
func (h *Handler) redirectWith(redirectURI string, params map[string]string) string {
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
	q.Set("iss", h.Issuer)
	u.RawQuery = q.Encode()
	return u.String()
}

func (h *Handler) errorRedirect(req authzRequest, e *authzError) string {
	return h.redirectWith(req.RedirectURI, map[string]string{"error": e.code, "error_description": e.description, "state": req.State})
}

// consentCSRF binds a consent decision to the page this server rendered for
// the same request, within oauthConsentTTL. The decision is also authenticated
// with the person's API key in a header, which a cross-site page cannot send.
func (h *Handler) consentCSRF(req authzRequest, issued int64) string {
	mac := hmac.New(sha256.New, h.consentKey)
	for _, part := range []string{req.Client.ClientID, req.RedirectURI, req.CodeChallenge, req.State, req.Resource, strconv.FormatInt(issued, 10)} {
		mac.Write([]byte(part))
		mac.Write([]byte{0})
	}
	return strconv.FormatInt(issued, 10) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (h *Handler) checkConsentCSRF(req authzRequest, token string) bool {
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
// consentHeaders sets the consent page's own policy; nonce is the one its
// inline scripts were stamped with (the page reads the API key, so no
// 'unsafe-inline' for scripts).
func consentHeaders(w http.ResponseWriter, nonce string) {
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'nonce-"+nonce+"'; style-src 'self' 'unsafe-inline'; "+
		"img-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
}

func (h *Handler) authorize(w http.ResponseWriter, r *http.Request) {
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
		if aerr.museHelp {
			data["muse_client_id"] = MuseShortClientID
			data["mcp_url"] = h.mcpResource
			if aerr.museTypo {
				data["muse_typo"] = true
			}
		}
		status = http.StatusBadRequest
	} else {
		data["client_name"] = req.Client.Name
		data["redirect_host"] = redirectTarget(req.RedirectURI)
		data["csrf"] = h.consentCSRF(req, h.now().Unix())
		if h.reviewer != nil {
			data["reviewer_signin"] = true
		}
		if h.teamConnections {
			data["hack"] = true
		}
		// This install's own host, named on the sign-in step and the Allow
		// page on a self-hosted instance only (SIM connect-mcp rule 4):
		// hosted text already says Simple Host/Simple Hack and stays as it
		// is, so host/self_hosted are only emitted on self-hosted -- hosted's
		// consent HTML/JSON must stay byte-identical to the baseline.
		if h.pipeline().HostedOwner() == "" {
			data["self_hosted"] = true
			data["host"] = r.Host
		}
	}
	h.renderConsent(w, r, status, data)
}

func (h *Handler) renderConsent(w http.ResponseWriter, r *http.Request, status int, data map[string]any) {
	page, err := h.pipeline().BuildChromePage("connect.html", h.pipeline().ChromeDataFor(r, ""))
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	var nb [16]byte
	if _, err := rand.Read(nb[:]); err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	nonce := base64.StdEncoding.EncodeToString(nb[:])
	page = bytes.ReplaceAll(page, []byte("<script>"), []byte(`<script nonce="`+nonce+`">`))
	// json.Marshal escapes <, > and & so the data cannot close the script.
	payload, _ := json.Marshal(data)
	page = bytes.Replace(page, []byte("<!--sh:connect-data-->"),
		append(append([]byte(`<script type="application/json" id="connect-data">`), payload...), []byte(`</script>`)...), 1)
	consentHeaders(w, nonce)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(page)
}

// SetTeamConnections turns on personal grants that select a team. The
// product that has teams calls it at startup.
func (h *Handler) SetTeamConnections(on bool) { h.teamConnections = on }

// resolveConsentUser authenticates the person deciding, by the API key the
// sign-in on this page (or the dashboard) left in the browser.
func (h *Handler) resolveConsentUser(ctx context.Context, key string) (model.User, int, string) {
	if key == "" {
		return model.User{}, http.StatusUnauthorized, "sign in first"
	}
	if subtle.ConstantTimeCompare([]byte(key), []byte(h.adminAPIKey)) == 1 {
		return model.User{}, http.StatusForbidden, "The instance admin key cannot connect apps. Sign in with a personal account."
	}
	user, err := h.accountsSvc().GetUserByAPIKey(ctx, key, h.Limits.KeyIdleExpiry)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.User{}, http.StatusUnauthorized, "sign in again"
		}
		if errors.Is(err, model.ErrAccountSuspended) {
			return model.User{}, http.StatusForbidden, "This account has been suspended by the operator, so it cannot connect apps."
		}
		return model.User{}, http.StatusInternalServerError, "internal server error"
	}
	// The built-in admin-key account is not a person and never connects an
	// app. A person who happens to be an admin connects like anyone else.
	if user.Username == "admin" {
		return model.User{}, http.StatusForbidden, "This account cannot connect apps. Sign in with a personal account."
	}
	return user, 0, ""
}

// decide records Allow or Cancel from the consent screen and returns where to
// send the browser. It is the only place an authorization code is minted.
func (h *Handler) decide(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	// Same-origin only. The API key header already rules out a cross-site
	// form, and this rules out a cross-site script as a second fence.
	if auth.RefuseForeign(w, r, h.Issuer) {
		return
	}
	var body consentDecision
	if !httpx.DecodeJSON(w, r, 16<<10, &body) {
		return
	}
	req, ok := h.consentRequest(w, r, body)
	if !ok {
		return
	}
	user, status, msg := h.resolveConsentUser(r.Context(), r.Header.Get("X-API-Key"))
	if status != 0 {
		httpx.WriteJSON(w, status, httpx.ErrorResponse{Error: msg})
		return
	}
	scope, ok := h.consentScope(w, body)
	if !ok {
		return
	}
	code, err := h.mintAuthorizationCode(r, req, user, scope)
	if err != nil {
		log.Printf("connector: insert code: %v", err)
		httpx.WriteInternal(w)
		return
	}
	log.Printf("connector: consent user_id=%s client_id=%s", user.ID, req.Client.ClientID)
	h.alerts.Alert(user.ID, r.UserAgent(), httpx.CleanClientName(req.Client.Name))
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"redirect_to": h.redirectWith(req.RedirectURI, map[string]string{"code": code, "state": req.State})})
}

// consentDecision is the consent screen's Allow or Cancel.
type consentDecision struct {
	Query    string `json:"query"`
	CSRF     string `json:"csrf"`
	Decision string `json:"decision"`
	TeamID   string `json:"team_id"`
	Mode     string `json:"mode"`
}

// consentRequest is the authorization request the consent page was shown
// for, when the person allowed it. An invalid request, an expired page or a
// Cancel is answered here (a request error or Cancel goes back to the app),
// reporting false.
func (h *Handler) consentRequest(w http.ResponseWriter, r *http.Request, body consentDecision) (authzRequest, bool) {
	q, err := url.ParseQuery(strings.TrimPrefix(body.Query, "?"))
	if err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: "invalid request"})
		return authzRequest{}, false
	}
	q.Del("token") // sign-in plumbing of the consent page, not part of the request
	q.Del("cn")
	req, aerr := h.parseAuthorize(r.Context(), q)
	if aerr != nil && !aerr.redirect {
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: aerr.description})
		return req, false
	}
	if !h.checkConsentCSRF(req, body.CSRF) {
		httpx.WriteJSON(w, http.StatusForbidden, httpx.ErrorResponse{Error: "This page has expired. Go back to the app and connect again."})
		return req, false
	}
	if aerr == nil && body.Decision == "deny" {
		aerr = &authzError{code: "access_denied", description: "The person cancelled."}
	}
	if aerr != nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]string{"redirect_to": h.errorRedirect(req, aerr)})
		return req, false
	}
	if body.Decision != "allow" {
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: "decision must be allow or deny"})
		return req, false
	}
	return req, true
}

// consentScope is what an Allow grants: "sites", and on the hosted
// hackathon platform "sites events". A consent page left open across a
// deployment may still submit the old team-only choice: that narrower
// approval is never silently turned into a personal grant; the person is
// asked to reload the current consent page.
func (h *Handler) consentScope(w http.ResponseWriter, body consentDecision) (string, bool) {
	if !h.teamConnections {
		return authrules.OAuthScope, true
	}
	if body.Mode != "" || body.TeamID != "" {
		httpx.WriteJSON(w, http.StatusConflict, httpx.ErrorResponse{Error: "The connection options changed. Reload this page and allow again.", Code: "consent_updated"})
		return "", false
	}
	return authrules.OAuthScope + " " + authrules.GroupManagementScope, true
}

// mintAuthorizationCode is the only place an authorization code is minted:
// for this request, this person and scope, short-lived.
func (h *Handler) mintAuthorizationCode(r *http.Request, req authzRequest, user model.User, scope string) (string, error) {
	code := RandomToken(PrefixCode, 32)
	err := InsertOAuthCode(r.Context(), h.database, accounts.HashSecret(code), model.OAuthCode{
		ClientID:      req.Client.ClientID,
		UserID:        user.ID,
		RedirectURI:   req.RedirectURI,
		CodeChallenge: req.CodeChallenge,
		Scope:         scope,
		Resource:      req.Resource,
		ExpiresAt:     h.now().Add(oauthCodeTTL),
		Device:        connectionDevice(r.UserAgent()),
	})
	return code, err
}
