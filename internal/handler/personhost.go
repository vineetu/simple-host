package handler

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"path"
	"strings"

	db "github.com/vsriram/simple-host/internal/db"
)

// Per-person addresses (owner decision 2026-09-25).
//
// Every account's handle is also a host: <handle>.<SITE_DOMAIN>. Its root is
// the person's page (their public sites), and each site lives under it at
// /<site>/. The host is that person's own browser origin, so — like a claimed
// <name>.<SITE_DOMAIN> or a custom domain — visitors sign in there, saves need
// a signed-in visitor, and private collections work there. The /v1/ API on a
// person host answers for that person's sites only.
//
// PERSON_HOSTS picks how far this goes: off (the path model only: what event
// and self-hosted instances keep), serve (person hosts answer, but every
// address handed out is still the path URL) or canonical (person hosts answer
// and are the address handed out everywhere).

type personHostMode int

const (
	personHostsOff personHostMode = iota
	personHostsServe
	personHostsCanonical
)

// SetPersonHosts sets the PERSON_HOSTS mode. Unknown values mean off.
func (h *SiteHandler) SetPersonHosts(mode string) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "serve":
		h.personHosts = personHostsServe
	case "canonical":
		h.personHosts = personHostsCanonical
	default:
		h.personHosts = personHostsOff
	}
}

func (h *SiteHandler) personHostsOn() bool {
	return h.personHosts != personHostsOff && h.siteDomain != ""
}

func (h *SiteHandler) personHostsCanonical() bool {
	return h.personHosts == personHostsCanonical && h.siteDomain != ""
}

// contentHostOnlySites keep their path URL as their address, because the page
// depends on something only the content host serves (vineetu/eb2-wait calls
// the root-absolute /eb2-api/* locations nginx has there). On a person host
// they redirect to the path URL. Remove an entry once its dependency moves.
var contentHostOnlySites = map[string]bool{
	"vineetu/eb2-wait": true,
}

// handleAddressable: the handle can be a host label and is not reserved.
func handleAddressable(handle string) bool {
	return visitorHandleRe.MatchString(handle) && handleIsLabel(handle) && !labelReserved(handle)
}

// personHostFor is the host of an account's own address.
func (h *SiteHandler) personHostFor(handle string) string {
	return strings.ToLower(handle) + "." + strings.ToLower(h.siteDomain)
}

// personAddressFor reports whether this site is served at its owner's person
// address (the mode is on, the handle can be a host, and the site is not
// pinned to the content host).
func (h *SiteHandler) personAddressFor(handle, name string) bool {
	return h.personHostsOn() && handleAddressable(handle) && !contentHostOnlySites[handle+"/"+name]
}

// SiteURL is the address handed out for a site: the person address in
// canonical mode, else the path URL on the content host. "" without a handle.
func (h *SiteHandler) SiteURL(handle, name string) string {
	if handle == "" {
		return ""
	}
	if h.personHostsCanonical() && h.personAddressFor(handle, name) {
		return "https://" + h.personHostFor(handle) + "/" + name + "/"
	}
	host := h.contentHost
	if host == "" {
		host = "sites." + h.siteDomain
	}
	return "https://" + host + "/" + handle + "/" + name + "/"
}

// PersonPageURL is the address of an account's public page.
func (h *SiteHandler) PersonPageURL(handle string) string {
	if handle == "" {
		return ""
	}
	if h.personHostsCanonical() && handleAddressable(handle) {
		return "https://" + h.personHostFor(handle) + "/"
	}
	return h.contentBaseURL() + "/" + handle
}

// siteURLFor is SiteURL for a site row, falling back to the stored address
// for the rare site whose owner has no handle.
func (h *SiteHandler) siteURLFor(site db.Site) string {
	if u := h.SiteURL(site.OwnerHandle, site.Name); u != "" {
		return u
	}
	return site.SiteURL
}

// personHostOwner returns the account whose address host is, when person
// hosts are on. Claimed names never get here (BoundSubdomains answers them
// first, and the shared namespace keeps them apart from handles).
func (h *SiteHandler) personHostOwner(ctx context.Context, host string) (db.User, bool) {
	if !h.personHostsOn() {
		return db.User{}, false
	}
	label, ok := platformSubdomainLabel(host, h.siteDomain)
	if !ok || !h.isPlatformSubdomainHost(host) || !handleAddressable(label) {
		return db.User{}, false
	}
	u, err := db.GetUserByHandle(ctx, h.database, label)
	if err != nil {
		return db.User{}, false
	}
	return u, true
}

// isPersonHost: the request host is some account's own address.
func (h *SiteHandler) isPersonHost(ctx context.Context, host string) bool {
	_, ok := h.personHostOwner(ctx, host)
	return ok
}

// siteHome is the host that is a site's own origin: its proven own domain
// (custom or a claimed <name>.<SITE_DOMAIN>), else its owner's person address.
type siteHome struct {
	Host     string
	OwnerID  string
	IsDomain bool
}

// siteHomeFor returns the site's own origin, or ok=false when it has none (no
// domain, and person hosts are off or the owner cannot have one).
func (h *SiteHandler) siteHomeFor(ctx context.Context, siteID string) (siteHome, bool, error) {
	info, ok, err := h.siteOwnDomain(ctx, siteID)
	if err != nil {
		return siteHome{}, false, err
	}
	if ok {
		return siteHome{Host: strings.ToLower(info.Domain), OwnerID: info.UserID, IsDomain: true}, true, nil
	}
	if !h.personHostsOn() {
		return siteHome{}, false, nil
	}
	handle, ownerID, name, err := db.GetSiteOwner(ctx, h.database, siteID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return siteHome{}, false, nil
		}
		return siteHome{}, false, err
	}
	if !h.personAddressFor(handle, name) {
		return siteHome{}, false, nil
	}
	return siteHome{Host: h.personHostFor(handle), OwnerID: ownerID}, true, nil
}

// livesOnDomainElsewhere reports whether a request on a shared or person host
// is for a site whose home is its own domain: such a site takes no saves and
// no sign-in here (owner decision 2026-09-06). Returns the domain.
func (h *SiteHandler) livesOnDomainElsewhere(r *http.Request, siteID string) (string, bool) {
	if !h.isPersonHost(r.Context(), requestHostName(r)) {
		return "", false
	}
	info, ok, err := h.siteOwnDomain(r.Context(), siteID)
	if err != nil || !ok {
		return "", false
	}
	return info.Domain, true
}

// PersonReturnSite resolves a sign-in return_to on a person host: the site is
// the first path segment, it must be the owner's, and it must live here (not
// on a domain of its own). Used by the OAuth start to bind the session.
func (h *SiteHandler) PersonReturnSite(ctx context.Context, host, p string) (string, bool) {
	owner, ok := h.personHostOwner(ctx, host)
	if !ok {
		return "", false
	}
	seg, _, _ := strings.Cut(strings.TrimPrefix(path.Clean("/"+p), "/"), "/")
	if !validSiteName.MatchString(seg) {
		return "", false
	}
	site, err := db.GetSiteByUser(ctx, h.database, owner.ID, seg)
	if err != nil || !h.personAddressFor(owner.Handle.String, site.Name) {
		return "", false
	}
	if _, has, err := h.siteOwnDomain(ctx, site.ID); err != nil || has {
		return "", false
	}
	return site.ID, true
}

// PersonHosts serves <handle>.<SITE_DOMAIN>: /v1/ goes to the API (which binds
// every site lookup to this person), / is the person's page, /<site>/... is
// that site's files. An old handle kept as an alias 301s to the current
// address. Everything else — the mode is off, the name is not a handle — goes
// to next.
func (h *SiteHandler) PersonHosts(api, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !h.personHostsOn() {
			next.ServeHTTP(w, r)
			return
		}
		host := requestHostName(r)
		label, ok := platformSubdomainLabel(host, h.siteDomain)
		if !ok || !h.isPlatformSubdomainHost(host) || !handleAddressable(label) {
			next.ServeHTTP(w, r)
			return
		}
		user, err := db.GetUserByHandle(r.Context(), h.database, label)
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				log.Printf("person host %s: %v", host, err)
				h.renderServiceError(w)
				return
			}
			if current, aerr := db.ResolveHandleAlias(r.Context(), h.database, label); aerr == nil && handleAddressable(current) {
				http.Redirect(w, r, "https://"+h.personHostFor(current)+r.URL.RequestURI(), http.StatusMovedPermanently)
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/v1/") {
			api.ServeHTTP(w, r)
			return
		}
		h.servePersonHost(w, r, user)
	})
}

// servePersonHost answers a non-API request on a person host.
func (h *SiteHandler) servePersonHost(w http.ResponseWriter, r *http.Request, user db.User) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	handle := user.Handle.String
	if r.URL.Path == "/" || r.URL.Path == "" {
		h.renderShowcase(w, r, handle)
		return
	}
	escaped := r.URL.EscapedPath()
	seg, _, hasSlash := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
	escTail := ""
	if _, t, ok := strings.Cut(strings.TrimPrefix(escaped, "/"), "/"); ok {
		escTail = t
	}
	query := ""
	if r.URL.RawQuery != "" {
		query = "?" + r.URL.RawQuery
	}
	if !validSiteName.MatchString(seg) {
		h.renderPersonNotFound(w, r, handle)
		return
	}
	site, err := db.GetSiteByUser(r.Context(), h.database, user.ID, seg)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			h.renderServiceError(w)
			return
		}
		// A root-absolute link written for the old path address
		// (/<handle>/<site>/...) lands here: send it to /<site>/....
		if seg == handle && strings.TrimLeft(escTail, "/") != "" {
			// TrimLeft: "//x" would be another host.
			http.Redirect(w, r, "/"+strings.TrimLeft(escTail, "/")+query, http.StatusMovedPermanently)
			return
		}
		h.renderPersonNotFound(w, r, handle)
		return
	}
	if !hasSlash {
		http.Redirect(w, r, "/"+seg+"/"+query, http.StatusMovedPermanently)
		return
	}
	if contentHostOnlySites[handle+"/"+site.Name] {
		w.Header().Set("Cache-Control", "no-store")
		http.Redirect(w, r, h.contentBaseURL()+"/"+handle+"/"+site.Name+"/"+escTail+query, http.StatusFound)
		return
	}
	// A site with its own domain lives there (302, like the content host, so
	// disconnecting the domain takes effect at once).
	if info, has, err := h.siteOwnDomain(r.Context(), site.ID); err == nil && has {
		w.Header().Set("Cache-Control", "no-store")
		http.Redirect(w, r, "https://"+info.Domain+"/"+escTail+query, http.StatusFound)
		return
	}
	_, rel, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
	h.serveSiteFileRel(w, r, site.UserID, site.Name, "/"+rel)
}

// renderPersonNotFound is the branded 404 for a miss on a person host, with
// the way back to that person's page.
func (h *SiteHandler) renderPersonNotFound(w http.ResponseWriter, r *http.Request, handle string) {
	h.renderNotFoundPage(w, r,
		"That page isn’t here",
		"This site or page doesn’t exist under @"+handle+".",
		"/",
		"Back to @"+handle+"’s sites")
}

// contentHostRedirect answers an old content-host address once nginx sends it
// here (docs/designs/per-person-subdomains-nginx.md; not wired tonight):
// GET /internal/site-redirect/{handle} and /{handle}/{sitename}/{rest...}.
// In canonical mode a site on a person address, or a person's page, 302s
// there with its path and query; everything else — a site pinned to the
// content host, off/serve mode — is served from disk here exactly as nginx
// would, so the nginx change is safe whatever the mode.
func (h *SiteHandler) contentHostRedirect(w http.ResponseWriter, r *http.Request) {
	handle := strings.ToLower(strings.TrimSpace(r.PathValue("handle")))
	name := strings.TrimSpace(r.PathValue("sitename"))
	query := ""
	if r.URL.RawQuery != "" {
		query = "?" + r.URL.RawQuery
	}
	user, err := db.GetUserByHandleOrAlias(r.Context(), h.database, handle)
	if err != nil {
		h.renderNotFound(w, r, "/"+handle)
		return
	}
	current := user.Handle.String
	if name == "" {
		if h.personHostsCanonical() && handleAddressable(current) {
			w.Header().Set("Cache-Control", "no-store")
			http.Redirect(w, r, h.PersonPageURL(current), http.StatusFound)
			return
		}
		h.renderShowcase(w, r, current)
		return
	}
	site, err := db.GetSiteByUser(r.Context(), h.database, user.ID, name)
	if err != nil || !validSiteName.MatchString(name) {
		h.renderNotFound(w, r, "/"+handle+"/"+name)
		return
	}
	rest := "/" + strings.TrimPrefix(r.PathValue("rest"), "/")
	if info, has, err := h.siteOwnDomain(r.Context(), site.ID); err == nil && has {
		w.Header().Set("Cache-Control", "no-store")
		http.Redirect(w, r, "https://"+info.Domain+rest+query, http.StatusFound)
		return
	}
	if h.personHostsCanonical() && h.personAddressFor(current, site.Name) {
		w.Header().Set("Cache-Control", "no-store")
		http.Redirect(w, r, strings.TrimSuffix(h.SiteURL(current, site.Name), "/")+rest+query, http.StatusFound)
		return
	}
	if r.PathValue("rest") == "" && !strings.HasSuffix(r.URL.Path, "/") {
		http.Redirect(w, r, "/"+handle+"/"+name+"/"+query, http.StatusPermanentRedirect)
		return
	}
	// A directory redirect must name the public address (/<handle>/<site>/...),
	// never this internal route; it is rebuilt from validated parts only.
	escRest := ""
	if parts := strings.SplitN(strings.TrimPrefix(r.URL.EscapedPath(), "/internal/site-redirect/"), "/", 3); len(parts) == 3 {
		escRest = "/" + parts[2]
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	h.serveSiteFile(w, r, site.UserID, site.Name, rest, "/"+handle+"/"+name+escRest)
}
