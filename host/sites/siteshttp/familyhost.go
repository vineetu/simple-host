package siteshttp

import (
	"context"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/sites/families"
)

// Address families.
//
// An account connects *.<suffix> once (model.AddressFamily). Every site of that
// account named <prefix><label> then answers at <label>.<suffix>, where
// <prefix> is the family's optional site-name prefix. Nothing else of the
// account, and nothing of any other account, is ever reachable there: the
// host alone names the account (the family's owner) and the site.
//
// A family serves only once it is verified (its TXT record, or the admin's
// proof exemption, plus the wildcard pointing here) AND its certificate is
// live: in Release 1 the operator's wildcard certificate (cert_mode
// wildcard, lineage cert_name), which the root issuer (deploy/family-certs)
// checks before it writes the family's nginx server and its ready marker
// ($ADDRESS_FAMILY_CERT_DIR/ready/<suffix>). Until then no address under it
// is answered, handed out or redirected to.
//
// Serving: nginx serves files straight from disk (families/<suffix> ->
// by-id/<user>) with the take-down, offline, passcode and lives-elsewhere
// markers, and hands /v1/, /internal/ and misses to the app, where
// FamilyHosts answers them. A family address is the site's main address
// (canonical, the default) unless the site has a custom domain or a claimed
// name: ranking is own domain > the most specific canonical family > the
// site host. Every family address of a site keeps working either way.

func (h *SiteHandler) startAddressFamilies(ctx context.Context) {
	o := h.siteLoop("sites.address_families")
	initialized := make(chan struct{})
	o.Go(ctx, func(ctx context.Context) {
		o.tracker.Attempt()
		h.Hosts.RefreshFamilies(sites.WithWorkerReport(ctx, o.tracker))
		close(initialized)
		t := time.NewTicker(h.Limits.FamilyCacheTTL)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				o.tracker.Attempt()
				h.Hosts.RefreshFamilies(sites.WithWorkerReport(ctx, o.tracker))
			}
		}
	})
	// The first cache load finishes before this returns; the work still runs
	// in the Runner so Stop cancels it and Wait includes it.
	if o.Status()[0].Running {
		<-initialized
	}
	o.periodic(ctx, 30*time.Second, h.Limits.FamilyCheckInterval, families.Handler{Handler: h.Handler}.CheckFamilies)
}

// FamilyHosts serves <label>.<suffix> for live families, as nginx hands it
// over: /v1/ goes to the API (which binds every lookup to this one site),
// /internal/ to the app's pages (nginx's own rewrites), everything else is
// the site's files through the gate. A site with a domain of its own
// redirects there; an old name of a renamed site redirects to its current
// address. Every other host goes to next.
func (h *SiteHandler) FamilyHosts(api, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := httpx.HostName(r)
		f, label, ok := h.Hosts.LiveFamily(host)
		if !ok && !strings.HasPrefix(r.URL.Path, "/internal/") && !strings.HasPrefix(r.URL.Path, "/v1/") &&
			h.Hosts.KnownFamilyHost(host) && !h.Hosts.IsBoundDomain(r.Context(), host) {
			// A family that is not live (yet, or any more) answers nothing:
			// never the platform's own pages.
			http.NotFound(w, r)
			return
		}
		if !ok || strings.HasPrefix(r.URL.Path, "/internal/") {
			next.ServeHTTP(w, r)
			return
		}
		// A custom domain bound here under the family: the exact name wins.
		if _, err := h.SitesSvc().GetSiteByCustomDomain(r.Context(), host); err == nil {
			next.ServeHTTP(w, r)
			return
		}
		query := ""
		if r.URL.RawQuery != "" {
			query = "?" + r.URL.RawQuery
		}
		escaped := r.URL.EscapedPath()
		if escaped == "" {
			escaped = "/"
		}
		m, found, err := h.Hosts.FamilySiteForHost(r.Context(), host)
		if err != nil {
			log.Printf("family host %s: %v", host, err)
			h.renderServiceError(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/v1/") {
			// Found or not, the API decides: on a family host nothing but
			// the one site the host names resolves.
			api.ServeHTTP(w, r)
			return
		}
		if !found {
			if sites.FamilyLabelOK(f, label, h.Limits) {
				if target, ok := h.Hosts.FamilyRenamedTarget(r.Context(), f, label, escaped); ok {
					w.Header().Set("Cache-Control", "no-store")
					http.Redirect(w, r, target+query, http.StatusFound)
					return
				}
			}
			http.NotFound(w, r)
			return
		}
		h.serveFamilyFile(w, r, m, r.URL.Path, escaped)
	})
}

// serveFamilyFile answers one path of the matched site on its family host:
// a site that lives on a domain of its own redirects there (302, so
// disconnecting that domain takes effect at once), anything else is its
// file through the gate (take-down, offline, passcode).
func (h *SiteHandler) serveFamilyFile(w http.ResponseWriter, r *http.Request, m sites.FamilyMatch, rel, escaped string) {
	query := ""
	if r.URL.RawQuery != "" {
		query = "?" + r.URL.RawQuery
	}
	if info, has, err := h.Hosts.SiteOwnDomain(r.Context(), m.Site.ID); err == nil && has {
		w.Header().Set("Cache-Control", "no-store")
		http.Redirect(w, r, "https://"+strings.ToLower(info.Domain)+"/"+strings.TrimLeft(escaped, "/")+query, http.StatusFound)
		return
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	h.serveSiteFile(w, r, m.Site.UserID, m.Site.Name, rel, escapedPageTail(r.URL, rel))
}

// familyPageHandler answers /internal/family/{rest...}, where nginx sends a
// family-host request for a site whose folder carries the lives-elsewhere
// marker: the redirect to its own domain (or, when the marker is stale, the
// file itself).
func (h *SiteHandler) familyPageHandler(w http.ResponseWriter, r *http.Request) {
	rest := "/" + strings.TrimPrefix(r.PathValue("rest"), "/")
	m, ok, err := h.Hosts.FamilySiteForHost(r.Context(), httpx.HostName(r))
	if err != nil {
		log.Printf("family page %s: %v", httpx.HostName(r), err)
		h.renderServiceError(w, r)
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	h.serveFamilyFile(w, r, m, rest, (&url.URL{Path: rest}).EscapedPath())
}
