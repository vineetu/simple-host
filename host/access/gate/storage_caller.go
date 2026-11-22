package gate

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"

	accounts "github.com/vsriram/simple-host/host/access"

	"github.com/vsriram/simple-host/host/access/auth"
	appstorage "github.com/vsriram/simple-host/host/data"
	notifications "github.com/vsriram/simple-host/host/data/notify"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/papermetrics"
	"github.com/vsriram/simple-host/host/web"
)

// StorageCaller is the data routes' caller resolution as middleware: it
// resolves the site and who is calling (storageCaller) or writes the refusal,
// and hands the call to the data route on the request (appstorage.WithCall).
func (h *Handler) StorageCaller(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, ok := h.storageCaller(w, r)
		if !ok {
			return
		}
		next.ServeHTTP(w, r.WithContext(appstorage.WithCall(r.Context(), c)))
	})
}

// storageCaller answers, once per request, which site a data route is for
// and who is calling it: the owner's key or connector token (owner rights),
// an AI visitor's pass, or a browser on the site's own address with its
// visitor session. It writes the refusal and reports false when the request
// stops here: no site, a suspended or offline site, a key that is not the
// owner's, or a browser on another address.
func (h *Handler) storageCaller(w http.ResponseWriter, r *http.Request) (appstorage.Call, bool) {
	c, ok := h.storageSiteFor(w, r)
	if !ok {
		return c, false
	}
	if !h.storageCredential(w, r, &c) {
		return c, false
	}
	if !c.Owner && web.RefuseOfflineSite(w, r, h.SitesSvc(), c.SiteID) {
		return c, false
	}
	if !c.Owner {
		h.StorageVisitor(r, &c)
	}
	c.CSRFOK = r.Header.Get("X-SH-CSRF") == "1"
	w.Header().Set("Cache-Control", "no-store")
	return c, true
}

// storageSiteFor resolves the {sitename} of a data route to a live site.
func (h *Handler) storageSiteFor(w http.ResponseWriter, r *http.Request) (appstorage.Call, bool) {
	name := strings.ToLower(strings.TrimSpace(r.PathValue("sitename")))
	if name == "" {
		httpx.WriteError(w, 400, "invalid_site", "site name is required")
		return appstorage.Call{}, false
	}
	id, err := h.resolveWriteSiteID(r, name)
	if err != nil {
		httpx.WriteError(w, 404, "site_not_found", "site not found")
		return appstorage.Call{}, false
	}
	_, ownerID, siteName, err := h.SitesSvc().GetSiteOwner(r.Context(), id)
	if err != nil {
		httpx.WriteError(w, 404, "site_not_found", "site not found")
		return appstorage.Call{}, false
	}
	if h.refuseSuspendedSiteID(w, r, id) {
		return appstorage.Call{}, false
	}
	papermetrics.RecordSite(h.Ed, r, ownerID, siteName, nil)
	return appstorage.Call{SiteID: id, OwnerID: ownerID, SiteName: siteName}, true
}

// storageCredential classifies the caller: an AI visitor's pass for this
// site, the owner's (or the admin's) key, or a browser request this site's
// own address made. A key that is not the owner's is refused, never treated
// as a visitor.
func (h *Handler) storageCredential(w http.ResponseWriter, r *http.Request, c *appstorage.Call) bool {
	if r.Header.Get(VisitorPassHeader) != "" {
		if _, ok := h.InternalVisitorSession(r, c.SiteID); !ok {
			httpx.WriteError(w, 403, "forbidden", "visitor pass does not cover this site")
			return false
		}
		c.VisitorAI = true
		return true
	}
	key := r.Header.Get("X-API-Key")
	if key == "" {
		if !h.storageSameSiteRequest(r, c.SiteID) {
			httpx.WriteError(w, 403, "forbidden", "use this site's own address")
			return false
		}
		return true
	}
	u, ok, err := h.resolveWriterKey(r.Context(), key)
	if errors.Is(err, model.ErrKeyExpired) || errors.Is(err, model.ErrKeyExpiredIdle) {
		msg, code := auth.ExpiredKeyMessage(err, u.KeyExpiresAt, h.Limits.KeyIdleExpiry)
		httpx.WriteError(w, 401, code, msg)
		return false
	}
	if err != nil {
		httpx.WriteError(w, 500, "internal_error", "internal server error")
		return false
	}
	papermetrics.From(r.Context()).User(r.Context(), &u)
	if !ok || u.DeployOnly() || (u.ID != c.OwnerID && !u.IsAdmin) {
		httpx.WriteError(w, 403, "forbidden", "owner credential required")
		return false
	}
	c.Owner, c.AccountID = true, u.ID
	if u.Member != nil {
		// A file link made for a member or editor works only while they may
		// still act for the site (fileLinkScopeCurrent).
		c.LinkScope = "as:" + u.Member.PersonID
	}
	return true
}

// storageVisitor records what Access knows about a caller who is not the
// owner: their visitor session on this site, whether they are on the site's
// own address, and whether the passcode and named-viewers rules let them in.
func (h *Handler) StorageVisitor(r *http.Request, c *appstorage.Call) {
	if sess, ok := h.StrictVisitorSession(r, c.SiteID); ok && sess.UserID != "" {
		c.VisitorID = sess.UserID
		c.VisitorSessionID, c.VisitorSessionUserID = sess.ID, sess.UserID
		c.VisitorFamily = notifications.PushFamily(sess)
		c.OnOwnAddress = !c.VisitorAI && h.StorageOwnAddress(r, c.SiteID)
	}
	c.PasscodeOK, c.ViewersOK = h.storageGates(r, c.SiteID)
}

// storageGates is the passcode rule (accounts.PasscodeAdmits) and the
// named-viewers rule (accounts.ViewersAdmit) for a caller storage classed as
// a visitor, from one passcode row. The named-viewers rule is decided from the
// visitor alone: a key the request also carries never names them a viewer
// (the passcode still takes it, as it always has). A viewers list that cannot
// be read refuses the viewers rule only (site_private, failing closed); a row
// that cannot be read refuses both.
func (h *Handler) storageGates(r *http.Request, siteID string) (passcode, viewers bool) {
	row, err := h.SitesSvc().GetSitePasscode(r.Context(), siteID)
	if errors.Is(err, sql.ErrNoRows) {
		return true, true
	}
	if err != nil {
		return false, false
	}
	visitor, _, _ := h.visitorFacts(r, row, accounts.VisitData, false)
	passcode = accounts.PasscodeAdmits(accounts.VisitData, row, visitor)
	if !passcode {
		who := visitor
		who.OwnerKey, who.AdminKey = h.KeyActsFor(r, row)
		passcode = accounts.PasscodeAdmits(accounts.VisitData, row, who)
	}
	return passcode, visitor.ListErr == nil && accounts.ViewersAdmit(accounts.VisitData, row, visitor) == accounts.VisitServe
}

// storageSameSiteRequest: a browser's data request without a credential is
// limited to the site's own address; a direct unauthenticated request only
// reads, and only when the host itself resolves to that site.
func (h *Handler) storageSameSiteRequest(r *http.Request, siteID string) bool {
	host := httpx.HostName(r)
	if h.Hosts.IsApexHost(host) || strings.EqualFold(host, h.ContentHost) {
		return false
	}
	id, err := h.ResolveSiteIDScoped(r, r.PathValue("sitename"))
	if err != nil || id != siteID {
		return false
	}
	if r.Header.Get("Origin") != "" || r.Header.Get("Referer") != "" {
		return httpx.SameOrigin(r)
	}
	return r.Method == http.MethodGet || r.Method == http.MethodHead
}

// StorageOwnAddress is the address half of D1's owner-on-site rule: the
// page is on one of the site's own hosts (its site host, a family address or
// its own domain; never the apex or the shared content host), not framed by
// another origin (auth.js sends X-SH-Framed: 1 there) and not a preview of
// an earlier version.
func (h *Handler) StorageOwnAddress(r *http.Request, siteID string) bool {
	return r.Header.Get("X-SH-Framed") == "" && !h.previewRefererFor(r, siteID) && h.storageOwnHost(r, siteID)
}

// storageOwnHost: the request's host is one of the site's own hosts. A
// person host is never a site's own, except on the event-sites platform,
// where the event's host is the holding account's person host and serves
// the event's custom website.
func (h *Handler) storageOwnHost(r *http.Request, siteID string) bool {
	host := httpx.HostName(r)
	if host == "" || h.Hosts.IsApexHost(host) || strings.EqualFold(host, h.ContentHost) {
		return false
	}
	if _, person := h.Hosts.PersonHostOwner(r.Context(), host); person && !h.Ed.Storage().EventSites {
		return false
	}
	id, err := h.ResolveSiteIDScoped(r, r.PathValue("sitename"))
	return err == nil && id == siteID
}
