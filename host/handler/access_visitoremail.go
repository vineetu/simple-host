package handler

import (
	"database/sql"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
)

func (h *SiteHandler) visitorEmailSite(w http.ResponseWriter, r *http.Request) (string, bool) {
	if strings.EqualFold(requestHostName(r), h.contentHost) {
		writeProductErr(w, http.StatusBadRequest, "custom_domain_required", "sign in on the site's own address, not the shared address")
		return "", false
	}
	// On a claimed <name>.<SITE_DOMAIN>, sign-in must come from a page on that
	// very host: its siblings are "same-site" to the browser, so without this a
	// page there could sign a visitor in as someone else (login CSRF).
	// The same on a family address (<label>.<family>): all of them, and the
	// family owner's other names, are one "site" to the browser.
	if host := requestHostName(r); (h.isPlatformSubdomainHost(host) || h.isSiteHostName(r.Context(), host) || h.isFamilyHostName(host)) && !sameOriginRequest(r) {
		writeJSON(w, http.StatusForbidden, errorResponse{Error: "forbidden"})
		return "", false
	}
	name := strings.TrimSpace(r.PathValue("sitename"))
	if !h.authorizeStateOrigin(w, r, name) {
		writeOriginRefused(w)
		return "", false
	}
	id, err := h.resolveSiteID(r, name)
	if err != nil {
		status, message := http.StatusInternalServerError, "internal server error"
		if errors.Is(err, sql.ErrNoRows) {
			status, message = http.StatusNotFound, "site not found"
		}
		writeJSON(w, status, errorResponse{Error: message})
		return "", false
	}
	if h.isVisitorApexHost(requestHostName(r)) {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "not available on this host"})
		return "", false
	}
	if domain, elsewhere := h.livesOnDomainElsewhere(r, id); elsewhere {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "this site signs visitors in on its own domain", "code": "use_custom_domain", "domain": domain})
		return "", false
	}
	if h.refuseOffline(w, r, id) || h.refuseSuspendedSiteID(w, r, id) {
		return "", false
	}
	return id, true
}

func (h *SiteHandler) requestVisitorEmail(w http.ResponseWriter, r *http.Request) {
	siteID, ok := h.visitorEmailSite(w, r)
	if !ok {
		return
	}
	if !h.visitorAuthLimiter.Allow(clientIP(r)) {
		writeEmailCodeError(w, http.StatusTooManyRequests, signInRateLimited())
		return
	}
	if h.noEmail {
		// Say so plainly instead of the mailer's 500: this server sends no
		// email, so the page should offer Google (when configured) or tell
		// the visitor.
		writeJSON(w, http.StatusServiceUnavailable, signInEmailUnavailable())
		return
	}
	var req authRequest
	if !httpx.DecodeJSON(w, r, 64<<10, &req) {
		return
	}
	address, expires, status, body := issueEmailCode(r.Context(), h.database, h.mailer, h.emailLimiter, req.Email, "", "visitor", sql.NullString{String: siteID, Valid: true}, sql.NullString{}, clientIP(r), h.geoBlock, h.authTokenTTL())
	if status != 0 {
		writeEmailCodeError(w, status, body)
		return
	}
	writeJSON(w, http.StatusAccepted, authChallengeResponse{Message: signInCodeSentMessage(address), Email: address, ExpiresIn: expires})
}

func (h *SiteHandler) verifyVisitorEmail(w http.ResponseWriter, r *http.Request) {
	siteID, ok := h.visitorEmailSite(w, r)
	if !ok {
		return
	}
	if r.Header.Get(visitorCSRFHeader) != visitorCSRFValue {
		writeProductErr(w, http.StatusForbidden, "csrf_required", "missing CSRF header")
		return
	}
	if !h.visitorAuthLimiter.Allow(clientIP(r)) {
		writeEmailCodeError(w, http.StatusTooManyRequests, signInRateLimited())
		return
	}
	var req struct {
		Email string `json:"email"`
		Code  string `json:"code"`
	}
	if !httpx.DecodeJSON(w, r, 64<<10, &req) {
		return
	}
	user, created, status, body := verifyEmailCode(r.Context(), h.database, h.emailLimiter, verifyRequest{Email: req.Email, Code: req.Code}, "visitor", sql.NullString{String: siteID, Valid: true}, h.ed.Identity().RandomHandles, h.authTokenTTL())
	if status != 0 {
		if status == http.StatusUnauthorized {
			writeJSON(w, status, signInUnauthorized(body))
		} else {
			writeEmailCodeError(w, status, body)
		}
		return
	}
	if created {
		recordSignup(r.Context(), h.database, user.ID, model.Signup{Source: model.SignupVisitor, Method: model.SignupMethodEmail})
	}
	// Only a real suspension answers account_suspended; a failed lookup is ours.
	if susp, serr := h.storeAccounts().UserSuspended(r.Context(), user.ID); serr != nil {
		writeInternal(w)
		return
	} else if susp {
		auth.WriteSuspended(w, "", "")
		return
	}
	id, err := randomRaw(32)
	if err != nil {
		writeInternal(w)
		return
	}
	now := time.Now()
	expires, idle := now.Add(h.visitorSessionTTL()), now.Add(h.visitorSessionIdle())
	first, err := h.storeAccounts().InsertVisitorSession(r.Context(), id, user.ID, siteID, requestHostName(r), expires, idle)
	if err != nil {
		writeInternal(w)
		return
	}
	h.visitorSignedIn(r, siteID, user.ID, first)
	setVisitorSessionCookie(w, r, hex.EncodeToString(id), h.visitorCookieMaxAge())
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, http.StatusOK, h.visitorSignedInResponse(r, idle, user.Username, "email"))
}
