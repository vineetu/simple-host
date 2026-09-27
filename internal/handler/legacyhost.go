package handler

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"strings"

	db "github.com/vsriram/simple-host/internal/db"
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
func (h *SiteHandler) LegacyHostRedirect(next http.Handler) http.Handler {
	siteDomain := strings.ToLower(strings.TrimSpace(h.siteDomain))
	contentHost := strings.ToLower(strings.TrimSpace(h.contentHost))
	suffix := "." + siteDomain

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := strings.ToLower(r.Host)
		if i := strings.IndexByte(host, ':'); i >= 0 {
			host = host[:i]
		}

		// Not a name-subdomain of ours → leave it alone.
		if siteDomain == "" || host == contentHost || host == siteDomain || !strings.HasSuffix(host, suffix) {
			next.ServeHTTP(w, r)
			return
		}
		label := strings.TrimSuffix(host, suffix)
		if label == "" || label == "www" || strings.Contains(label, ".") {
			next.ServeHTTP(w, r) // www, or a multi-label host (e.g. *.lab)
			return
		}

		retired, err := db.GetRetiredName(r.Context(), h.database, host)
		switch {
		case err == nil:
			if retired.SiteID != "" {
				if target, ok := h.siteAddressFor(r.Context(), retired.SiteID, r.URL.EscapedPath()); ok {
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
				h.mainSiteURL(), "Go to "+siteDomain)
			return
		case !errors.Is(err, sql.ErrNoRows):
			log.Printf("legacy host %s: %v", host, err)
			h.renderServiceError(w)
			return
		}

		// A claimed name whose site is in Recently deleted stays held: it
		// answers as removed (restore brings it back), never another site.
		if held, err := db.DomainHeldByDeletedSite(r.Context(), h.database, host); err != nil {
			log.Printf("legacy host %s: %v", host, err)
			h.renderServiceError(w)
			return
		} else if held {
			h.renderNotFoundPage(w, r, "This site was removed",
				"The site that used this address isn’t on Simple Host any more.",
				h.mainSiteURL(), "Go to "+siteDomain)
			return
		}

		// A deprecated <name>.<siteDomain>. Resolve the oldest owner's handle (the
		// account this host historically served) and 301 to the path URL. Unknown
		// name → send them to the home page rather than a dead end.
		handle, err := db.GetHandleBySiteName(r.Context(), h.database, label)
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

// siteAddressFor is a site's current address with an escaped path appended:
// its proven own domain, else the address every tool hands out for it.
func (h *SiteHandler) siteAddressFor(ctx context.Context, siteID, escapedPath string) (string, bool) {
	rest := strings.TrimLeft(escapedPath, "/")
	if _, _, _, err := db.GetSiteOwner(ctx, h.database, siteID); err != nil {
		return "", false // gone, or in Recently deleted
	}
	if info, has, err := h.siteOwnDomain(ctx, siteID); err == nil && has {
		return "https://" + strings.ToLower(info.Domain) + "/" + rest, true
	}
	handle, _, name, err := db.GetSiteOwner(ctx, h.database, siteID)
	if err != nil || handle == "" {
		return "", false
	}
	if h.personHostsCanonical() && h.personAddressFor(handle, name) {
		return h.siteAddressWithPath(handle, name, "/"+rest), true
	}
	return strings.TrimSuffix(h.SiteURL(handle, name), "/") + "/" + rest, true
}
