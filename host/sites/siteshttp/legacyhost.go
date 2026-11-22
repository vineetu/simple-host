package siteshttp

import (
	"database/sql"
	"errors"
	"log"
	"net/http"
	"strings"

	appsites "github.com/vsriram/simple-host/host/sites"
)

// LegacyHostRedirect answers single-label <name>.<siteDomain> hosts that no
// site serves any more (claimed names and account addresses are answered
// before this). Everything else passes straight through untouched: the
// content host itself, the apex and www, custom domains (a different host
// entirely), and multi-label hosts like x.lab.<siteDomain>.
//
//   - A retired name (legacy_hostnames): the name stays with its site. It 302s
//     to that site's current address, keeping path and query, or says the
//     site was removed when the site is gone. This covers the per-name
//     subdomains of the old model and every claimed name a site released
//     (by switching address, disconnecting it, or being deleted).
//   - Any other name keeps the old per-name model's 301 to the path URL of
//     the oldest site with that name, or the home page when there is none.
//
// Under a moving base (sitebase.go) the same holds for names under the base.
func (h *SiteHandler) LegacyHostRedirect(next http.Handler) http.Handler {
	siteDomain := strings.ToLower(strings.TrimSpace(h.SiteDomain))
	contentHost := strings.ToLower(strings.TrimSpace(h.ContentHost))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := strings.ToLower(r.Host)
		if i := strings.IndexByte(host, ':'); i >= 0 {
			host = host[:i]
		}
		base := siteDomain
		for _, b := range h.Hosts.ServedBases() {
			if strings.HasSuffix(host, "."+b) {
				base = b
				break
			}
		}

		// The hackathon platform has no content host: sites.<domain> is its
		// 404, never a second origin for the signed-in app (hack_mode.go).
		if h.Ed.Routes().HostedEvents && contentHost != "" && host == contentHost {
			h.renderGroupNotFound(w, r)
			return
		}
		// Not a name-subdomain of ours → leave it alone.
		if siteDomain == "" || host == contentHost || host == siteDomain || !strings.HasSuffix(host, "."+base) {
			next.ServeHTTP(w, r)
			return
		}
		label, ok := appsites.LegacyHostLabel(host, base)
		if !ok {
			next.ServeHTTP(w, r) // a reserved platform label, or a multi-label host (e.g. *.lab)
			return
		}
		// The hackathon platform has no per-name past: a name that is no
		// event is our 404 (hack_mode.go).
		if h.Ed.Routes().HostedEvents {
			h.renderGroupNotFound(w, r)
			return
		}

		retired, err := h.SitesSvc().GetRetiredName(r.Context(), host)
		switch {
		case err == nil:
			if retired.SiteID != "" {
				if target, ok := h.Hosts.SiteAddressFor(r.Context(), retired.SiteID, r.URL.EscapedPath()); ok {
					if r.URL.RawQuery != "" {
						target += "?" + r.URL.RawQuery
					}
					w.Header().Set("Cache-Control", "no-store")
					http.Redirect(w, r, target, http.StatusFound)
					return
				}
			}
			h.renderNotFoundPage(w, r, "This site was removed",
				"The site that used this address isn’t on Simple Host any more.",
				h.MainSiteURL(), "Go to "+siteDomain)
			return
		case !errors.Is(err, sql.ErrNoRows):
			log.Printf("legacy host %s: %v", host, err)
			h.renderServiceError(w, r)
			return
		}

		// A claimed name whose site is in Recently deleted stays held: it
		// answers as removed (restore brings it back), never another site.
		if held, err := h.SitesSvc().DomainHeldByDeletedSite(r.Context(), host); err != nil {
			log.Printf("legacy host %s: %v", host, err)
			h.renderServiceError(w, r)
			return
		} else if held {
			h.renderNotFoundPage(w, r, "This site was removed",
				"The site that used this address isn’t on Simple Host any more.",
				h.MainSiteURL(), "Go to "+siteDomain)
			return
		}

		// A deprecated <name>.<siteDomain>. Resolve the oldest owner's handle (the
		// account this host historically served) and 301 to the path URL. Unknown
		// name → send them to the home page rather than a dead end.
		handle, err := h.SitesSvc().GetHandleBySiteName(r.Context(), label)
		if err != nil || handle == "" {
			http.Redirect(w, r, "https://"+siteDomain+"/", http.StatusMovedPermanently)
			return
		}
		target := "https://" + contentHost + "/" + handle + "/" + label + r.URL.Path
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
		http.Redirect(w, r, target, http.StatusMovedPermanently)
	})
}
