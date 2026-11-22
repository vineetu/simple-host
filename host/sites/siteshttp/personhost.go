package siteshttp

import (
	"database/sql"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/sites/sitesroute"
	"github.com/vsriram/simple-host/host/web"
)

// Per-person addresses.
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

// PersonHosts serves <handle>.<SITE_DOMAIN>: /v1/ goes to the API (which binds
// every site lookup to this person), / is the person's page, /<site>/... is
// that site's files. An old handle kept as an alias 301s to the current
// address. Everything else — the mode is off, the name is not a handle — goes
// to next.
func (h *SiteHandler) PersonHosts(api, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !h.Hosts.Addressing().PersonHostsOn() {
			next.ServeHTTP(w, r)
			return
		}
		host := httpx.HostName(r)
		label, base, ok := h.Hosts.UserHostLabel(host)
		if !ok || !h.Hosts.IsPlatformSubdomain(host) || !sites.HandleAddressable(label) {
			next.ServeHTTP(w, r)
			return
		}
		user, err := h.StoreAccounts().GetUserByHandle(r.Context(), label)
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				log.Printf("person host %s: %v", host, err)
				h.renderServiceError(w, r)
				return
			}
			if current, aerr := h.StoreAccounts().ResolveHandleAlias(r.Context(), label); aerr == nil && sites.HandleAddressable(current) {
				http.Redirect(w, r, "https://"+h.Hosts.Addressing().PersonHostOn(current, h.Hosts.Addressing().BaseFor(current))+r.URL.RequestURI(), http.StatusMovedPermanently)
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		if a := h.Hosts.Addressing(); a.BaseSplit() && !strings.EqualFold(base, a.BaseFor(user.Handle.String)) {
			next.ServeHTTP(w, r) // not this person's base (SiteBaseHosts redirected the pages)
			return
		}
		if h.Ed.Routes().HostedEvents {
			h.ServeGroupHost(w, r, user, api)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/v1/") {
			api.ServeHTTP(w, r)
			return
		}
		h.servePersonHost(w, r, user, base)
	})
}

func (h *SiteHandler) servePersonHost(w http.ResponseWriter, r *http.Request, user model.User, base string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	handle := user.Handle.String
	if h.serveHomePage(w, r, user, base) {
		return
	}
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
	site, err := h.SitesSvc().AddressedSite(r.Context(), user.ID, seg)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			h.renderServiceError(w, r)
			return
		}
		// A root-absolute link written for the old path address
		// (/<handle>/<site>/...) lands here: send it to /<site>/....
		if seg == handle && strings.TrimLeft(escTail, "/") != "" {
			// TrimLeft: "//x" would be another host.
			http.Redirect(w, r, "/"+strings.TrimLeft(escTail, "/")+query, http.StatusMovedPermanently)
			return
		}
		// An old name of a renamed site: its current address.
		if target, ok := h.renamedSiteAddress(r.Context(), user.ID, seg, "/"+escTail); ok {
			w.Header().Set("Cache-Control", "no-store")
			http.Redirect(w, r, target+query, http.StatusFound)
			return
		}
		h.renderPersonNotFound(w, r, handle)
		return
	}
	if !hasSlash {
		http.Redirect(w, r, "/"+seg+"/"+query, http.StatusMovedPermanently)
		return
	}
	sitesroute.MarkRestrictedSite(w, site)
	// A preview of one of its versions (preview.go), before any redirect
	// to the site's own host or domain.
	if _, tail, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/"); strings.HasPrefix(tail, web.PreviewPathSegment+"/") && h.Hosts.Addressing().PersonAddressFor(handle, site.Name) {
		h.servePreview(w, r, site, strings.TrimPrefix(tail, web.PreviewPathSegment+"/"), "/"+site.Name+"/"+web.PreviewPathSegment+"/")
		return
	}
	if sites.ContentHostOnly(handle, site.Name) {
		w.Header().Set("Cache-Control", "no-store")
		http.Redirect(w, r, h.ContentBaseURL()+"/"+handle+"/"+site.Name+"/"+escTail+query, http.StatusFound)
		return
	}
	// A site with its own domain lives there (302, like the content host, so
	// disconnecting the domain takes effect at once).
	if info, has, err := h.Hosts.SiteOwnAddress(r.Context(), site.ID); err == nil && has {
		w.Header().Set("Cache-Control", "no-store")
		http.Redirect(w, r, "https://"+info.Domain+"/"+escTail+query, http.StatusFound)
		return
	}
	// A site on its own site host lives there (302 while the move is new),
	// under the same base as this request.
	if h.Hosts.Addressing().SiteHostCanonicalOn(handle, site.Name, base) {
		w.Header().Set("Cache-Control", "no-store")
		http.Redirect(w, r, "https://"+h.Hosts.Addressing().SiteHostOn(handle, site.Name, base)+"/"+strings.TrimLeft(escTail, "/")+query, http.StatusFound)
		return
	}
	_, rel, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
	h.ServeSiteFileRel(w, r, site.UserID, site.Name, "/"+rel)
}

func (h *SiteHandler) renderPersonNotFound(w http.ResponseWriter, r *http.Request, handle string) {
	h.renderNotFoundPage(w, r,
		"That page isn’t here",
		"This site or page doesn’t exist under @"+handle+".",
		"/",
		"Back to @"+handle+"’s sites")
}

// escapedPageTail removes an internal rewrite prefix while retaining the
// visitor's spelling of the decoded, site-relative path.
func escapedPageTail(u *url.URL, rel string) string {
	escaped := u.EscapedPath()
	if u.Path == rel {
		return escaped
	}
	prefix, ok := strings.CutSuffix(u.Path, rel)
	if !ok {
		return (&url.URL{Path: rel}).EscapedPath()
	}
	// Skip the prefix and the leading slash of rel in decoded bytes.
	for range len(prefix) + 1 {
		if escaped[0] == '%' {
			escaped = escaped[3:]
		} else {
			escaped = escaped[1:]
		}
	}
	return "/" + escaped
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
	user, err := h.StoreAccounts().GetUserByHandleOrAlias(r.Context(), handle)
	if err != nil {
		h.RenderNotFound(w, r, "/"+handle)
		return
	}
	current := user.Handle.String
	if name == "" {
		if h.Hosts.Addressing().PersonHostsCanonical() && sites.HandleAddressable(current) {
			w.Header().Set("Cache-Control", "no-store")
			http.Redirect(w, r, h.PersonPageURL(current), http.StatusFound)
			return
		}
		h.renderShowcase(w, r, current)
		return
	}
	rest := "/" + strings.TrimPrefix(r.PathValue("rest"), "/")
	site, err := h.SitesSvc().AddressedSite(r.Context(), user.ID, name)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// An old name of a renamed site: its current address.
			escRest := (&url.URL{Path: rest}).EscapedPath()
			if target, ok := h.renamedSiteAddress(r.Context(), user.ID, name, escRest); ok {
				w.Header().Set("Cache-Control", "no-store")
				http.Redirect(w, r, target+query, http.StatusFound)
				return
			}
		}
		h.RenderNotFound(w, r, "/"+handle+"/"+name)
		return
	}
	sitesroute.MarkRestrictedSite(w, site)
	if info, has, err := h.Hosts.SiteOwnAddress(r.Context(), site.ID); err == nil && has {
		w.Header().Set("Cache-Control", "no-store")
		http.Redirect(w, r, "https://"+info.Domain+rest+query, http.StatusFound)
		return
	}
	if h.Hosts.Addressing().PersonHostsCanonical() && h.Hosts.Addressing().PersonAddressFor(current, site.Name) {
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
	escRest := escapedPageTail(r.URL, rest)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	h.serveSiteFile(w, r, site.UserID, site.Name, rest, "/"+handle+"/"+name+escRest)
}
