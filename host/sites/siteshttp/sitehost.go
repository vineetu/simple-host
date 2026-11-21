package siteshttp

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/papermetrics"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/sites/pagepath"
	"github.com/vsriram/simple-host/host/web"
)

// Per-site addresses. Every site lives at its own origin, <site>.<handle>.<SITE_DOMAIN>: files at the host
// root, /v1/ answering for that one site, and visitor sign-in, sessions and private collections bound to the
// host, so a person's sites are kept apart by the browser, not only by the server.
//
// A two-label name needs a certificate of its own (the platform wildcard covers one label). A root-owned
// issuer outside this process makes them (deploy/site-certs/): this service drops a request file per handle
// in SITE_CERT_DIR/requests/ and the issuer writes SITE_CERT_DIR/ready/<handle> once it is served. Until then
// sites keep their person-path address and nothing hands out, or redirects to, a site host whose certificate
// is not ready: a TLS name mismatch cannot be fixed after the handshake.
//
// SITE_HOSTS: off (no site hosts), serve (site hosts answer; handed-out addresses stay person-path) or
// canonical (site hosts are handed out; person-path and legacy content-host URLs redirect there). Needs PERSON_HOSTS on.

// SiteHosts serves <site>.<handle>.<SITE_DOMAIN>: /v1/ goes to the API (bound to this one site), the rest is
// the site's files. A site with its own domain redirects there; an old handle kept as an alias redirects to
// the current address. Other host shapes go to next.
func (h *SiteHandler) SiteHosts(api, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !h.Hosts.Addressing().SiteHostsOn() {
			next.ServeHTTP(w, r)
			return
		}
		host := httpx.HostName(r)
		label, handle, base, ok := h.Hosts.SiteHostParts(r.Context(), host)
		if !ok {
			next.ServeHTTP(w, r)
			return
		}
		user, err := h.SitesSvc().HostOwner(r.Context(), handle, label)
		if err != nil {
			h.siteHostNoOwner(w, r, err, handle, label, base)
			return
		}
		site, err := h.SitesSvc().GetSiteByUser(r.Context(), user.ID, label)
		if err != nil {
			h.siteHostNoSite(w, r, err, user, label)
			return
		}
		h.serveSiteHostSite(w, r, api, user, site, base)
	})
}

// siteHostNoOwner answers a site host whose handle is no current account: an alias redirects to the current
// address (or a renamed site's), anything else is the platform's 404.
func (h *SiteHandler) siteHostNoOwner(w http.ResponseWriter, r *http.Request, err error, handle, label, base string) {
	if !errors.Is(err, sql.ErrNoRows) && !errors.Is(err, sites.ErrInvalidHost) {
		log.Printf("site host %s: %v", httpx.HostName(r), err)
		h.renderServiceError(w, r)
		return
	}
	if errors.Is(err, sql.ErrNoRows) {
		if target, ok := h.aliasSiteTarget(r, handle, label, base); ok {
			w.Header().Set("Cache-Control", "no-store")
			http.Redirect(w, r, target+queryOf(r), http.StatusFound)
			return
		}
	}
	h.renderNotFoundPage(w, r, "Page not found", "The page you’re looking for doesn’t exist.", h.mainSiteURL(), "Go to "+h.SiteDomain)
}

func (h *SiteHandler) aliasSiteTarget(r *http.Request, handle, label, base string) (string, bool) {
	current, err := h.storeAccounts().ResolveHandleAlias(r.Context(), handle)
	if err != nil || !sites.HandleAddressable(current) {
		return "", false
	}
	cu, err := h.storeAccounts().GetUserByHandle(r.Context(), current)
	if err != nil {
		return "", false
	}
	site, err := h.SitesSvc().GetSiteByUser(r.Context(), cu.ID, label)
	if err == nil {
		return h.Hosts.Addressing().SiteAddressWithPathOn(current, site.Name, r.URL.EscapedPath(), base), true
	}
	if errors.Is(err, sql.ErrNoRows) && !strings.HasPrefix(r.URL.Path, "/v1/") {
		return h.renamedSiteAddress(r.Context(), cu.ID, label, r.URL.EscapedPath())
	}
	return "", false
}

func (h *SiteHandler) siteHostNoSite(w http.ResponseWriter, r *http.Request, err error, user model.User, label string) {
	if !errors.Is(err, sql.ErrNoRows) {
		log.Printf("site host %s: %v", httpx.HostName(r), err)
		h.renderServiceError(w, r)
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/v1/") {
		if target, ok := h.renamedSiteAddress(r.Context(), user.ID, label, r.URL.EscapedPath()); ok {
			w.Header().Set("Cache-Control", "no-store")
			http.Redirect(w, r, target+queryOf(r), http.StatusFound)
			return
		}
	}
	h.renderSiteHostNotFound(w, r, user.Handle.String)
}

// serveSiteHostSite answers one request for a site on its site host, in
// order: pinned to the content host, a preview, a domain of its own, a
// missing certificate, the API, then its files.
func (h *SiteHandler) serveSiteHostSite(w http.ResponseWriter, r *http.Request, api http.Handler, user model.User, site model.Site, base string) {
	papermetrics.RecordSite(h.Ed, r, site.UserID, site.Name, auth.GetUser(r.Context()))
	markRestrictedSite(w, site)
	handle, query := user.Handle.String, queryOf(r)
	escaped := r.URL.EscapedPath()
	if escaped == "" {
		escaped = "/"
	}
	live := h.Hosts.Addressing().SiteHostLiveOn(handle, site.Name, base)
	if sites.ContentHostOnly(handle, site.Name) {
		w.Header().Set("Cache-Control", "no-store")
		http.Redirect(w, r, h.contentBaseURL()+"/"+handle+"/"+site.Name+escaped+query, http.StatusFound)
		return
	}
	// A preview of one of its versions is served here even when the site
	// lives on a domain of its own.
	if strings.HasPrefix(r.URL.Path, "/"+web.PreviewPathSegment+"/") && live {
		site.OwnerHandle = handle
		h.servePreview(w, r, site, strings.TrimPrefix(r.URL.Path, "/"+web.PreviewPathSegment+"/"), "/"+web.PreviewPathSegment+"/")
		return
	}
	// A site with its own domain lives there (302, so disconnecting the
	// domain takes effect at once); its API here refuses saves and sign-in.
	if !strings.HasPrefix(r.URL.Path, "/v1/") {
		if info, has, err := h.Hosts.SiteOwnAddress(r.Context(), site.ID); err == nil && has {
			w.Header().Set("Cache-Control", "no-store")
			http.Redirect(w, r, "https://"+strings.ToLower(info.Domain)+escaped+query, http.StatusFound)
			return
		}
	}
	if !live && h.Ed.Storage().EventSites {
		h.renderGroupNotFound(w, r) // no person-path address to fall back to
		return
	}
	if !live {
		// TLS was served for a person whose marker is missing (e.g. cleared
		// by hand): the working address.
		w.Header().Set("Cache-Control", "no-store")
		http.Redirect(w, r, h.Hosts.Addressing().PersonPathAddressOn(handle, site.Name, base)+strings.TrimPrefix(escaped, "/")+query, http.StatusFound)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/v1/") {
		api.ServeHTTP(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	h.serveSiteFileWithProbe(w, r, site.UserID, site.Name, r.URL.Path, escaped, func() bool {
		// A root-absolute link written for an older address of this site
		// redirects to the same path at its root when no such file exists.
		rest, ok := h.oldAddressPrefix(handle, site, r.URL.Path, escaped)
		if ok {
			http.Redirect(w, r, "/"+strings.TrimLeft(rest, "/")+query, http.StatusMovedPermanently)
		}
		return ok
	})
}

func queryOf(r *http.Request) string {
	if r.URL.RawQuery == "" {
		return ""
	}
	return "?" + r.URL.RawQuery
}

// oldAddressPrefix: p starts with /<handle>/<site>/ or /<site>/ and the site
// has no file there. Returns the escaped remainder after the prefix.
func (h *SiteHandler) oldAddressPrefix(handle string, site model.Site, p, escaped string) (string, bool) {
	for _, prefix := range []string{"/" + handle + "/" + site.Name + "/", "/" + site.Name + "/"} {
		if !strings.HasPrefix(p, prefix) || !strings.HasPrefix(escaped, prefix) {
			continue
		}
		if h.siteHasPath(site, p) {
			return "", false
		}
		return strings.TrimPrefix(escaped, prefix), true
	}
	return "", false
}

func (h *SiteHandler) siteHasPath(site model.Site, rel string) bool {
	root, err := os.OpenRoot(h.Disk.SiteDir(site.UserID, site.Name) + "/current")
	if err != nil {
		return false
	}
	defer root.Close()
	return pagepath.Taken(root, rel)
}

// renamedSiteAddress: name is an old name of a renamed site of this account
// (and no site has it now; callers look the site up first). Returns that
// site's current address with escapedPath (starting with "/") appended.
func (h *SiteHandler) renamedSiteAddress(ctx context.Context, userID, name, escapedPath string) (string, bool) {
	siteID, _, err := h.SitesSvc().AddressedOldSite(ctx, userID, name)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			log.Printf("old site name %s/%s: %v", userID, name, err)
		}
		return "", false
	}
	return h.Hosts.SiteAddressFor(ctx, siteID, escapedPath)
}

func (h *SiteHandler) renderSiteHostNotFound(w http.ResponseWriter, r *http.Request, handle string) {
	h.renderNotFoundPage(w, r,
		"That site isn’t here",
		"This site doesn’t exist under @"+handle+".",
		h.PersonPageURL(handle),
		"Back to @"+handle+"’s sites")
}

func (h *SiteHandler) RequestSiteCert(handle string) {
	h.Hosts.RequestSiteCert(context.Background(), handle)
}

// RequestSiteCertsForAll requests a certificate for every person with at
// least one site that has none yet (the back-fill, and a safety net for a
// request that was missed).
func (h *SiteHandler) RequestSiteCertsForAll(ctx context.Context) {
	if !h.Hosts.Addressing().SiteHostsOn() || len(h.Hosts.CertRequestBases()) == 0 {
		return
	}
	handles, err := h.SitesSvc().ListHandlesWithSites(ctx)
	if err != nil {
		sites.ReportWorkerFailure(ctx, "list certificate handles", err)
		return
	}
	for _, handle := range handles {
		if ctx.Err() != nil {
			return
		}
		h.Hosts.RequestSiteCert(ctx, handle)
	}
}

func (h *SiteHandler) updateSiteCertPending(ctx context.Context) {
	var pending int64
	seen := map[string]bool{}
	for _, base := range h.Hosts.CertRequestBases() {
		if ctx.Err() != nil {
			return
		}
		dir := filepath.Join(h.Hosts.Addressing().CertDirFor(base), "requests")
		if seen[dir] {
			continue
		}
		seen[dir] = true
		files, err := os.ReadDir(dir)
		if err != nil {
			sites.ReportWorkerFailure(ctx, "count certificate requests", err)
			return
		}
		for _, f := range files {
			if f.Type().IsRegular() {
				pending++
			}
		}
	}
	h.siteLoop("sites.cert_requests").tracker.SetPending(pending)
}

func (h *SiteHandler) startSiteCertRequests(ctx context.Context, every time.Duration) {
	o := h.siteLoop("sites.cert_requests")
	o.periodicTicker(ctx, 0, every, func(ctx context.Context) { h.RequestSiteCertsForAll(ctx); h.updateSiteCertPending(ctx) })
}
