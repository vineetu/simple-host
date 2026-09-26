package handler

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	db "github.com/vsriram/simple-host/internal/db"
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

type siteHostMode int

const (
	siteHostsOff siteHostMode = iota
	siteHostsServe
	siteHostsCanonical
)

// SetSiteHosts sets the SITE_HOSTS mode and the certificate hand-off
// directory. Unknown modes mean off.
func (h *SiteHandler) SetSiteHosts(mode, certDir string) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "serve":
		h.siteHosts = siteHostsServe
	case "canonical":
		h.siteHosts = siteHostsCanonical
	default:
		h.siteHosts = siteHostsOff
	}
	h.siteCertDir = strings.TrimSpace(certDir)
}

func (h *SiteHandler) siteHostsOn() bool {
	return h.siteHosts != siteHostsOff && h.personHostsOn()
}

func (h *SiteHandler) siteHostsCanonical() bool {
	return h.siteHosts == siteHostsCanonical && h.personHostsCanonical()
}

// siteCertReady reports whether *.<handle>.<SITE_DOMAIN> is served with a
// valid certificate: the issuer's ready marker exists. Without a hand-off
// directory every person counts as ready.
func (h *SiteHandler) siteCertReady(handle string) bool {
	if h.siteCertDir == "" {
		return true
	}
	if !handleAddressable(handle) {
		return false
	}
	st, err := os.Stat(filepath.Join(h.siteCertDir, "ready", strings.ToLower(handle)))
	return err == nil && st.Mode().IsRegular()
}

// siteHostFor is the host of a site's own address.
func (h *SiteHandler) siteHostFor(handle, name string) string {
	return strings.ToLower(name) + "." + h.personHostFor(handle)
}

// siteHostLive: this site is served at <site>.<handle>.<SITE_DOMAIN> — the
// mode is on, the site lives on its owner's address (not pinned to the
// content host), its name is a host label and its owner's certificate is
// ready. A site with its own domain still answers here, with a redirect.
func (h *SiteHandler) siteHostLive(handle, name string) bool {
	return h.siteHostsOn() && h.personAddressFor(handle, name) && validSiteName.MatchString(name) &&
		!strings.HasPrefix(name, "xn--") && h.siteCertReady(handle)
}

// siteHostCanonical: the site host is this site's address — handed out, and
// the target of the person-path and legacy redirects.
func (h *SiteHandler) siteHostCanonical(handle, name string) bool {
	return h.siteHostsCanonical() && h.siteHostLive(handle, name)
}

// siteHostLabels splits <site>.<handle>.<SITE_DOMAIN> into its two labels.
// Purely syntactic: any other shape (one label, three, the apex) is ok=false.
func siteHostLabels(host, siteDomain string) (site, handle string, ok bool) {
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	siteDomain = strings.ToLower(strings.TrimSpace(siteDomain))
	if siteDomain == "" || !strings.HasSuffix(host, "."+siteDomain) {
		return "", "", false
	}
	site, handle, ok = strings.Cut(strings.TrimSuffix(host, "."+siteDomain), ".")
	if !ok || site == "" || handle == "" || strings.Contains(handle, ".") {
		return "", "", false
	}
	return site, handle, true
}

// isSiteHostName: host has the shape of a site host (two labels under the
// platform domain). Such a host is never the content host, the apex or a
// claimed name, and every browser treats it as same-site with all of them.
func (h *SiteHandler) isSiteHostName(host string) bool {
	_, _, ok := siteHostLabels(host, h.siteDomain)
	return ok
}

// siteHostSite resolves a site host to its site: the handle is a current
// handle (not an alias), the site is that account's, and the site is live on
// its site host. ok=false otherwise, including when site hosts are off.
func (h *SiteHandler) siteHostSite(ctx context.Context, host string) (db.Site, db.User, bool, error) {
	if !h.siteHostsOn() {
		return db.Site{}, db.User{}, false, nil
	}
	label, handle, ok := siteHostLabels(host, h.siteDomain)
	if !ok || !handleAddressable(handle) || !validSiteName.MatchString(label) {
		return db.Site{}, db.User{}, false, nil
	}
	user, err := db.GetUserByHandle(ctx, h.database, handle)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return db.Site{}, db.User{}, false, nil
		}
		return db.Site{}, db.User{}, false, err
	}
	site, err := db.GetSiteByUser(ctx, h.database, user.ID, label)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return db.Site{}, db.User{}, false, nil
		}
		return db.Site{}, db.User{}, false, err
	}
	if !h.siteHostLive(user.Handle.String, site.Name) {
		return db.Site{}, db.User{}, false, nil
	}
	return site, user, true, nil
}

// SiteReturnSite resolves a sign-in return_to on a site host to its site. It
// must live there (not on a domain of its own). Used by the OAuth start to
// bind the session to this host and site.
func (h *SiteHandler) SiteReturnSite(ctx context.Context, host string) (string, bool) {
	site, _, ok, err := h.siteHostSite(ctx, host)
	if err != nil || !ok {
		return "", false
	}
	if _, has, err := h.siteOwnDomain(ctx, site.ID); err != nil || has {
		return "", false
	}
	return site.ID, true
}

// SiteHosts serves <site>.<handle>.<SITE_DOMAIN>: /v1/ goes to the API (which
// binds every lookup to this one site), everything else is the site's files
// at the root. A site with its own domain redirects there; an old handle kept
// as an alias redirects to the current address. Hosts of any other shape go
// to next.
func (h *SiteHandler) SiteHosts(api, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !h.siteHostsOn() {
			next.ServeHTTP(w, r)
			return
		}
		host := requestHostName(r)
		label, handle, ok := siteHostLabels(host, h.siteDomain)
		if !ok {
			next.ServeHTTP(w, r)
			return
		}
		query := ""
		if r.URL.RawQuery != "" {
			query = "?" + r.URL.RawQuery
		}
		if !handleAddressable(handle) || !validSiteName.MatchString(label) {
			h.renderNotFoundPage(w, r, "Page not found", "The page you’re looking for doesn’t exist.", h.mainSiteURL(), "Go to "+h.siteDomain)
			return
		}
		user, err := db.GetUserByHandle(r.Context(), h.database, handle)
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				log.Printf("site host %s: %v", host, err)
				h.renderServiceError(w)
				return
			}
			// An old handle kept as an alias: the site's current address.
			if current, aerr := db.ResolveHandleAlias(r.Context(), h.database, handle); aerr == nil && handleAddressable(current) {
				if cu, cerr := db.GetUserByHandle(r.Context(), h.database, current); cerr == nil {
					if site, serr := db.GetSiteByUser(r.Context(), h.database, cu.ID, label); serr == nil {
						w.Header().Set("Cache-Control", "no-store")
						http.Redirect(w, r, h.siteAddressWithPath(current, site.Name, r.URL.EscapedPath())+query, http.StatusFound)
						return
					}
				}
			}
			h.renderNotFoundPage(w, r, "Page not found", "The page you’re looking for doesn’t exist.", h.mainSiteURL(), "Go to "+h.siteDomain)
			return
		}
		site, err := db.GetSiteByUser(r.Context(), h.database, user.ID, label)
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				log.Printf("site host %s: %v", host, err)
				h.renderServiceError(w)
				return
			}
			h.renderSiteHostNotFound(w, r, user.Handle.String)
			return
		}
		escaped := r.URL.EscapedPath()
		if escaped == "" {
			escaped = "/"
		}
		// Pinned to the content host: it lives at its path URL there.
		if contentHostOnlySites[user.Handle.String+"/"+site.Name] {
			w.Header().Set("Cache-Control", "no-store")
			http.Redirect(w, r, h.contentBaseURL()+"/"+user.Handle.String+"/"+site.Name+escaped+query, http.StatusFound)
			return
		}
		// A site with its own domain lives there (302, so disconnecting the
		// domain takes effect at once). Its API here refuses saves and
		// sign-in (livesOnDomainElsewhere), like on the person host.
		if !strings.HasPrefix(r.URL.Path, "/v1/") {
			if info, has, err := h.siteOwnDomain(r.Context(), site.ID); err == nil && has {
				w.Header().Set("Cache-Control", "no-store")
				http.Redirect(w, r, "https://"+strings.ToLower(info.Domain)+escaped+query, http.StatusFound)
				return
			}
		}
		if !h.siteHostLive(user.Handle.String, site.Name) {
			// Reachable only when TLS was served for a person whose marker is
			// missing (e.g. cleared by hand): serve the working address.
			w.Header().Set("Cache-Control", "no-store")
			http.Redirect(w, r, h.personPathAddress(user.Handle.String, site.Name)+strings.TrimPrefix(escaped, "/")+query, http.StatusFound)
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
		// A root-absolute link written for an older address of this site
		// (/<handle>/<site>/... on the content host, /<site>/... on the
		// person host) lands here: when no such file exists, send it to the
		// same path at this site's root.
		if rest, ok := h.oldAddressPrefix(user.Handle.String, site, r.URL.Path, escaped); ok {
			http.Redirect(w, r, "/"+strings.TrimLeft(rest, "/")+query, http.StatusMovedPermanently)
			return
		}
		h.serveSiteFileRel(w, r, site.UserID, site.Name, r.URL.Path)
	})
}

// oldAddressPrefix: p starts with /<handle>/<site>/ or /<site>/ and the site
// has no file there. Returns the escaped remainder after the prefix.
func (h *SiteHandler) oldAddressPrefix(handle string, site db.Site, p, escaped string) (string, bool) {
	for _, prefix := range []string{"/" + handle + "/" + site.Name + "/", "/" + site.Name + "/"} {
		if !strings.HasPrefix(p, prefix) || !strings.HasPrefix(escaped, prefix) {
			continue
		}
		if h.siteHasPath(site.UserID, site.Name, p) {
			return "", false
		}
		return strings.TrimPrefix(escaped, prefix), true
	}
	return "", false
}

// siteHasPath reports whether rel names a file or directory in the site's
// live version (os.Root refuses escapes).
func (h *SiteHandler) siteHasPath(userID, siteName, rel string) bool {
	root, err := os.OpenRoot(h.disk.SiteDir(userID, siteName) + "/current")
	if err != nil {
		return false
	}
	defer root.Close()
	name := strings.TrimPrefix(path.Clean("/"+rel), "/")
	if name == "" {
		return true
	}
	_, err = root.Stat(name)
	return err == nil
}

// personPathAddress is https://<handle>.<SITE_DOMAIN>/<site>/.
func (h *SiteHandler) personPathAddress(handle, name string) string {
	return "https://" + h.personHostFor(handle) + "/" + name + "/"
}

// siteAddressWithPath is the site's current address with an escaped path
// (starting with "/") appended: its site host when that is canonical, else
// its person-path address.
func (h *SiteHandler) siteAddressWithPath(handle, name, escapedPath string) string {
	if h.siteHostCanonical(handle, name) {
		return "https://" + h.siteHostFor(handle, name) + "/" + strings.TrimLeft(escapedPath, "/")
	}
	return h.personPathAddress(handle, name) + strings.TrimLeft(escapedPath, "/")
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

// RequestSiteCert asks the root issuer for *.<handle>.<SITE_DOMAIN> by
// creating SITE_CERT_DIR/requests/<handle> (empty; the name is the request).
// A no-op when site hosts are off, there is no hand-off directory, the handle
// cannot be a host or its certificate is already ready.
func (h *SiteHandler) RequestSiteCert(handle string) {
	handle = strings.ToLower(strings.TrimSpace(handle))
	if !h.siteHostsOn() || h.siteCertDir == "" || !handleAddressable(handle) || h.siteCertReady(handle) {
		return
	}
	p := filepath.Join(h.siteCertDir, "requests", handle)
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if !errors.Is(err, os.ErrExist) {
			log.Printf("site certs: request %s: %v", handle, err)
		}
		return
	}
	_ = f.Close()
	log.Printf("site certs: requested *.%s", h.personHostFor(handle))
}

// requestSiteCertsForAll requests a certificate for every person with at
// least one site that has none yet (the back-fill, and a safety net for a
// request that was missed).
func (h *SiteHandler) requestSiteCertsForAll(ctx context.Context) {
	if !h.siteHostsOn() || h.siteCertDir == "" {
		return
	}
	handles, err := db.ListHandlesWithSites(ctx, h.database)
	if err != nil {
		log.Printf("site certs: list handles: %v", err)
		return
	}
	for _, handle := range handles {
		h.RequestSiteCert(handle)
	}
}

// StartSiteCertRequests runs requestSiteCertsForAll now and then every
// interval until ctx ends.
func (h *SiteHandler) StartSiteCertRequests(ctx context.Context, every time.Duration) {
	if !h.siteHostsOn() || h.siteCertDir == "" {
		return
	}
	go func() {
		h.requestSiteCertsForAll(ctx)
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				h.requestSiteCertsForAll(ctx)
			}
		}
	}()
}
