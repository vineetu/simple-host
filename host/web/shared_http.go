package web

import (
	"errors"
	operations "github.com/vsriram/simple-host/host/ops"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sites"
	appsites "github.com/vsriram/simple-host/host/sites"
	"golang.org/x/net/publicsuffix"
)

// HTTP answers and request rules the site, access and data routes share.

func WriteSiteSuspended(w http.ResponseWriter, reason string) {
	httpx.WriteJSON(w, http.StatusForbidden, map[string]string{
		"error":  "this site has been taken down by the operator; changes are refused until it is restored",
		"code":   "site_suspended",
		"reason": reason,
	})
}

// RefuseSuspendedSite writes the refusal and returns true when the site (or
// its owner's account) is suspended.
func RefuseSuspendedSite(w http.ResponseWriter, site model.Site) bool {
	if !site.Suspended() {
		return false
	}
	WriteSiteSuspended(w, site.SuspendedReason())
	return true
}

// SuspendedBy says which switch holds a site down: "site", "account", or "".
func SuspendedBy(s model.Site) string {
	switch {
	case s.SiteSuspended:
		return "site"
	case s.OwnerSuspended:
		return "account"
	}
	return ""
}

// RefuseOfflineSite answers site_offline (and reports true) when the site is
// offline: offline means offline, so visitors' reads and writes of its saved
// data and visitor sign-in all stop. The owner's key is let through by the
// callers, not here.
func RefuseOfflineSite(w http.ResponseWriter, r *http.Request, svc *sites.Service, siteID string) bool {
	off, err := svc.SiteOffline(r.Context(), siteID)
	if err != nil {
		httpx.WriteInternal(w)
		return true
	}
	if off {
		WriteSiteOffline(w)
		return true
	}
	return false
}

func WriteSiteOffline(w http.ResponseWriter) {
	httpx.WriteJSON(w, http.StatusForbidden, map[string]string{
		"error": "this site is offline; its owner has paused it, and it takes no saves, reads or sign-ins until it is back online",
		"code":  "site_offline",
	})
}

// WriteAccessError answers an Access refusal at its status and code, and
// anything else as a failed site change (WriteAccessMutationError).
func WriteAccessError(w http.ResponseWriter, err error) {
	var refusal *accounts.Refusal
	if errors.As(err, &refusal) {
		httpx.WriteJSON(w, refusal.Status, httpx.ErrorResponse{Error: refusal.Message, Code: refusal.Code})
		return
	}
	WriteAccessMutationError(w, err)
}

func WriteAccessMutationError(w http.ResponseWriter, err error) {
	var marker *appsites.Error
	if errors.As(err, &marker) && marker.Code == "access_marker" {
		httpx.WriteJSON(w, 500, httpx.ErrorResponse{Error: marker.Message})
		return
	}
	WriteSiteLookupError(w, err)
}

// WriteOriginRefused is the 403 for a request from a page that is not one of
// the site's own addresses (or that names no page and holds no key of the
// site's owner).
func WriteOriginRefused(w http.ResponseWriter) {
	httpx.WriteJSON(w, http.StatusForbidden, httpx.ErrorResponse{
		Error: "this request comes from a page that is not one of this site's own addresses, so it cannot use the site's saved data. " +
			"A page saves from the site itself; an agent or script sends the site owner's X-API-Key and no Origin header",
		Code: "origin_not_allowed",
	})
}

// AccountAuthOnly keeps account sign-in (the emailed code and Google flows
// that end in an account API key) on the app's own address. Every other host
// this server answers on (a site's address, a person's address, an address
// family, a connected domain, the shared content host) serves hosted pages,
// and a hosted page must never be able to mint an account key: a visitor there
// signs in with the visitor routes instead, which issue a cookie scoped to
// that one site.
//
// Two checks: the request's Host must be the public base host, and when a
// browser sends an Origin it must be exactly the public base origin (scheme,
// host and port, with default ports dropped), so a page on a site cannot call
// the apex cross-origin either; the open CORS policy would otherwise let it.
// A loopback host (a bare local run, a test fixture) is allowed with no Origin
// or its own, since no hosted page shares that address. With no usable public
// base URL nothing is refused.
func AccountAuthOnly(publicBaseURL string, next http.Handler) http.Handler {
	base := PublicBaseOrigin(publicBaseURL)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if base != nil && !AccountAuthAllowed(r, base) {
			WriteAccountAuthUnavailable(w, base.Hostname())
			return
		}
		next.ServeHTTP(w, r)
	})
}

func AccountAuthAllowed(r *http.Request, base *url.URL) bool {
	host := httpx.HostName(r)
	loopback := IsLoopbackHost(host)
	if host != strings.ToLower(base.Hostname()) && !loopback {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		// No Origin: an agent or a same-origin navigation, not a cross-origin
		// page script.
		return true
	}
	if origin == "null" {
		// An opaque origin (a sandboxed frame): foreign.
		return false
	}
	o, err := url.Parse(origin)
	if err != nil || o.Hostname() == "" {
		return false
	}
	if loopback {
		// A bare local run: the page must come from this very server (same
		// scheme, host and port), not from another local app.
		scheme := "http"
		if httpx.IsHTTPS(r) {
			scheme = "https"
		}
		own, err := url.Parse(scheme + "://" + r.Host)
		return err == nil && normalizedOrigin(o) == normalizedOrigin(own)
	}
	return normalizedOrigin(o) == normalizedOrigin(base)
}

// PublicBaseOrigin parses PUBLIC_BASE_URL into an origin. A schemeless value
// is read as https; an unparseable or empty one gives nil (nothing gated).
func PublicBaseOrigin(publicBaseURL string) *url.URL {
	s := strings.TrimSpace(publicBaseURL)
	if s == "" {
		return nil
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil || u.Hostname() == "" {
		return nil
	}
	return u
}

// WithVisitorPath is r with nginx's or Caddy's /internal/passcode rewrite
// taken off its URL: the path the visitor asked for, query kept.
func WithVisitorPath(r *http.Request) *http.Request {
	escaped := StripPasscodeRewrite(r.URL.EscapedPath())
	path := StripPasscodeRewrite(r.URL.Path)
	if escaped == r.URL.EscapedPath() && path == r.URL.Path {
		return r
	}
	r2 := r.Clone(r.Context())
	r2.URL.Path = path
	r2.URL.RawPath = ""
	if escaped != (&url.URL{Path: path}).EscapedPath() {
		r2.URL.RawPath = escaped
	}
	r2.RequestURI = r2.URL.RequestURI()
	return r2
}

// StoreSpan formats durations for messages and emails.
func StoreSpan(d time.Duration) string {
	n, unit := int(d/time.Second), "second"
	switch {
	case d >= 24*time.Hour && d%(24*time.Hour) == 0:
		n, unit = int(d/(24*time.Hour)), "day"
	case d >= time.Hour && d%time.Hour == 0:
		n, unit = int(d/time.Hour), "hour"
	case d >= time.Minute && d%time.Minute == 0:
		n, unit = int(d/time.Minute), "minute"
	}
	if n == 1 {
		return "one " + unit
	}
	return strconv.Itoa(n) + " " + unit + "s"
}

// passcodeRewritePrefix is where nginx and Caddy send a protected site's
// request (rewrite ^ /internal/passcode$uri): the visitor asked for the rest.
const passcodeRewritePrefix = "/internal/passcode"

// IsApexDomain reports whether domain is its own registrable apex (eTLD+1),
// i.e. it has no subdomain label (agent-deploy.dev -> true, x.agent-deploy.dev -> false).
func IsApexDomain(domain string) bool {
	etld1, err := publicsuffix.EffectiveTLDPlusOne(domain)
	return err == nil && etld1 == domain
}

// AdminSiteMismatch reports a request from asSiteOwner whose site, found again
// by owner and name, is not the one the admin named by id.
func AdminSiteMismatch(r *http.Request, siteID string) bool {
	want, ok := r.Context().Value(AdminSiteKey{}).(string)
	return ok && want != siteID
}

// A site's "access" as the site routes answer it: anyone with the link, or
// only its named viewers.
const (
	AccessAnyone   = "anyone"
	AccessSpecific = "specific"
)

// SiteAccessOf is a site's access as the site routes report it.
func SiteAccessOf(s model.Site) string {
	if s.NamedViewers {
		return AccessSpecific
	}
	return AccessAnyone
}

func SignupGroupOf(source string) string { return operations.SignupGroupOf(source, CohortSources()) }

func SignupGroups(present map[string]bool) []SignupGroup {
	return operations.SignupGroups(present, CohortSources())
}

// keepVersions is the instance retention (0 keeps all). Hosted Simple Host
// sets 4; self-hosters retain the existing unset default and Simple Hack sets 2.
// Account and site defaults are resolved in accountRetention.
var DefaultKeepVersions = 0
