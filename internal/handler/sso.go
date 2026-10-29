package handler

import (
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"html"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/vsriram/simple-host/internal/auth"
	db "github.com/vsriram/simple-host/internal/db"
)

const ssoCookieHost = "__Host-sh_sso"
const ssoCookieHTTP = "sh_sso"

func ssoCookieName(r *http.Request) string {
	if requestIsHTTPS(r) {
		return ssoCookieHost
	}
	return ssoCookieHTTP
}

// Only /v1/sso handlers may read this cookie. Account authentication never
// accepts it. Keeping the reader in this file makes that boundary testable.
func ssoCookie(r *http.Request) string {
	if !strings.HasPrefix(r.URL.Path, "/v1/sso/") {
		return ""
	}
	c, err := r.Cookie(ssoCookieName(r))
	if err != nil {
		return ""
	}
	return c.Value
}
func setSSOCookie(w http.ResponseWriter, r *http.Request, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: ssoCookieName(r), Value: value, Path: "/", MaxAge: maxAge, HttpOnly: true, Secure: requestIsHTTPS(r), SameSite: http.SameSiteLaxMode})
}

// Issuance runs only through /v1/sso/session, after account sign-in on the apex.
func issueSSOPass(w http.ResponseWriter, r *http.Request, database *sql.DB, keyHash string) error {
	raw, err := randomHex(32)
	if err != nil {
		return err
	}
	if err = db.ReplaceSSOPass(r.Context(), database, db.HashAPIKey(raw), keyHash, db.HashAPIKey(ssoCookie(r)), visitorSessionTTL(), visitorSessionIdle()); err != nil {
		return err
	}
	setSSOCookie(w, r, raw, visitorCookieMaxAge())
	return nil
}
func (h *OAuthHandler) registerSSO(mux *http.ServeMux) {
	mux.Handle("GET /v1/sso/start", rateLimitByIP(h.ipLimiter, http.HandlerFunc(h.startSSO)))
	mux.Handle("GET /v1/sso/hop", h.ssoApex(adminUICSP(http.HandlerFunc(h.hopSSO))))
	mux.Handle("POST /v1/sso/consent", h.ssoApex(http.HandlerFunc(h.hopSSO)))
	mux.Handle("POST /v1/sso/session", h.ssoApex(rateLimitByIP(h.ipLimiter, http.HandlerFunc(h.bootstrapSSO))))
	mux.Handle("GET /v1/sso/signed-out", h.ssoApex(http.HandlerFunc(h.signedOutSSO)))
}
func (h *OAuthHandler) ssoApex(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		// A no-referrer, non-CORS form navigation may serialize Origin as
		// null. Fetch Metadata still proves its origin; the consent handler
		// additionally requires the pass-bound one-time form token.
		sameOriginForm := r.Method == "POST" && r.URL.Path == "/v1/sso/consent" && r.Header.Get("Origin") == "null" && r.Header.Get("Sec-Fetch-Site") == "same-origin"
		if publicBaseHost(h.cfg.PublicBaseURL) == "" || requestHostName(r) != publicBaseHost(h.cfg.PublicBaseURL) || (!accountAuthAllowed(r, publicBaseOrigin(h.cfg.PublicBaseURL)) && !sameOriginForm) {
			http.Error(w, "Sign in on the app's own address.", 403)
			return
		}
		if r.Method == "POST" && !sameOriginRequest(r) && !sameOriginForm {
			http.Error(w, "Use the sign-in page on this address.", 403)
			return
		}
		if r.Method == "GET" {
			if dest := r.Header.Get("Sec-Fetch-Dest"); dest != "" && dest != "document" {
				http.Error(w, "Open sign-in in this tab.", 403)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
func (h *OAuthHandler) bootstrapSSO(w http.ResponseWriter, r *http.Request) {
	key := r.Header.Get("X-API-Key")
	if key == "" {
		http.Error(w, "Sign in first.", 401)
		return
	}
	// Stored full account keys only. Internal connector and deploy keys cannot
	// manufacture a browser sign-in that outlives their authority.
	keyHash := db.HashAPIKey(key)
	if !h.ssoKeyLimiter.allowHTTP(w, keyHash) {
		tooManyRequests(w)
		return
	}
	if p, err := db.GetSSOPass(r.Context(), h.database, db.HashAPIKey(ssoCookie(r))); err == nil && p.ParentKeyHash == keyHash {
		w.WriteHeader(204)
		return
	}
	if err := issueSSOPass(w, r, h.database, keyHash); err != nil {
		http.Error(w, "Sign in first.", 401)
		return
	}
	w.WriteHeader(204)
}
func (h *OAuthHandler) startSSO(w http.ResponseWriter, r *http.Request) {
	if publicBaseHost(h.cfg.PublicBaseURL) == "" || crossSiteSubresource(r) || framedElsewhere(r) || (r.Header.Get("Sec-Fetch-Dest") != "" && r.Header.Get("Sec-Fetch-Dest") != "document") {
		writeOAuthHTMLError(w, 400)
		return
	}
	sanitized, siteID, host, purpose, err := h.sanitizeReturnTo(r.Context(), r.URL.Query().Get("return_to"))
	if err != nil || purpose != "site" || !strings.EqualFold(host, requestHostName(r)) {
		writeOAuthHTMLError(w, 400)
		return
	}
	if !h.siteSignInAllowed(w, r, siteID.String) {
		return
	}
	nonce, err := randomHex(32)
	if err != nil {
		writeOAuthHTMLError(w, 500)
		return
	}
	state, err := randomHex(32)
	if err != nil {
		writeOAuthHTMLError(w, 500)
		return
	}
	sum := sha256.Sum256([]byte(nonce))
	hop := db.SSOHop{State: state, SiteID: siteID.String, Host: host, ReturnTo: sanitized, NonceHash: base64.RawURLEncoding.EncodeToString(sum[:])}
	if err := db.InsertSSOHop(r.Context(), h.database, hop); err != nil {
		writeOAuthHTMLError(w, 500)
		return
	}
	setVisitorNonceCookie(w, r, nonce, int(siteSignInNonceTTL.Seconds()))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	http.Redirect(w, r, strings.TrimRight(h.cfg.PublicBaseURL, "/")+"/v1/sso/hop?state="+state, 302)
}
func (h *OAuthHandler) hopSSO(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	if r.Method == "POST" {
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		if r.ParseForm() != nil {
			writeOAuthHTMLError(w, 400)
			return
		}
		state = r.PostForm.Get("state")
	}
	hop, err := db.GetSSOHop(r.Context(), h.database, state, false)
	if err != nil {
		writeOAuthHTMLError(w, 400)
		return
	}
	// An OAuth callback carries a browser-bound account token. Redeem that on
	// the apex before using any existing pass, including when switching people.
	p, err := db.GetSSOPass(r.Context(), h.database, db.HashAPIKey(ssoCookie(r)))
	if err != nil || r.URL.Query().Get("token") != "" {
		if r.Method == "POST" {
			writeOAuthHTMLError(w, 401)
			return
		}
		h.ssoSignInPage(w, r, hop)
		return
	}
	email, err := visitorEmail(r.Context(), h.database, p.UserID)
	if err != nil {
		writeOAuthHTMLError(w, 500)
		return
	}
	listed, err := db.IsSiteViewer(r.Context(), h.database, hop.SiteID, strings.ToLower(email))
	if err != nil {
		writeOAuthHTMLError(w, 500)
		return
	}
	consent, err := db.HasSSOConsent(r.Context(), h.database, p.UserID, hop.SiteID)
	if err != nil {
		writeOAuthHTMLError(w, 500)
		return
	}
	if r.Method == "POST" {
		if !hop.ConsentPassHash.Valid || hop.ConsentPassHash.String != p.Hash || !hop.ConsentHash.Valid || !nonceMatches(r.PostForm.Get("form_token"), hop.ConsentHash.String) {
			writeOAuthHTMLError(w, 403)
			return
		}
	} else if !listed && !consent {
		token, err := randomHex(32)
		if err != nil {
			writeOAuthHTMLError(w, 500)
			return
		}
		sum := sha256.Sum256([]byte(token))
		if err := db.SetSSOConsentForm(r.Context(), h.database, state, base64.RawURLEncoding.EncodeToString(sum[:]), p.Hash); err != nil {
			writeOAuthHTMLError(w, 500)
			return
		}
		w.Header().Set("X-Frame-Options", "DENY")
		// Chromium applies form-action to redirects too. Permit only the
		// exact validated destination, while the form itself posts here.
		target, _ := url.Parse(hop.ReturnTo)
		w.Header().Set("Content-Security-Policy", w.Header().Get("Content-Security-Policy")+"; form-action 'self' "+target.Scheme+"://"+target.Host)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		page := strings.NewReplacer("__EMAIL__", html.EscapeString(email), "__HOST__", html.EscapeString(hop.Host), "__STATE__", html.EscapeString(state), "__TOKEN__", html.EscapeString(token)).Replace(ssoConsentPage)
		w.Write(stampNonce(r, hostedStatusPage(page)))
		return
	}
	// Delete the state in the same transaction as the fresh session and code.
	// Concurrent hops cannot mint twice; revoking the parent races safely with
	// the foreign-key insert, and is checked again when the code is redeemed.
	tx, err := h.database.BeginTx(r.Context(), nil)
	if err != nil {
		writeOAuthHTMLError(w, 500)
		return
	}
	defer tx.Rollback()
	hop, err = db.GetSSOHop(r.Context(), tx, state, true)
	if err != nil {
		writeOAuthHTMLError(w, 400)
		return
	}
	if _, err = db.GetSSOPass(r.Context(), tx, p.Hash); err != nil {
		writeOAuthHTMLError(w, 401)
		return
	}
	if r.Method == "POST" {
		if hop.ConsentPassHash.String != p.Hash || !nonceMatches(r.PostForm.Get("form_token"), hop.ConsentHash.String) {
			writeOAuthHTMLError(w, 403)
			return
		}
		if err = db.RememberSSOConsent(r.Context(), tx, p.UserID, hop.SiteID); err != nil {
			writeOAuthHTMLError(w, 500)
			return
		}
	}
	id, err := randomRaw(32)
	if err != nil {
		writeOAuthHTMLError(w, 500)
		return
	}
	once, err := randomHex(32)
	if err != nil {
		writeOAuthHTMLError(w, 500)
		return
	}
	now := time.Now()
	if err = db.InsertVisitorSession(r.Context(), tx, id, p.UserID, hop.SiteID, hop.Host, now.Add(visitorSessionTTL()), now.Add(visitorSessionIdle())); err == nil {
		err = db.ParentVisitorSession(r.Context(), tx, id, p.Hash)
	}
	if err == nil {
		err = db.InsertEstablishToken(r.Context(), tx, once, id, hop.Host, hop.ReturnTo, sql.NullString{String: hop.NonceHash, Valid: true}, now.Add(time.Minute))
	}
	if err == nil {
		err = db.TouchSSOPass(r.Context(), tx, p.Hash, visitorSessionIdle())
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		writeOAuthHTMLError(w, 500)
		return
	}
	u, _ := url.Parse(hop.ReturnTo)
	http.Redirect(w, r, u.Scheme+"://"+u.Host+"/v1/visitor/establish?once="+once, 302)
}
func (h *OAuthHandler) signedOutSSO(w http.ResponseWriter, r *http.Request) {
	back, err := db.CompleteSSOSignout(r.Context(), h.database, r.URL.Query().Get("token"), db.HashAPIKey(ssoCookie(r)))
	if err != nil {
		writeOAuthHTMLError(w, 400)
		return
	}
	setSSOCookie(w, r, "", -1)
	http.Redirect(w, r, back, 302)
}

// SetSSOHop enables the gate's automatic hop only on an instance with an
// account apex. Path-only installs without one retain their visitor sign-in.
func (h *SiteHandler) SetSSOHop(o *OAuthHandler) {
	if publicBaseHost(o.cfg.PublicBaseURL) == "" {
		return
	}
	h.ssoStart = rateLimitByIP(o.ipLimiter, http.HandlerFunc(o.startSSO))
	h.ssoBaseURL = strings.TrimRight(o.cfg.PublicBaseURL, "/")
}
func (h *SiteHandler) startGateSSO(w http.ResponseWriter, r *http.Request) bool {
	if h.ssoStart == nil {
		return false
	}
	scheme := "http"
	if requestIsHTTPS(r) {
		scheme = "https"
	}
	clone := r.Clone(r.Context())
	u := *r.URL
	clone.URL = &u
	q := url.Values{"return_to": {scheme + "://" + r.Host + safeNext(gateNext(r))}}
	clone.URL.RawQuery = q.Encode()
	h.ssoStart.ServeHTTP(w, clone)
	return true
}

// endSiteSSO never reads the apex cookie. Every site logout with an account
// apex returns a one-use navigation, including absent, expired and legacy sessions.
func (h *SiteHandler) endSiteSSO(w http.ResponseWriter, r *http.Request, next string) (string, error) {
	var sess db.VisitorSession
	id, err := hex.DecodeString(strictVisitorCookie(r))
	if err == nil && len(id) == 32 {
		s, err := db.GetVisitorSession(r.Context(), h.database, id)
		if err != nil && err != sql.ErrNoRows {
			return "", err
		}
		if err == nil && strings.EqualFold(s.Host, requestHostName(r)) {
			sess = s
			if !s.SSOPassHash.Valid || h.ssoBaseURL == "" {
				if err := db.DeleteVisitorSession(r.Context(), h.database, id); err != nil {
					return "", err
				}
			}
		}
	}
	if h.ssoBaseURL == "" {
		return "", nil
	}
	// Without a host-bound session, resolve the site before storing a return
	// address. A missing session must not turn the apex into an open redirect.
	if len(sess.ID) == 0 {
		_, _, ok, err := h.passcodeSiteForHost(r.Context(), requestHostName(r), safeNext(next))
		if err != nil {
			return "", err
		}
		if !ok {
			return "", sql.ErrNoRows
		}
	}
	token, err := randomHex(32)
	if err != nil {
		return "", err
	}
	scheme := "http"
	if requestIsHTTPS(r) {
		scheme = "https"
	}
	back := scheme + "://" + r.Host + safeNext(next)
	if err = db.RevokeVisitorSSO(r.Context(), h.database, sess, token, back); err != nil {
		return "", err
	}
	return h.ssoBaseURL + "/v1/sso/signed-out?token=" + token, nil
}

// ssoReturnToOK is the owner-provider callback allow-list for the apex card.
func ssoReturnToOK(u *url.URL, base string) bool {
	if u == nil || u.Path != "/v1/sso/hop" || u.Fragment != "" {
		return false
	}
	q := u.Query()
	if len(q) != 2 || len(q["state"]) != 1 || len(q["cn"]) != 1 || len(q.Get("state")) != 64 || !nonceHashRe.MatchString(q.Get("cn")) {
		return false
	}
	same := *u
	same.Path = "/"
	same.RawPath = ""
	same.RawQuery = ""
	return ownerReturnToOK(&same, base)
}

// Reuse the server-rendered sign-in card, including its shared light/dark
// theme. Consent works with scripts disabled; only theme selection uses JS.
var ssoConsentPage = themed(`<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><!--sh:theme--><title>Continue to site</title><style>` + passcodeGateCSS + `html:root main.gate h1,html:root main.gate p{overflow-wrap:anywhere}html:root main.gate h1 span{display:block;font:600 20px/1.4 system-ui;margin:8px 0 16px}</style></head><body><main class="gate"><h1>Continue as <span>__EMAIL__</span></h1><p class="lead">to __HOST__</p><p class="fine">The site owner can see the email you sign in with.</p><form method="post" action="/v1/sso/consent"><input type="hidden" name="state" value="__STATE__"><input type="hidden" name="form_token" value="__TOKEN__"><button class="primary" type="submit">Continue</button></form></main></body></html>`)

func (h *OAuthHandler) ssoSignInPage(w http.ResponseWriter, r *http.Request, hop db.SSOHop) {
	page, err := chromePage("sso-signin.html", chromeDataFor(r, ""))
	if err != nil {
		writeOAuthHTMLError(w, 500)
		return
	}
	p := strings.ReplaceAll(string(page), "__HOST__", html.EscapeString(hop.Host))
	// adminUICSP provides the script nonce; keep this page out of frames.
	w.Header().Set("Content-Security-Policy", w.Header().Get("Content-Security-Policy")+"; frame-ancestors 'none'; form-action 'self'")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(stampNonce(r, []byte(p)))
}

// Consent memory can be revoked without a session-list UI. This is an
// account-key operation; the SSO cookie still has no account API authority.
func (h *UserHandler) forgetSSOConsent(w http.ResponseWriter, r *http.Request) {
	if !sameOriginRequest(r) {
		writeJSON(w, 403, errorResponse{Error: "Use the account's own address."})
		return
	}
	user := auth.GetUser(r.Context())
	if user == nil || user.KeyHash == "" || user.KeyScope != db.KeyScopeFull {
		writeJSON(w, 403, errorResponse{Error: "Use a full account key."})
		return
	}
	if err := db.ForgetSSOConsent(r.Context(), h.database, user.ID, r.PathValue("site")); err != nil {
		writeJSON(w, 500, errorResponse{Error: "Could not forget consent."})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(204)
}
