package handler

import (
	"context"
	"net/http"
	"strings"

	"github.com/vsriram/simple-host/host/model"
)

// StaticErrorPage is the ordinary service-error page for an API outage.
func (h *SiteHandler) StaticErrorPage() http.Handler {
	return http.HandlerFunc(h.renderServiceError)
}

// StaticAPIRequest identifies requests needing API-owned state even when no
// routing copy is loaded.
func (h *SiteHandler) StaticAPIRequest(r *http.Request) bool {
	// Static does not install the product's ownership provider. Let the API
	// apply the owner frame guard to signed-in group members and organisers.
	if h.ed.Storage().EventOwners && visitorCookieValue(r) != "" {
		return true
	}
	// Preview tokens belong to the API's signing key, including person-path
	// previews where the site name precedes the preview segment.
	_, rest, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if strings.HasPrefix(r.URL.Path, "/"+previewPathSegment+"/") || strings.HasPrefix(rest, previewPathSegment+"/") {
		return true
	}
	if strings.HasPrefix(r.URL.Path, "/internal/") {
		if handle, ok := strings.CutPrefix(r.URL.Path, "/internal/site-redirect/"); ok && !strings.Contains(handle, "/") && !h.personHostsCanonical() {
			return true
		}
		return strings.HasPrefix(r.URL.Path, "/internal/showcase/")
	}
	host := requestHostName(r)
	for _, base := range h.servedBases() {
		if host == base || host == "www."+base {
			return true
		}
	}
	return host == h.contentHost
}

// StaticPages installs only page routes. This handler must be a separate
// instance from the API handler: it has no mutating routes or workers.
// Access-marked requests are handed over whole until D4-S4b runs their gates.
func (h *SiteHandler) StaticPages(table model.SiteTable, api http.Handler, retry func(http.ResponseWriter, *http.Request, model.RoutingAddress) bool) http.Handler {
	h.SetSiteFacts(NewCopyFacts(table))
	upstream := api
	api = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Resolution may have set page headers before discovering a hand-over.
		// The API owns this whole response, including its cache and gate headers.
		for key := range w.Header() {
			w.Header().Del(key)
		}
		upstream.ServeHTTP(w, r)
	})
	h.staticAPI = api
	mux := http.NewServeMux()
	mux.HandleFunc("GET /internal/tls-ask", h.tlsAsk)
	mux.HandleFunc("GET /internal/domain-redirect/{handle}/{sitename}", h.domainRedirect)
	mux.HandleFunc("GET /internal/domain-redirect/{handle}/{sitename}/{rest...}", h.domainRedirect)
	mux.HandleFunc("GET /internal/site-redirect/{handle}", h.contentHostRedirect)
	mux.HandleFunc("GET /internal/site-redirect/{handle}/{sitename}", h.contentHostRedirect)
	mux.HandleFunc("GET /internal/site-redirect/{handle}/{sitename}/{rest...}", h.contentHostRedirect)
	mux.HandleFunc("GET /internal/notfound", h.notFound)
	mux.HandleFunc("GET /internal/suspended", h.suspendedPage)
	mux.HandleFunc("GET /internal/offline", h.offlinePageHandler)
	mux.HandleFunc("GET /internal/passcode/{rest...}", h.passcodePageHandler)
	mux.HandleFunc("GET /internal/family/{rest...}", h.familyPageHandler)
	mux.Handle("/", api)
	internal := SecurityHeaders(CORS(mux))
	notFound := SecurityHeaders(CORS(http.NotFoundHandler()))
	fallback := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := requestHostName(r)
		if host == h.siteDomain || host == h.contentHost || strings.HasPrefix(r.URL.Path, "/internal/") {
			mux.ServeHTTP(w, r)
		} else {
			notFound.ServeHTTP(w, r)
		}
	})
	pages := h.FamilyHosts(api, h.SiteBaseHosts(h.BoundSubdomains(api, h.SiteHosts(api, h.PersonHosts(api, h.LegacyHostRedirect(fallback))))))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.staticRefreshFamilies(table.Families)
		if h.StaticAPIRequest(r) || h.staticPersonRootNeedsAPI(r) {
			api.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/internal/") {
			if address, missing := h.staticMissingAddress(r); missing && retry(w, r, address) {
				return
			}
			internal.ServeHTTP(w, r)
			return
		}
		if address, missing := h.staticMissingAddress(r); missing && retry(w, r, address) {
			return
		}
		pages.ServeHTTP(w, r)
	})
}

// Readiness belongs to the issuer's files and can change without a table swap.
// The regular family refresh also repairs disk links; Static only reads files
// and replaces its serving indexes, even when the database is unavailable.
func (h *SiteHandler) staticRefreshFamilies(families []model.AddressFamily) {
	bySuffix := map[string]model.AddressFamily{}
	byUser := map[string][]model.AddressFamily{}
	known := map[string]bool{}
	for _, f := range families {
		if f.VerifiedAt.Valid || f.ProofExempt {
			known[f.Suffix] = true
		}
		if h.familiesOn() && h.familyIsLive(f) {
			bySuffix[f.Suffix] = f
			byUser[f.UserID] = append(byUser[f.UserID], f)
		}
	}
	h.families.mu.Lock()
	h.families.bySuffix, h.families.byUser, h.families.known = bySuffix, byUser, known
	h.families.mu.Unlock()
}

// A person root missing from the copy cannot be resolved by a domain lookup.
// Without a copied home, the API resolves the current home or profile itself.
func (h *SiteHandler) staticPersonRootNeedsAPI(r *http.Request) bool {
	if r.URL.Path != "/" && r.URL.Path != "" {
		return false
	}
	host := requestHostName(r)
	if _, err := h.siteFacts().GetSiteByCustomDomain(r.Context(), host); err == nil {
		return false
	}
	if h.siteHostsOn() && h.isSiteHostName(r.Context(), host) {
		return false
	}
	label, _, ok := h.userHostLabel(host)
	if !ok || !h.personHostsOn() {
		return false
	}
	u, err := h.siteFacts().GetUserByHandle(r.Context(), label)
	if err != nil {
		return true
	}
	_, has, err := h.siteFacts().HomeSite(r.Context(), u.ID)
	return err != nil || !has
}

// staticMissingAddress uses routing facts and disk markers to detect a miss.
// Only unmarked homes are probed for path collisions. A miss gets one bounded
// database lookup at the Static boundary, then normal resolution.
func (h *SiteHandler) staticMissingAddress(r *http.Request) (model.RoutingAddress, bool) {
	ctx := r.Context()
	host := requestHostName(r)
	if r.URL.Path == "/internal/tls-ask" {
		raw := r.URL.Query().Get("domain")
		host = strings.ToLower(strings.TrimSpace(strings.TrimSuffix(raw, ".")))
		if i := strings.Index(host, "://"); i >= 0 {
			host = host[i+3:]
		}
		if i := strings.IndexByte(host, '/'); i >= 0 {
			host = host[:i]
		}
		if i := strings.IndexByte(host, ':'); i >= 0 {
			host = host[:i]
		}
		if domain, err := h.normalizeDomain(raw); err == nil {
			host = domain
		}
		if isOwnHost(host, h.contentHost) || isOwnHost(host, h.siteDomain) && !h.ed.Storage().EventSites {
			return model.RoutingAddress{}, false
		}
		if h.ed.Storage().EventSites {
			for _, base := range h.servedBases() {
				if !strings.HasSuffix(host, "."+base) {
					continue
				}
				if h.groupTLSHostExists(ctx, host) {
					return model.RoutingAddress{}, false
				}
				if name, group, _, ok := h.siteHostParts(ctx, host); ok {
					return model.RoutingAddress{Group: group, GroupSite: name}, true
				}
				label := strings.TrimSuffix(host, "."+base)
				if label != "" && !strings.Contains(label, ".") {
					return model.RoutingAddress{Group: label}, true
				}
			}
		}
	}
	if _, err := h.siteFacts().GetSiteByCustomDomain(ctx, host); err == nil {
		return model.RoutingAddress{}, false
	}
	if name, handle, _, ok := h.siteHostParts(ctx, host); ok && h.siteHostsOn() {
		return h.staticPairMissing(ctx, handle, name)
	}
	for _, prefix := range []string{"/internal/site-redirect/", "/internal/domain-redirect/", "/internal/passcode/"} {
		if host == h.contentHost && strings.HasPrefix(r.URL.Path, prefix) {
			parts := strings.Split(strings.TrimPrefix(r.URL.Path, prefix), "/")
			if len(parts) >= 2 {
				return h.staticPairMissing(ctx, parts[0], parts[1])
			}
		}
	}
	if label, _, ok := h.userHostLabel(host); ok && h.personHostsOn() {
		name, _, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
		if name != "" && !strings.HasPrefix(r.URL.Path, "/internal/") {
			// Home files win collisions. An absent home path may instead
			// name a sibling published since the last routing load.
			if u, err := h.siteFacts().GetUserByHandle(ctx, label); err == nil {
				if home, has, _ := h.siteFacts().HomeSite(ctx, u.ID); has {
					// Marked homes reach their gate before any path probe or
					// sibling lookup, even when the copy predates the marker.
					if h.disk.IsSuspended(home.UserID, home.Name) || h.disk.IsOffline(home.UserID, home.Name) || h.disk.HasPasscodeMarker(home.UserID, home.Name) {
						return model.RoutingAddress{}, false
					}
					if h.homePathExists(home, r.URL.Path) {
						return model.RoutingAddress{}, false
					}
				}
			}
			return h.staticPairMissing(ctx, label, name)
		}
		if _, err := h.siteFacts().GetUserByHandleOrAlias(ctx, label); err == nil {
			return model.RoutingAddress{}, false
		}
	}
	if _, found, _ := h.familySiteForHost(ctx, host); found {
		return model.RoutingAddress{}, false
	}
	if host == h.siteDomain || host == h.contentHost {
		return model.RoutingAddress{}, false
	}
	return model.RoutingAddress{Domain: host}, true
}

func (h *SiteHandler) staticPairMissing(ctx context.Context, handle, name string) (model.RoutingAddress, bool) {
	if u, err := h.siteFacts().GetUserByHandleOrAlias(ctx, handle); err == nil {
		if _, err := h.siteFacts().AddressedSite(ctx, u.ID, name); err == nil {
			return model.RoutingAddress{}, false
		}
		if _, _, err := h.siteFacts().AddressedOldSite(ctx, u.ID, name); err == nil {
			return model.RoutingAddress{}, false
		}
		handle = u.Handle.String
	}
	return model.RoutingAddress{Handle: handle, Name: name}, true
}
