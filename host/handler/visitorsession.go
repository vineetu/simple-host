package handler

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"html"
	"log"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	accountapp "github.com/vsriram/simple-host/host/app/accounts"
	"github.com/vsriram/simple-host/host/model"
)

const (
	visitorCookieHost = "__Host-sh_vsess"
	visitorCookieHTTP = "sh_vsess"
	visitorCSRFHeader = "X-SH-CSRF"
	visitorCSRFValue  = "1"
)

func requestIsHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func visitorCookieName(r *http.Request) string {
	if requestIsHTTPS(r) {
		return visitorCookieHost
	}
	return visitorCookieHTTP
}

func requestHostName(r *http.Request) string {
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return strings.ToLower(host)
}

func setVisitorSessionCookie(w http.ResponseWriter, r *http.Request, value string, maxAge int) {
	secure := requestIsHTTPS(r)
	name := visitorCookieHTTP
	if secure {
		name = visitorCookieHost
	}
	// net/http: MaxAge=0 omits the attribute; MaxAge<0 emits Max-Age=0 (delete).
	if maxAge == 0 {
		maxAge = -1
	}
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func visitorCookieValue(r *http.Request) string {
	if c, err := r.Cookie(visitorCookieName(r)); err == nil && c.Value != "" {
		return c.Value
	}
	// Proto detection can disagree with when the cookie was issued; accept either name.
	if c, err := r.Cookie(visitorCookieHost); err == nil && c.Value != "" {
		return c.Value
	}
	if c, err := r.Cookie(visitorCookieHTTP); err == nil && c.Value != "" {
		return c.Value
	}
	return ""
}

// sessionCookieFor is the visitor cookie a storage or /me request may
// use. On a claimed <name>.<SITE_DOMAIN> host it is stricter: every such host
// and the shared host are the same "site" to a browser, so SameSite=Lax would
// let a page on any of them send this host's cookie along. There only the
// __Host- cookie counts (a sibling can plant a plain one, never a __Host- one)
// and only on a same-origin request.
func (h *SiteHandler) sessionCookieFor(r *http.Request) string {
	if host := requestHostName(r); h.isPlatformSubdomainHost(host) || h.isSiteHostName(r.Context(), host) || h.isFamilyHostName(host) {
		if !sameOriginRequest(r) {
			return ""
		}
		return strictVisitorCookie(r)
	}
	return visitorCookieValue(r)
}

// sessionValidFor requires the exact host and site where the visitor signed in.
// Person hosts never widen a session to the owner's other sites.
func (h *SiteHandler) sessionValidFor(r *http.Request, sess model.VisitorSession, siteID string) bool {
	now := time.Now()
	if !strings.EqualFold(sess.Host, requestHostName(r)) || now.After(sess.ExpiresAt) || now.After(sess.IdleExpiresAt) {
		return false
	}
	return sess.SiteID == siteID
}

func hasVisitorCSRF(r *http.Request) bool {
	if r.Header.Get(visitorCSRFHeader) == visitorCSRFValue {
		return true
	}
	ct := r.Header.Get("Content-Type")
	if ct == "" {
		return false
	}
	media, _, err := mime.ParseMediaType(ct)
	return err == nil && media == "application/json"
}

func (h *SiteHandler) isVisitorApexHost(host string) bool {
	host = strings.ToLower(host)
	if host == strings.ToLower(h.siteDomain) || host == "www."+strings.ToLower(h.siteDomain) {
		return true
	}
	// The base's apex and www (sitebase.go) are the app's, never a site's.
	if b := strings.ToLower(h.siteBase); h.baseSplit() && (host == b || host == "www."+b) {
		return true
	}
	raw := os.Getenv("PUBLIC_BASE_URL")
	if raw == "" {
		raw = "https://simple-host.app"
	}
	if u, err := url.Parse(raw); err == nil {
		if ah := strings.ToLower(u.Hostname()); ah != "" && host == ah {
			return true
		}
	}
	return false
}

// establishVisitor handles GET /v1/visitor/establish: consume the one-time
// token, check this browser holds the nonce the sign-in started with, then
// Set-Cookie on this host and 302 to the stored return_to.
//
// The code is burned before any check, so a code tried in the wrong browser
// (no nonce cookie, or another one) or on the wrong host cannot be retried.
// Only the __Host- nonce cookie counts over HTTPS: a sibling host under the
// same parent domain can plant a plain cookie, never a __Host- one.
func (h *SiteHandler) establishVisitor(w http.ResponseWriter, r *http.Request) {
	host := requestHostName(r)
	once := strings.TrimSpace(r.URL.Query().Get("once"))
	// Try again restarts sign-in at the site's own address, as a fresh
	// top-level navigation in this browser (the gate starts a new hop).
	retry := ""
	if host != "" && !h.isVisitorApexHost(host) && !strings.EqualFold(host, h.contentHost) {
		retry = "https://" + r.Host + "/"
		if !requestIsHTTPS(r) {
			retry = "http://" + r.Host + "/"
		}
	}
	fail := func(status int, reason string) {
		logSignInFail(r, "establish", reason, once)
		h.writeSignInFailed(w, status, reason, retry)
	}
	if !signInNavigationAllowed(r) {
		fail(403, "not_navigation")
		return
	}
	w.Header().Set("Referrer-Policy", "no-referrer")

	if once == "" {
		fail(http.StatusBadRequest, "no_code")
		return
	}

	tok, err := h.storeAccounts().ConsumeEstablishToken(r.Context(), once, host)
	if err != nil {
		fail(http.StatusBadRequest, "code_used_or_expired")
		return
	}
	activated := false
	defer func() {
		if !activated {
			_ = h.storeAccounts().DeleteVisitorSession(r.Context(), tok.SessionID)
		}
	}()
	if retry == "" {
		fail(http.StatusBadRequest, "wrong_host")
		return
	}
	if target, err := url.Parse(tok.ReturnTo); err == nil && strings.EqualFold(target.Hostname(), host) {
		retry = tok.ReturnTo
	}

	if !tok.NonceHash.Valid || visitorNonceCookie(r) == "" {
		fail(http.StatusBadRequest, "nonce_missing")
		return
	}
	if !nonceMatches(visitorNonceCookie(r), tok.NonceHash.String) {
		fail(http.StatusBadRequest, "nonce_mismatch")
		return
	}

	// A revoked or expired parent, an offline site, or a passcode set while
	// the browser was on the apex must not yield a usable session.
	sess, err := h.storeAccounts().GetVisitorSession(r.Context(), tok.SessionID, h.limits.KeyIdleExpiry)
	if err != nil || !h.sessionValidFor(r, sess, sess.SiteID) {
		fail(400, "session_invalid")
		return
	}
	// The current host binding must still name the original immutable site.
	target, parseErr := url.Parse(tok.ReturnTo)
	if parseErr != nil || !strings.EqualFold(target.Hostname(), host) {
		fail(400, "return_host")
		return
	}
	boundID, ok := h.PersonReturnSite(r.Context(), host, target.Path)
	if !ok {
		info, err := h.sitesSvc().GetSiteByCustomDomain(r.Context(), host)
		if err != nil || !info.VerifiedAt.Valid {
			fail(400, "site_unbound")
			return
		}
		boundID = info.SiteID
	}
	if boundID != sess.SiteID {
		fail(400, "site_moved")
		return
	}
	if sess.SSOPassHash.Valid {
		if off, err := h.sitesSvc().SiteOffline(r.Context(), sess.SiteID); err != nil || off {
			fail(403, "site_offline")
			return
		}
		if susp, _, err := h.sitesSvc().SiteSuspension(r.Context(), sess.SiteID); err != nil || susp {
			fail(403, "site_suspended")
			return
		}
		if !h.PasscodeLetsIn(r, sess.SiteID) {
			logSignInFail(r, "establish", "passcode", once)
			writeSiteLocked(w)
			return
		}
	}
	first, err := h.storeAccounts().ActivateVisitorSession(r.Context(), tok.SessionID, h.visitorSessionTTL(), h.visitorSessionIdle())
	if err != nil {
		fail(400, "activate")
		return
	}
	activated = true
	if first {
		h.emitSiteNotification(sess.SiteID, "visitor.signed_up", map[string]any{"visitor": sess.UserID}, 0)
	}
	h.noteOwnerVisitorSignIn(r, sess.SiteID, sess.UserID)
	setVisitorSessionCookie(w, r, hex.EncodeToString(tok.SessionID), h.visitorCookieMaxAge())
	setVisitorNonceCookie(w, r, "", -1)
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, tok.ReturnTo, http.StatusFound)
}

// logSignInFail records why a sign-in step refused, with a short reason
// code and at most the first 6 characters of the one-use code or state.
func logSignInFail(r *http.Request, route, reason, code string) {
	if len(code) > 6 {
		code = code[:6]
	}
	log.Printf("signin_fail route=%s reason=%s host=%s code=%s sec_fetch=%s/%s/%s",
		route, reason, requestHostName(r), code,
		r.Header.Get("Sec-Fetch-Site"), r.Header.Get("Sec-Fetch-Mode"), r.Header.Get("Sec-Fetch-Dest"))
}

// signInFailedHelp says what went wrong in words a visitor can act on.
func signInFailedHelp(reason string) string {
	switch reason {
	case "nonce_missing":
		return "This sign-in finished in a different browser or app from the one it started in, for example a link opened from another app into Safari."
	case "nonce_mismatch":
		return "A newer sign-in to this site started in this browser, so this one was cancelled."
	case "code_used_or_expired", "no_code":
		return "This sign-in link was already used or has expired."
	case "site_offline", "site_suspended":
		return "This site is not taking sign-ins right now."
	}
	return "Sign-in could not finish."
}

var signInFailedPage = themed(`<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><!--sh:theme--><title>Sign-in failed</title><style>` + passcodeGateCSS +
	`html:root main.gate a.primary{display:block;box-sizing:border-box;width:100%;height:48px;line-height:48px;margin-top:12px;border-radius:255px 18px 225px 18px / 18px 225px 18px 255px;background:#116b77;color:#fffdf5;font-family:Caveat,"Segoe Print",cursive;font-size:22px;font-weight:700;text-align:center;text-decoration:none}` +
	`html:root[data-theme=dark] main.gate a.primary{background:#9edacf;color:#0b1222}` +
	`</style></head><body><main class="gate"><h1>Sign-in did not finish</h1><p class="lead">__HELP__</p>__ACTION__</main></body></html>`)

// writeSignInFailed is the establish failure page: what happened, and a Try
// again link that restarts sign-in for this site in this browser.
func (h *SiteHandler) writeSignInFailed(w http.ResponseWriter, status int, reason, retry string) {
	action := `<p class="fine">Close this tab and open the site again.</p>`
	if retry != "" {
		action = `<a class="primary" href="` + html.EscapeString(retry) + `">Try again</a><p class="fine">Try again signs you in to this site in this browser.</p>`
	}
	page := strings.NewReplacer("__HELP__", html.EscapeString(signInFailedHelp(reason)), "__ACTION__", action).Replace(signInFailedPage)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(h.pipeline().hostedStatusPage(page))
}

// logoutVisitor handles POST /v1/visitor/logout: delete this session and clear
// the cookie. Requires X-SH-CSRF: 1 or Content-Type: application/json.
func (h *SiteHandler) logoutVisitor(w http.ResponseWriter, r *http.Request) {
	if !hasVisitorCSRF(r) {
		writeJSON(w, http.StatusForbidden, struct {
			Error string `json:"error"`
			Code  string `json:"code"`
		}{Error: "missing CSRF header", Code: "csrf_required"})
		return
	}

	if !sameOriginRequest(r) {
		writeJSON(w, 403, errorResponse{Error: "sign out on the site's own page"})
		return
	}
	// SIM-360 privacy guardrail: the one Sign out ends push on that
	// device. auth.js's SH.signOut() already unsubscribes the browser's
	// own PushSubscription and calls /push/unsubscribe itself, but that is
	// a second, client-initiated request; resolve the session being
	// destroyed here too and delete that visitor's push rows for this
	// site server-side, so a blocked or racing client call can never
	// leave one behind. Best-effort: never blocks or fails the sign-out.
	h.deleteVisitorPushOnSignOut(r)
	back, err := h.endSiteSSO(w, r, "/")
	if err != nil {
		writeJSON(w, 500, errorResponse{Error: "sign-out unavailable"})
		return
	}
	setVisitorSessionCookie(w, r, "", 0)
	if back != "" {
		writeJSON(w, 200, map[string]string{"redirect_url": back})
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *SiteHandler) resolveWriterKey(ctx context.Context, key string) (model.User, bool, error) {
	if subtle.ConstantTimeCompare([]byte(key), []byte(h.adminAPIKey)) == 1 {
		return model.User{ID: h.adminUserID, Username: "admin", IsAdmin: true}, true, nil
	}
	u, err := h.storeAccounts().GetUserByAPIKey(ctx, key, h.limits.KeyIdleExpiry)
	if err != nil {
		// A suspended account's key is refused like an unknown one.
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, model.ErrAccountSuspended) {
			return model.User{}, false, nil
		}
		return model.User{}, false, err
	}
	return u, true, nil
}

// getVisitorMe reports the current site session without extending its lifetime.
func (h *SiteHandler) getVisitorMe(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	siteName := strings.TrimSpace(r.PathValue("sitename"))
	if siteName == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "site name is required"})
		return
	}
	if !h.authorizeStateOrigin(w, r, siteName) {
		writeOriginRefused(w)
		return
	}
	siteID, err := h.resolveSiteID(r, siteName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	// Shared host: sign-in is never offered (one origin for every site). If the
	// site has its own domain, say so, so the page can send the visitor there.
	if strings.EqualFold(requestHostName(r), h.contentHost) {
		resp := map[string]any{"signed_in": false, "sign_in_available": false, "code": "custom_domain_required"}
		if info, ok, _ := h.siteOwnAddress(r.Context(), siteID); ok && info.Domain != "" {
			resp["code"] = "use_custom_domain"
			resp["domain"] = info.Domain
		} else if h.personHostsCanonical() {
			// The site's own address, where visitors sign in.
			if handle, _, name, err := h.sitesSvc().GetSiteOwner(r.Context(), siteID); err == nil && h.personAddressFor(handle, name) {
				resp["address"] = h.SiteURL(handle, name)
			}
		}
		writeJSON(w, http.StatusOK, resp)
		return
	}
	if domain, elsewhere := h.livesOnDomainElsewhere(r, siteID); elsewhere {
		writeJSON(w, http.StatusOK, map[string]any{"signed_in": false, "sign_in_available": false, "code": "use_custom_domain", "domain": domain})
		return
	}
	if id, decErr := hex.DecodeString(h.sessionCookieFor(r)); decErr == nil && len(id) == 32 {
		sess, sessErr := h.storeAccounts().GetVisitorSession(r.Context(), id, h.limits.KeyIdleExpiry)
		if sessErr == nil {
			if h.sessionValidFor(r, sess, siteID) {
				expires := sess.ExpiresAt
				if sess.IdleExpiresAt.Before(expires) {
					expires = sess.IdleExpiresAt
				}
				resp := h.visitorSignedInResponse(r, expires, "", "")

				// The same address the site's rules see (visitorEmail).
				email, err := visitorEmail(r.Context(), h.database, sess.UserID)
				resp.Email = email
				if err == nil {
					identity, ierr := h.storeAccounts().GetLatestOAuthIdentity(r.Context(), sess.UserID)
					if ierr == nil && strings.EqualFold(identity.Email.String, email) {
						resp.Provider = identity.Provider
					} else if ierr != nil && !errors.Is(ierr, sql.ErrNoRows) {
						err = ierr
					}
				}
				if err != nil {
					writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
					return
				}
				// The same test storage applies (storageOwnerOnSite): the
				// strict same-origin session, the owner's account, one of
				// the site's own hosts. On Simple Hack: a team member or an
				// organiser while the site may change (the storage bindings provider).
				if h.ed.Storage().EventOwners {
					if strict, ok := h.strictVisitorSession(r, siteID); ok && strict.UserID == sess.UserID && h.storageOwnHost(r, siteID) {
						owner, oerr := h.storageBindingProvider().OwnerOnSite(r.Context(), siteID, sess.UserID)
						resp.SiteOwner = oerr == nil && owner
					}
				} else {
					if _, ownerID, _, oerr := h.sitesSvc().GetSiteOwner(r.Context(), siteID); oerr == nil && ownerID == sess.UserID {
						if strict, ok := h.strictVisitorSession(r, siteID); ok && strict.UserID == ownerID {
							resp.SiteOwner = h.storageOwnHost(r, siteID)
						}
					}
				}
				writeJSON(w, http.StatusOK, resp)
				return
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"signed_in": false, "sign_in": "/v1/auth/oauth/providers"})
}

func (h *SiteHandler) optionsVisitorMe(w http.ResponseWriter, r *http.Request) {
	siteName := strings.TrimSpace(r.PathValue("sitename"))
	if siteName == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if !h.authorizeStateOrigin(w, r, siteName) {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	w.Header().Set("Access-Control-Max-Age", "600")
	w.WriteHeader(http.StatusNoContent)
}

type visitorMeResponse struct {
	SignedIn  bool   `json:"signed_in"`
	Email     string `json:"email,omitempty"`
	Provider  string `json:"provider,omitempty"`
	ExpiresAt string `json:"expires_at"`
	// SiteOwner: the site's own owner, signed in on one of the site's own
	// addresses, where the page reads and changes saved data with owner
	// rights (storageOwnerOnSite). Simple Host only.
	SiteOwner bool `json:"site_owner,omitempty"`
}

func (h *SiteHandler) visitorSignedInResponse(r *http.Request, expires time.Time, email, provider string) visitorMeResponse {
	resp := visitorMeResponse{SignedIn: true, ExpiresAt: expires.Format(time.RFC3339)}
	if !strings.EqualFold(requestHostName(r), h.contentHost) {
		resp.Email, resp.Provider = email, provider
	}
	return resp
}

// requestOrigin is the scheme://host the browser says the request came from:
// Origin, else the Referer's origin, else this request's own origin when the
// browser marks it Sec-Fetch-Site: same-origin. A page with
// <meta name="referrer" content="no-referrer"> sends no Referer and no Origin
// on its own GETs, and Origin: null on its writes; Fetch Metadata still names
// it, and a page script cannot set a Sec- header. Empty when none is usable.
func requestOrigin(r *http.Request) string {
	if o := r.Header.Get("Origin"); o != "" && o != "null" {
		return o
	}
	if ref := r.Header.Get("Referer"); ref != "" {
		if u, err := url.Parse(ref); err == nil && u.Scheme != "" && u.Host != "" {
			return u.Scheme + "://" + u.Host
		}
	}
	if r.Header.Get("Sec-Fetch-Site") == "same-origin" && r.Host != "" {
		scheme := "http"
		if requestIsHTTPS(r) {
			scheme = "https"
		}
		return scheme + "://" + r.Host
	}
	return ""
}

// sameOriginRequest: the request was made by a page on this very origin. A
// browser cannot forge Origin/Referer or Sec-Fetch-Site; a script outside a
// browser can, but then it has no visitor cookie to go with them.
func sameOriginRequest(r *http.Request) bool {
	if sfs := r.Header.Get("Sec-Fetch-Site"); sfs != "" && sfs != "same-origin" {
		return false
	}
	o := requestOrigin(r)
	if o == "" {
		return false
	}
	u, err := url.Parse(o)
	if err != nil || u.Host == "" {
		return false
	}
	want := "http"
	if requestIsHTTPS(r) {
		want = "https"
	}
	return strings.EqualFold(u.Scheme, want) && strings.EqualFold(u.Host, r.Host)
}

// strictVisitorCookie is the visitor session cookie without the fallback
// visitorCookieValue allows: over HTTPS only the __Host- cookie counts, since
// a sibling host under the same parent domain can plant a plain sh_vsess
// cookie (Domain=parent) but can never set a __Host- one.
func strictVisitorCookie(r *http.Request) string {
	name := visitorCookieHTTP
	if requestIsHTTPS(r) {
		name = visitorCookieHost
	}
	if c, err := r.Cookie(name); err == nil {
		return c.Value
	}
	return ""
}

// strictVisitorSession returns the visitor signed in to siteID on this host,
// for a same-origin request carrying the __Host- cookie. ok=false otherwise.
func (h *SiteHandler) strictVisitorSession(r *http.Request, siteID string) (model.VisitorSession, bool) {
	if r.Header.Get(visitorPassHeader) != "" {
		return h.internalVisitorSession(r, siteID)
	}
	if !sameOriginRequest(r) {
		return model.VisitorSession{}, false
	}
	id, err := hex.DecodeString(strictVisitorCookie(r))
	if err != nil || len(id) != 32 {
		return model.VisitorSession{}, false
	}
	sess, err := h.storeAccounts().GetVisitorSession(r.Context(), id, h.limits.KeyIdleExpiry)
	if err != nil || !h.sessionValidFor(r, sess, siteID) {
		return model.VisitorSession{}, false
	}
	paperRecorder(r.Context()).Member(r.Context(), sess.UserID)
	return sess, true
}

// visitorEmail is the address an account presents to other people's sites
// (who may save, blocks, one per person, the _submitted_by stamp, GET /me):
// its current sign-in email, which it proved by code when it signed up or
// moved there. A linked Google address is only used for an account whose
// name is not an address (an organiser-made event account), because an
// account can move off an address it no longer owns while the identity
// linked under it stays.
//
// A self-signup password account's username is never treated as a verified
// address here, even one shaped like an email: the person who typed it never
// proved they control it, which is exactly how a password signup could
// squat someone else's email ahead of them (see the password-accounts
// pre-account-hijacking fix). An admin-created password account keeps the
// old behaviour -- the admin vouched for that address. If a self-signup
// account separately proved an address through Google/OIDC, that linked
// address is used instead; with neither, it has no verified email at all.
func visitorEmail(ctx context.Context, database *sql.DB, userID string) (string, error) {
	u, err := accountapp.New(database).GetUserByID(ctx, userID)
	if err != nil {
		return "", err
	}
	looksLikeEmail := strings.Contains(u.Username, "@")
	selfSignupPassword := false
	if looksLikeEmail {
		selfSignupPassword, err = accountapp.New(database).SelfSignupPasswordAccount(ctx, userID)
		if err != nil {
			return "", err
		}
	}
	if looksLikeEmail && !selfSignupPassword {
		return u.Username, nil
	}
	identity, err := accountapp.New(database).GetLatestOAuthIdentity(ctx, userID)
	if err == nil && identity.Email.Valid && identity.Email.String != "" {
		return identity.Email.String, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if looksLikeEmail {
		// A self-signup password account's email-shaped username never
		// proved it controls that address (pre-account hijacking) and there
		// is no linked OAuth identity either: no verified email to fall
		// back to, unlike an ordinary non-email username below.
		return "", nil
	}
	return u.Username, nil
}

// onOwnDomain: this request arrived on the site's own origin — its proven own
