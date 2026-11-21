package accesshttp

import (
	"database/sql"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/web"
)

func (h *AccessHandler) visitorEmailSite(w http.ResponseWriter, r *http.Request) (string, bool) {
	if strings.EqualFold(httpx.HostName(r), h.ContentHost) {
		httpx.WriteError(w, http.StatusBadRequest, "custom_domain_required", "sign in on the site's own address, not the shared address")
		return "", false
	}
	// On a claimed <name>.<SITE_DOMAIN>, sign-in must come from a page on that
	// very host: its siblings are "same-site" to the browser, so without this a
	// page there could sign a visitor in as someone else (login CSRF).
	// The same on a family address (<label>.<family>): all of them, and the
	// family owner's other names, are one "site" to the browser.
	if host := httpx.HostName(r); (h.Hosts.IsPlatformSubdomain(host) || h.Hosts.IsSiteHost(r.Context(), host) || h.Hosts.IsFamilyHost(host)) && !httpx.SameOrigin(r) {
		httpx.WriteJSON(w, http.StatusForbidden, httpx.ErrorResponse{Error: "forbidden"})
		return "", false
	}
	name := strings.TrimSpace(r.PathValue("sitename"))
	if !h.authorizeStateOrigin(w, r, name) {
		web.WriteOriginRefused(w)
		return "", false
	}
	id, err := h.resolveSiteID(r, name)
	if err != nil {
		status, message := http.StatusInternalServerError, "internal server error"
		if errors.Is(err, sql.ErrNoRows) {
			status, message = http.StatusNotFound, "site not found"
		}
		httpx.WriteJSON(w, status, httpx.ErrorResponse{Error: message})
		return "", false
	}
	if h.Hosts.IsApexHost(httpx.HostName(r)) {
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: "not available on this host"})
		return "", false
	}
	if domain, elsewhere := h.Hosts.LivesOnDomainElsewhere(r.Context(), httpx.HostName(r), id); elsewhere {
		httpx.WriteJSON(w, http.StatusBadRequest, map[string]string{"error": "this site signs visitors in on its own domain", "code": "use_custom_domain", "domain": domain})
		return "", false
	}
	if web.RefuseOfflineSite(w, r, h.sitesSvc(), id) || h.refuseSuspendedSiteID(w, r, id) {
		return "", false
	}
	return id, true
}

func (h *AccessHandler) requestVisitorEmail(w http.ResponseWriter, r *http.Request) {
	siteID, ok := h.visitorEmailSite(w, r)
	if !ok {
		return
	}
	if !h.VisitorAuthLimiter.Allow(httpx.ClientIP(r)) {
		writeEmailCodeError(w, http.StatusTooManyRequests, signInRateLimited())
		return
	}
	if h.noEmail {
		// Say so plainly instead of the mailer's 500: this server sends no
		// email, so the page should offer Google (when configured) or tell
		// the visitor.
		httpx.WriteJSON(w, http.StatusServiceUnavailable, signInEmailUnavailable())
		return
	}
	var req authRequest
	if !httpx.DecodeJSON(w, r, 64<<10, &req) {
		return
	}
	address, expires, status, body := issueEmailCode(r.Context(), h.Database, h.Mailer, h.EmailLimiter, req.Email, "", "visitor", sql.NullString{String: siteID, Valid: true}, sql.NullString{}, httpx.ClientIP(r), h.geoBlock, h.Limits.SigninCodeTTL)
	if status != 0 {
		writeEmailCodeError(w, status, body)
		return
	}
	httpx.WriteJSON(w, http.StatusAccepted, authChallengeResponse{Message: SignInCodeSentMessage(address), Email: address, ExpiresIn: expires})
}

func (h *AccessHandler) verifyVisitorEmail(w http.ResponseWriter, r *http.Request) {
	siteID, ok := h.visitorEmailSite(w, r)
	if !ok {
		return
	}
	if r.Header.Get(visitorCSRFHeader) != visitorCSRFValue {
		httpx.WriteError(w, http.StatusForbidden, "csrf_required", "missing CSRF header")
		return
	}
	if !h.VisitorAuthLimiter.Allow(httpx.ClientIP(r)) {
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
	user, created, status, body := VerifyEmailCode(r.Context(), h.Database, h.EmailLimiter, VerifyRequest{Email: req.Email, Code: req.Code}, "visitor", sql.NullString{String: siteID, Valid: true}, h.Ed.Identity().RandomHandles, h.Limits.SigninCodeTTL)
	if status != 0 {
		if status == http.StatusUnauthorized {
			httpx.WriteJSON(w, status, signInUnauthorized(body))
		} else {
			writeEmailCodeError(w, status, body)
		}
		return
	}
	if created {
		accounts.RecordSignup(r.Context(), h.Database, user.ID, model.Signup{Source: model.SignupVisitor, Method: model.SignupMethodEmail})
	}
	// Only a real suspension answers account_suspended; a failed lookup is ours.
	start, err := h.StoreAccounts().StartVisitorSession(r.Context(), user.ID, siteID, httpx.HostName(r), h.Limits.VisitorSessionTTL, h.Limits.VisitorSessionIdle)
	if errors.Is(err, model.ErrAccountSuspended) {
		auth.WriteSuspended(w, "", "")
		return
	}
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	h.VisitorSignedIn(r, siteID, user.ID, start.First)
	setVisitorSessionCookie(w, r, hex.EncodeToString(start.ID), int(h.Limits.VisitorSessionTTL/time.Second))
	w.Header().Set("Cache-Control", "private, no-store")
	httpx.WriteJSON(w, http.StatusOK, h.visitorSignedInResponse(r, start.IdleExpiresAt, user.Username, "email"))
}

// VisitorSignedIn is the one place a visitor sign-in is noted, after the
// session row is written: the first one on a site sends visitor.signed_up,
// and the owner's own sign-in marks the caller's address. It never affects
// the response.
func (h *AccessHandler) VisitorSignedIn(r *http.Request, siteID, userID string, first bool) {
	if first {
		h.emitSiteEvent(sites.SiteEvent{SiteID: siteID, Name: "visitor.signed_up", Data: map[string]any{"visitor": userID}})
	}
	if h.ownerMarks == nil || siteID == "" || userID == "" {
		return
	}
	sites.New(h.Database, nil, nil, 0, sites.Deps{OwnerMarks: sites.OwnerMarkDeps{Mark: h.ownerMarks.Mark}}).NoteOwnerVisitorSignIn(r.Context(), sites.OwnerMarkInput{SiteID: siteID, UserID: userID, IP: httpx.ClientIP(r)})
}
