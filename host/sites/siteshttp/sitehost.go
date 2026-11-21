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
	"github.com/vsriram/simple-host/host/web"
)

// Per-site addresses (owner decision 2026-09-26).
//
// Every site lives at its own origin, <site>.<handle>.<SITE_DOMAIN>: the
// site's files at the host root, /v1/ answering for that one site only, and
// visitor sign-in, sessions and private collections bound to that host. A
// person's sites are therefore kept apart from each other by the browser, not
// only by the server. The person host (<handle>.<SITE_DOMAIN>) stays the
// person's page.
//
// A two-label name needs a certificate of its own (*.<handle>.<SITE_DOMAIN>;
// the platform wildcard covers one label only). A root-owned issuer outside
// this process makes them (deploy/site-certs/): this service drops a request
// file per handle in SITE_CERT_DIR/requests/ and the issuer writes
// SITE_CERT_DIR/ready/<handle> once the certificate is served. Until then a
// person's sites keep their person-path address (<handle>.<SITE_DOMAIN>/<site>/)
// and nothing here ever hands out, or redirects to, a site host whose
// certificate is not ready — a TLS name mismatch cannot be fixed after the
// handshake.
//
// SITE_HOSTS: off (no site hosts; event and self-hosted instances), serve
// (site hosts answer; addresses handed out stay the person-path form) or
// canonical (site hosts are the address handed out, and person-path and
// legacy content-host URLs redirect there). Needs PERSON_HOSTS on.

type siteHostMode = sites.HostMode

const (
	siteHostsOff       = sites.HostsOff
	siteHostsServe     = sites.HostsServe
	siteHostsCanonical = sites.HostsCanonical
)

// SiteHosts serves <site>.<handle>.<SITE_DOMAIN>: /v1/ goes to the API (which
// binds every lookup to this one site), everything else is the site's files
// at the root. A site with its own domain redirects there; an old handle kept
// as an alias redirects to the current address. Hosts of any other shape go
// to next.
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
		user, err := h.siteFacts().HostOwner(r.Context(), handle, label)
		if err != nil {
			h.siteHostNoOwner(w, r, err, handle, label, base)
			return
		}
		site, err := h.siteFacts().GetSiteByUser(r.Context(), user.ID, label)
		if err != nil {
			h.siteHostNoSite(w, r, err, user, label)
			return
		}
		h.serveSiteHostSite(w, r, api, user, site, base)
	})
}

// siteHostNoOwner answers a site host whose handle is no current account: an
// old handle kept as an alias redirects to the site's current address (or a
// renamed site's), anything else is the platform's 404.
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

// aliasSiteTarget is where a site host under an old handle kept as an alias
// now lives: the same site under the current handle, or a renamed one.
func (h *SiteHandler) aliasSiteTarget(r *http.Request, handle, label, base string) (string, bool) {
	current, err := h.siteFacts().ResolveHandleAlias(r.Context(), handle)
	if err != nil || !sites.HandleAddressable(current) {
		return "", false
	}
	cu, err := h.siteFacts().GetUserByHandle(r.Context(), current)
	if err != nil {
		return "", false
	}
	site, err := h.siteFacts().GetSiteByUser(r.Context(), cu.ID, label)
	if err == nil {
		return h.Hosts.Addressing().SiteAddressWithPathOn(current, site.Name, r.URL.EscapedPath(), base), true
	}
	if errors.Is(err, sql.ErrNoRows) && !strings.HasPrefix(r.URL.Path, "/v1/") {
		return h.renamedSiteAddress(r.Context(), cu.ID, label, r.URL.EscapedPath())
	}
	return "", false
}

// siteHostNoSite answers a site host naming no site of its account: an old
// name of a renamed site redirects to its current address.
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

// queryOf is "?" and the request's raw query, or "".
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

// siteHasPath reports whether the site's live version has something at rel
// (os.Root refuses escapes).
func (h *SiteHandler) siteHasPath(site model.Site, rel string) bool {
	root, err := os.OpenRoot(h.Disk.SiteDir(site.UserID, site.Name) + "/current")
	if err != nil {
		return false
	}
	defer root.Close()
	return rootContentExists(root, rel)
}

// renamedSiteAddress: name is an old name of a renamed site of this account
// (and no site has it now; callers look the site up first). Returns that
// site's current address with escapedPath (starting with "/") appended.
func (h *SiteHandler) renamedSiteAddress(ctx context.Context, userID, name, escapedPath string) (string, bool) {
	siteID, _, err := h.siteFacts().AddressedOldSite(ctx, userID, name)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			log.Printf("old site name %s/%s: %v", userID, name, err)
		}
		return "", false
	}
	return h.Hosts.SiteAddressFor(ctx, siteID, escapedPath)
}

// renderSiteHostNotFound is the branded 404 for a site host naming no site
// of that person, with the way back to their page.
func (h *SiteHandler) renderSiteHostNotFound(w http.ResponseWriter, r *http.Request, handle string) {
	h.renderNotFoundPage(w, r,
		"That site isn’t here",
		"This site doesn’t exist under @"+handle+".",
		h.PersonPageURL(handle),
		"Back to @"+handle+"’s sites")
}

// RequestSiteCert is hosts.RequestSiteCert for the product hooks.
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

// StartSiteCertRequests runs RequestSiteCertsForAll now and then every
// interval until ctx ends.
func (h *SiteHandler) StartSiteCertRequests(ctx context.Context, every time.Duration) {
	o := h.siteLoop("sites.cert_requests")
	o.once.Do(func() {
		if o.enabled() {
			h.startSiteCertRequests(ctx, every)
		}
	})
}
func (h *SiteHandler) startSiteCertRequests(ctx context.Context, every time.Duration) {
	o := h.siteLoop("sites.cert_requests")
	o.periodicTicker(ctx, 0, every, func(ctx context.Context) { h.RequestSiteCertsForAll(ctx); h.updateSiteCertPending(ctx) })
}
