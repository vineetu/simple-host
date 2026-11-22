package gate

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"html"
	"log"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	accountapp "github.com/vsriram/simple-host/host/access"
	notifications "github.com/vsriram/simple-host/host/data/notify"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
	operations "github.com/vsriram/simple-host/host/ops"
	"github.com/vsriram/simple-host/host/papermetrics"
	"github.com/vsriram/simple-host/host/web"
)

const (
	VisitorCookieHost = "__Host-sh_vsess"
	VisitorCookieHTTP = "sh_vsess"
	visitorCSRFHeader = "X-SH-CSRF"
	visitorCSRFValue  = "1"
)

func setVisitorSessionCookie(w http.ResponseWriter, r *http.Request, value string, maxAge int) {
	secure := httpx.IsHTTPS(r)
	name := VisitorCookieHTTP
	if secure {
		name = VisitorCookieHost
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

// establishVisitor handles GET /v1/visitor/establish: claim the one-time
// token for this browser and host (operations.ClaimVisitorEstablish), let a
// company sign-in's site recheck its gate, settle the session, then
// Set-Cookie on this host and 302 to the stored return_to.
//
// Only the __Host- nonce cookie counts over HTTPS: a sibling host under the
// same parent domain can plant a plain cookie, never a __Host- one.
func (h *Handler) establishVisitor(w http.ResponseWriter, r *http.Request) {
	host := httpx.HostName(r)
	once := strings.TrimSpace(r.URL.Query().Get("once"))
	retry := h.establishRetry(r, host)
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
	tok, sess, status, reason := operations.ClaimVisitorEstablish(r.Context(), h.Database, h.Hosts, once, host, visitorNonceCookie(r), retry != "", h.Limits.KeyIdleExpiry)
	if target, err := url.Parse(tok.ReturnTo); retry != "" && err == nil && strings.EqualFold(target.Hostname(), host) {
		retry = tok.ReturnTo
	}
	if reason == "" && sess.SSOPassHash.Valid {
		status, reason = h.establishSiteOpen(r, sess.SiteID)
	}
	first, err := SettleEstablish(r.Context(), h.Database, tok.SessionID, reason == "", h.Limits.VisitorSessionTTL, h.Limits.VisitorSessionIdle)
	switch {
	case reason == "passcode":
		logSignInFail(r, "establish", "passcode", once)
		writeSiteLocked(w)
		return
	case reason != "":
		fail(status, reason)
		return
	case err != nil:
		fail(400, "activate")
		return
	}
	h.VisitorSignedIn(r, sess.SiteID, sess.UserID, first)
	setVisitorSessionCookie(w, r, hex.EncodeToString(tok.SessionID), int(h.Limits.VisitorSessionTTL/time.Second))
	setVisitorNonceCookie(w, r, "", -1)
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, tok.ReturnTo, http.StatusFound)
}

// establishRetry is where Try again restarts sign-in: the site's own
// address, as a fresh top-level navigation in this browser (the gate starts
// a new hop). "" on the apex and the content host.
func (h *Handler) establishRetry(r *http.Request, host string) string {
	if host == "" || h.Hosts.IsApexHost(host) || strings.EqualFold(host, h.ContentHost) {
		return ""
	}
	if !httpx.IsHTTPS(r) {
		return "http://" + r.Host + "/"
	}
	return "https://" + r.Host + "/"
}

// signInRefusal is why a site takes no visitor sign-in now.
type signInRefusal int

const (
	signInOpen signInRefusal = iota
	signInOffline
	signInSuspended
	signInLocked // its passcode does not let this browser in yet
)

// siteSignIn decides whether site siteID takes a visitor sign-in from this
// request: it is not offline (offline.go), not taken down (suspend.go), and
// its passcode lets the browser in (PasscodeLetsIn). On a failed read, err is
// set and the refusal names the check that failed. reason is the take-down's.
// The OAuth starts and the sign-in establish both ask it.
func (h *Handler) siteSignIn(r *http.Request, siteID string) (refusal signInRefusal, reason string, err error) {
	if off, err := h.SitesSvc().SiteOffline(r.Context(), siteID); err != nil || off {
		return signInOffline, "", err
	}
	if susp, reason, err := h.SitesSvc().SiteSuspension(r.Context(), siteID); err != nil || susp {
		return signInSuspended, reason, err
	}
	switch h.decideData(r, siteID, accountapp.VisitSignIn).verdict {
	case accountapp.VisitServe:
		return signInOpen, "", nil
	case accountapp.VisitServiceError:
		return signInLocked, "", errSignInPasscodeRead
	}
	return signInLocked, "", nil
}

var errSignInPasscodeRead = errors.New("site passcode: read failed")

// establishSiteOpen: a company sign-in's site still takes sign-ins and its
// passcode (reason "passcode") lets the browser in.
func (h *Handler) establishSiteOpen(r *http.Request, siteID string) (int, string) {
	refusal, _, _ := h.siteSignIn(r, siteID)
	switch refusal {
	case signInOffline:
		return 403, "site_offline"
	case signInSuspended:
		return 403, "site_suspended"
	case signInLocked:
		return 0, "passcode"
	}
	return 0, ""
}

// logSignInFail records why a sign-in step refused, with a short reason
// code and at most the first 6 characters of the one-use code or state.
func logSignInFail(r *http.Request, route, reason, code string) {
	if len(code) > 6 {
		code = code[:6]
	}
	log.Printf("signin_fail route=%s reason=%s host=%s code=%s sec_fetch=%s/%s/%s",
		route, reason, httpx.HostName(r), code,
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

var signInFailedPage = web.Themed(`<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><!--sh:theme--><title>Sign-in failed</title><style>` + passcodeGateCSS +
	`html:root main.gate a.primary{display:block;box-sizing:border-box;width:100%;height:48px;line-height:48px;margin-top:12px;border-radius:255px 18px 225px 18px / 18px 225px 18px 255px;background:#116b77;color:#fffdf5;font-family:Caveat,"Segoe Print",cursive;font-size:22px;font-weight:700;text-align:center;text-decoration:none}` +
	`html:root[data-theme=dark] main.gate a.primary{background:#9edacf;color:#0b1222}` +
	`</style></head><body><main class="gate"><h1>Sign-in did not finish</h1><p class="lead">__HELP__</p>__ACTION__</main></body></html>`)

// writeSignInFailed is the establish failure page: what happened, and a Try
// again link that restarts sign-in for this site in this browser.
func (h *Handler) writeSignInFailed(w http.ResponseWriter, status int, reason, retry string) {
	action := `<p class="fine">Close this tab and open the site again.</p>`
	if retry != "" {
		action = `<a class="primary" href="` + html.EscapeString(retry) + `">Try again</a><p class="fine">Try again signs you in to this site in this browser.</p>`
	}
	page := strings.NewReplacer("__HELP__", html.EscapeString(signInFailedHelp(reason)), "__ACTION__", action).Replace(signInFailedPage)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(h.Pipeline().HostedStatusPage(page))
}

// logoutVisitor handles POST /v1/visitor/logout: delete this session and clear
// the cookie. Requires X-SH-CSRF: 1 or Content-Type: application/json.
func (h *Handler) logoutVisitor(w http.ResponseWriter, r *http.Request) {
	if !hasVisitorCSRF(r) {
		httpx.WriteJSON(w, http.StatusForbidden, struct {
			Error string `json:"error"`
			Code  string `json:"code"`
		}{Error: "missing CSRF header", Code: "csrf_required"})
		return
	}

	if !httpx.SameOrigin(r) {
		httpx.WriteJSON(w, 403, httpx.ErrorResponse{Error: "sign out on the site's own page"})
		return
	}
	// Privacy guardrail: the one Sign out ends push on that
	// device. auth.js's SH.signOut() already unsubscribes the browser's
	// own PushSubscription and calls /push/unsubscribe itself, but that is
	// a second, client-initiated request; resolve the session being
	// destroyed here too and delete that visitor's push rows for this
	// site server-side, so a blocked or racing client call can never
	// leave one behind. Best-effort: never blocks or fails the sign-out.
	h.deleteVisitorPushOnSignOut(r)
	back, err := h.endSiteSSO(w, r, "/")
	if err != nil {
		httpx.WriteJSON(w, 500, httpx.ErrorResponse{Error: "sign-out unavailable"})
		return
	}
	setVisitorSessionCookie(w, r, "", 0)
	if back != "" {
		httpx.WriteJSON(w, 200, map[string]string{"redirect_url": back})
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) resolveWriterKey(ctx context.Context, key string) (model.User, bool, error) {
	if subtle.ConstantTimeCompare([]byte(key), []byte(h.AdminAPIKey)) == 1 {
		return model.User{ID: h.AdminUserID, Username: "admin", IsAdmin: true}, true, nil
	}
	// A suspended account's key is refused like an unknown one; an expired
	// one is told it expired (accounts.Service.KeyHolder).
	return h.StoreAccounts().KeyHolder(ctx, key, h.Limits.KeyIdleExpiry)
}

// getVisitorMe reports the current site session without extending its lifetime.
func (h *Handler) getVisitorMe(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	siteName := strings.TrimSpace(r.PathValue("sitename"))
	if siteName == "" {
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: "site name is required"})
		return
	}
	if !h.authorizeStateOrigin(w, r, siteName) {
		web.WriteOriginRefused(w)
		return
	}
	siteID, err := h.resolveSiteID(r, siteName)
	if err != nil {
		web.WriteSiteLookupError(w, err)
		return
	}
	// Shared host: sign-in is never offered (one origin for every site).
	if strings.EqualFold(httpx.HostName(r), h.ContentHost) {
		httpx.WriteJSON(w, http.StatusOK, h.sharedHostMe(r, siteID))
		return
	}
	if domain, elsewhere := h.Hosts.LivesOnDomainElsewhere(r.Context(), httpx.HostName(r), siteID); elsewhere {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"signed_in": false, "sign_in_available": false, "code": "use_custom_domain", "domain": domain})
		return
	}
	// The same session storage sees (StrictVisitorSession).
	sess, ok := h.StrictVisitorSession(r, siteID)
	if !ok {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"signed_in": false, "sign_in": "/v1/auth/oauth/providers"})
		return
	}
	resp, err := h.signedInMe(r, siteID, sess)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
}

// sharedHostMe is /me on the shared content host: never signed in. If the
// site has its own domain, say so, so the page can send the visitor there;
// else the site's own address, where visitors sign in.
func (h *Handler) sharedHostMe(r *http.Request, siteID string) map[string]any {
	resp := map[string]any{"signed_in": false, "sign_in_available": false, "code": "custom_domain_required"}
	if info, ok, _ := h.Hosts.SiteOwnAddress(r.Context(), siteID); ok && info.Domain != "" {
		resp["code"] = "use_custom_domain"
		resp["domain"] = info.Domain
		return resp
	}
	if !h.Hosts.Addressing().PersonHostsCanonical() {
		return resp
	}
	if handle, _, name, err := h.SitesSvc().GetSiteOwner(r.Context(), siteID); err == nil && h.Hosts.Addressing().PersonAddressFor(handle, name) {
		resp["address"] = h.Hosts.Addressing().SiteURL(handle, name)
	}
	return resp
}

// signedInMe is /me for a visitor signed in to the site: until when, as
// which address (the same the site's rules see, visitorEmail) and provider,
// and whether they count as the site's owner here.
func (h *Handler) signedInMe(r *http.Request, siteID string, sess model.VisitorSession) (visitorMeResponse, error) {
	expires := sess.ExpiresAt
	if sess.IdleExpiresAt.Before(expires) {
		expires = sess.IdleExpiresAt
	}
	resp := h.visitorSignedInResponse(r, expires, "", "")
	email, provider, err := accountapp.VisitorIdentity(r.Context(), h.Database, sess.UserID)
	resp.Email, resp.Provider = email, provider
	if err != nil {
		return resp, err
	}
	// The same test storage applies (storageOwnerOnSite): the owner's
	// account or a member of the team that holds the site (ActsAsOwner;
	// on Simple Hack the storage bindings provider), on one of the site's
	// own hosts.
	if h.storageOwnHost(r, siteID) {
		owner, oerr := h.storageBindingProvider().OwnerOnSite(r.Context(), siteID, sess.UserID)
		resp.SiteOwner = oerr == nil && owner
	}
	return resp, nil
}

// optionsSiteRoute is the one preflight for the per-site credentialed
// routes (visitor sign-in and /me): only the site's own origins
// (authorizeStateOrigin) get it.
func (h *Handler) optionsSiteRoute(w http.ResponseWriter, r *http.Request) {
	siteName := strings.TrimSpace(r.PathValue("sitename"))
	if siteName == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if !h.authorizeStateOrigin(w, r, siteName) {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-SH-CSRF")
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

func (h *Handler) visitorSignedInResponse(r *http.Request, expires time.Time, email, provider string) visitorMeResponse {
	resp := visitorMeResponse{SignedIn: true, ExpiresAt: expires.Format(time.RFC3339)}
	if !strings.EqualFold(httpx.HostName(r), h.ContentHost) {
		resp.Email, resp.Provider = email, provider
	}
	return resp
}

// StrictVisitorCookie is the visitor session cookie without the fallback
// visitorCookieValue allows: over HTTPS only the __Host- cookie counts, since
// a sibling host under the same parent domain can plant a plain sh_vsess
// cookie (Domain=parent) but can never set a __Host- one.
func StrictVisitorCookie(r *http.Request) string {
	name := VisitorCookieHTTP
	if httpx.IsHTTPS(r) {
		name = VisitorCookieHost
	}
	if c, err := r.Cookie(name); err == nil {
		return c.Value
	}
	return ""
}

// cookieSession is the visitor this request's __Host- cookie names on this
// host (accounts.VisitorSession decides; siteID "" for any site here).
func (h *Handler) cookieSession(r *http.Request, siteID string) (model.VisitorSession, error) {
	return visitorCookieSession(r, h.Database, siteID, h.Limits.KeyIdleExpiry)
}

// visitorCookieSession is the session the request's __Host- visitor cookie
// names on this host (siteID "" for any site here).
func visitorCookieSession(r *http.Request, database *sql.DB, siteID string, idle time.Duration) (model.VisitorSession, error) {
	id, err := hex.DecodeString(StrictVisitorCookie(r))
	if err != nil || len(id) != 32 {
		return model.VisitorSession{}, sql.ErrNoRows
	}
	return accountapp.New(database).VisitorSession(r.Context(), id, httpx.HostName(r), siteID, idle)
}

// StrictVisitorSession returns the visitor signed in to siteID on this host,
// for a same-origin request carrying the __Host- cookie. ok=false otherwise.
func (h *Handler) StrictVisitorSession(r *http.Request, siteID string) (model.VisitorSession, bool) {
	if r.Header.Get(VisitorPassHeader) == "" && !httpx.SameOrigin(r) {
		return model.VisitorSession{}, false
	}
	sess, ok := h.viewerSession(r, siteID)
	if ok && r.Header.Get(VisitorPassHeader) == "" {
		papermetrics.From(r.Context()).Member(r.Context(), sess.UserID)
	}
	return sess, ok
}

// onOwnDomain: this request arrived on the site's own origin — its proven own

// deleteVisitorPushOnSignOut is the server-side half of "Sign out ends push
// on that device": it removes the push devices of the visitor session
// /v1/visitor/logout is about to end, scoped to that session's own family
// (another browser's devices survive; a signed-in-everywhere family's
// reach every site it spans). Best-effort and silent: nothing here may
// block or fail sign-out.
func (h *Handler) deleteVisitorPushOnSignOut(r *http.Request) {
	sess, err := visitorCookieSession(r, h.Database, "", h.Limits.KeyIdleExpiry)
	if err != nil || sess.UserID == "" || sess.SiteID == "" {
		return
	}
	if _, err := notifications.DeletePushSubscriptionsForFamily(r.Context(), h.Database, notifications.PushFamily(sess)); err != nil {
		log.Printf("web push: delete on sign-out for site %s: %v", sess.SiteID, err)
	}
}
