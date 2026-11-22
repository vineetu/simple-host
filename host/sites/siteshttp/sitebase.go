package siteshttp

import (
	"net/http"
	"strings"

	"github.com/vsriram/simple-host/host/httpx"
	appsites "github.com/vsriram/simple-host/host/sites"
)

// The base domain of people's addresses.
//
// SITE_DOMAIN is the app: the dashboard, sign-in, the API, emails, the
// content host, free names and the CNAME target. SITE_BASE_DOMAIN is a
// second domain for people's addresses. On most installs they are the same
// domain (the default), and then nothing here applies.
//
// SITE_BASE_MOVE:
//
//   - off: only SITE_DOMAIN serves; a distinct configured base 301s back
//     to the same address on SITE_DOMAIN, keeping path and query.
//   - flat: a person without a certificate of their own gets one-label
//     addresses under the base, <handle>.<base> and <site>--<handle>.<base>
//     (Addressing.FlatFor); everyone else keeps SITE_DOMAIN. A host under
//     the base that is not one of those goes to the same labels under
//     SITE_DOMAIN, and a flat person's page under SITE_DOMAIN goes to the
//     base (Hosts.BaseTarget). Both are 302s (who is flat is decided per
//     person, so no browser may keep the answer), for page GETs only: the
//     API and other methods there get a 404.

func (h *SiteHandler) HandoutBase() string { return h.Hosts.Addressing().HandoutBase() }

func (h *SiteHandler) ServedBases() []string { return h.Hosts.ServedBases() }

// SiteBaseHosts answers what only a second base adds, in front of
// everything else: the base's apex, www and reserved names go to the app,
// and a host under the wrong base goes to the right one. When off, a
// distinct configured base redirects back to SITE_DOMAIN.
func (h *SiteHandler) SiteBaseHosts(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a := h.Hosts.Addressing()
		host := strings.TrimSuffix(httpx.HostName(r), ".")
		// A paused move keeps already-shared base links working, including API
		// paths, without serving content or requesting certificates there.
		if a.BaseMove == appsites.BaseMoveOff && a.SiteBase != "" && h.SiteDomain != "" && !strings.EqualFold(a.SiteBase, h.SiteDomain) {
			if host == a.SiteBase || strings.HasSuffix(host, "."+a.SiteBase) {
				target := strings.TrimSuffix(host, a.SiteBase) + strings.ToLower(h.SiteDomain)
				http.Redirect(w, r, "https://"+target+r.URL.RequestURI(), http.StatusMovedPermanently)
				return
			}
		}
		if !a.BaseSplit() {
			next.ServeHTTP(w, r)
			return
		}
		// The base is only ever people's addresses: its apex, www and the
		// platform's own names are the app's.
		if host == a.SiteBase || host == "www."+a.SiteBase || appsites.PlatformHostTarget(host, a.SiteBase, strings.ToLower(h.SiteDomain)) != "" {
			http.Redirect(w, r, "https://"+strings.ToLower(h.SiteDomain)+"/", http.StatusMovedPermanently)
			return
		}
		// Only pages are redirected (redirectPage): browsers do not follow a
		// redirect on a CORS preflight, and a redirect turns a POST into a GET.
		if target, ok := h.Hosts.BaseTarget(r.Context(), host); ok {
			h.redirectPage(w, r, "https://"+target+r.URL.EscapedPath())
			return
		}
		next.ServeHTTP(w, r)
	})
}
