package siteshttp

import (
	"net/http"
	"strings"

	"github.com/vsriram/simple-host/host/httpx"
	appsites "github.com/vsriram/simple-host/host/sites"
)

// The base domain of people's addresses (owner decision 2026-09-28).
//
// SITE_DOMAIN is the app: the dashboard, sign-in, the API, emails, the
// content host and the CNAME target. SITE_BASE_DOMAIN is what every address a
// person or site gets lives under: <handle>.<base>, <site>.<handle>.<base>
// and free <name>.<base> names. On most installs they are the same domain
// (the default), and then every function here reduces to today's single
// domain. The hosted move is paused (owner decision 2026-10-09):
// simple-host.app is the only address handed out.
//
// SITE_BASE_MOVE says how far the move has gone:
//
//   - off: only SITE_DOMAIN serves; a distinct configured base 301s back
//     to the same address on SITE_DOMAIN, keeping path and query.
//   - serve: both domains answer; addresses handed out stay on SITE_DOMAIN.
//   - canonical: base addresses are handed out; SITE_DOMAIN ones still answer.
//   - redirect: SITE_DOMAIN addresses 302 to the same labels, path and query
//     under the base (never /v1/, which old pages keep calling).
//   - permanent: the same with 301.
//
// The label namespace is one across both domains (the same handle, site and
// claimed names), so the redirect is a pure suffix swap and can be permanent.

type baseMoveMode = appsites.BaseMove

const (
	baseMoveOff       = appsites.BaseMoveOff
	baseMoveServe     = appsites.BaseMoveServe
	baseMoveCanonical = appsites.BaseMoveCanonical
	baseMoveRedirect  = appsites.BaseMoveRedirect
	baseMovePermanent = appsites.BaseMovePermanent
)

// HandoutBase is handoutBase for main (the namespace and analytics wiring).
func (h *SiteHandler) HandoutBase() string { return h.Hosts.Addressing().HandoutBase() }

// ServedBases is servedBases for main.
func (h *SiteHandler) ServedBases() []string { return h.Hosts.ServedBases() }

// legacyRedirects: SITE_DOMAIN addresses redirect to the base.
func (h *SiteHandler) legacyRedirects() bool {
	return h.Hosts.Addressing().BaseSplit() && h.Hosts.Addressing().BaseMove >= baseMoveRedirect
}

// certRequestBases are the bases a person's certificate is requested under:
// SITE_DOMAIN until the base is handed out (its certificates then only
// renew), and the base from serve on.
func (h *SiteHandler) certRequestBases() []string {
	var out []string
	if h.Hosts.Addressing().SiteCertDir != "" && !(h.Hosts.Addressing().BaseSplit() && h.Hosts.Addressing().BaseMove >= baseMoveCanonical) {
		out = append(out, strings.ToLower(h.SiteDomain))
	}
	if h.Hosts.Addressing().BaseSplit() && h.Hosts.Addressing().BaseCertDir != "" {
		out = append(out, h.Hosts.Addressing().SiteBase)
	}
	return out
}

// SiteBaseHosts answers what only the move adds, in front of everything else:
// the base's apex, www and reserved names go to the app, and in redirect and
// permanent mode an address under SITE_DOMAIN goes to the same address under
// the base. When off, a distinct configured base redirects back to SITE_DOMAIN.
func (h *SiteHandler) SiteBaseHosts(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A paused move keeps already-shared base links working, including API
		// paths, without serving content or requesting certificates there.
		if h.Hosts.Addressing().BaseMove == baseMoveOff && h.Hosts.Addressing().SiteBase != "" && h.SiteDomain != "" &&
			!strings.EqualFold(h.Hosts.Addressing().SiteBase, h.SiteDomain) {
			host := strings.TrimSuffix(httpx.HostName(r), ".")
			if host == h.Hosts.Addressing().SiteBase || strings.HasSuffix(host, "."+h.Hosts.Addressing().SiteBase) {
				target := strings.TrimSuffix(host, h.Hosts.Addressing().SiteBase) + strings.ToLower(h.SiteDomain)
				http.Redirect(w, r, "https://"+target+r.URL.RequestURI(), http.StatusMovedPermanently)
				return
			}
		}
		if !h.Hosts.Addressing().BaseSplit() {
			next.ServeHTTP(w, r)
			return
		}
		host := httpx.HostName(r)
		base := h.Hosts.Addressing().SiteBase
		// The base is only ever people's addresses: its apex, www and the
		// platform's own names are the app's.
		if host == base || host == "www."+base {
			http.Redirect(w, r, "https://"+strings.ToLower(h.SiteDomain)+"/", http.StatusMovedPermanently)
			return
		}
		if appsites.PlatformHostTarget(host, base, strings.ToLower(h.SiteDomain)) != "" {
			http.Redirect(w, r, "https://"+strings.ToLower(h.SiteDomain)+"/", http.StatusMovedPermanently)
			return
		}
		if h.legacyRedirects() {
			if target, status, ok := h.legacyBaseTarget(r); ok {
				if status == http.StatusFound {
					w.Header().Set("Cache-Control", "no-store")
				}
				http.Redirect(w, r, target, status)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// legacyBaseTarget is where a request on an address under SITE_DOMAIN goes
// under the base: the same labels, escaped path and query, 302 in redirect
// mode and 301 in permanent. ok=false for anything that stays: /v1/ (browsers
// do not follow a redirect on a CORS preflight, and a 301 turns a POST into a
// GET), the content host, the CNAME target, www, reserved names and every
// other shape of host. A site host whose owner has no certificate under the
// base yet goes to its person path there instead, always with a 302.
func (h *SiteHandler) legacyBaseTarget(r *http.Request) (string, int, bool) {
	if strings.HasPrefix(r.URL.Path, "/v1/") || strings.HasPrefix(r.URL.Path, "/internal/") {
		return "", 0, false
	}
	host := strings.TrimSuffix(httpx.HostName(r), ".")
	legacy := strings.ToLower(h.SiteDomain)
	if !strings.HasSuffix(host, "."+legacy) || strings.EqualFold(host, h.ContentHost) ||
		strings.EqualFold(host, strings.TrimSuffix(h.CNAMETarget, ".")) {
		return "", 0, false
	}
	parts, ok := appsites.LegacyBaseLabels(host, legacy)
	if !ok {
		return "", 0, false
	}
	labels := strings.Join(parts, ".")
	escaped := r.URL.EscapedPath()
	if !strings.HasPrefix(escaped, "/") {
		escaped = "/" + escaped
	}
	query := ""
	if r.URL.RawQuery != "" {
		query = "?" + r.URL.RawQuery
	}
	// A site whose main address is not a Simple Host one (its own domain,
	// or its address family) goes straight there: one hop, and a 302, since
	// that address can change.
	if len(parts) == 2 && h.Database != nil {
		if u, err := h.siteFacts().HostOwner(r.Context(), parts[1], parts[0]); err == nil {
			if site, err := h.siteFacts().AddressedSite(r.Context(), u.ID, parts[0]); err == nil {
				if own, has, err := h.Hosts.SiteOwnAddress(r.Context(), site.ID); err == nil && has {
					return "https://" + strings.ToLower(own.Domain) + escaped + query, http.StatusFound, true
				}
			}
		}
	}
	if len(parts) == 2 && !h.Hosts.Addressing().SiteCertReadyOn(parts[1], h.Hosts.Addressing().SiteBase) {
		return "https://" + h.Hosts.Addressing().PersonHostOn(parts[1], h.Hosts.Addressing().SiteBase) + "/" + parts[0] + escaped + query, http.StatusFound, true
	}
	status := http.StatusFound
	if h.Hosts.Addressing().BaseMove == baseMovePermanent {
		status = http.StatusMovedPermanently
	}
	return "https://" + labels + "." + h.Hosts.Addressing().SiteBase + escaped + query, status, true
}

// moveStatus is the status of a redirect from an old address to a person
// address: 302 until the move is permanent, then 301 (the content host's
// person-address redirects; docs/designs/per-person-subdomains-nginx.md step 3).
func (h *SiteHandler) moveStatus() int {
	if h.Hosts.Addressing().BaseSplit() && h.Hosts.Addressing().BaseMove == baseMovePermanent {
		return http.StatusMovedPermanently
	}
	return http.StatusFound
}
