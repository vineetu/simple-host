package siteshttp

import (
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
	appsites "github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/sites/pagepath"
)

// A free <name>.<SITE_DOMAIN> address (owner decision 2026-09-24).
//
// A site can claim one single-label name under the platform domain, e.g.
// clayandkiln.simple-host.app, through the ordinary custom-domain binding (the
// same sites.custom_domain column, the same POST /v1/sites/{site}/domain and
// connect_domain tool). Because the platform owns the zone, the wildcard
// certificate and the wildcard DNS record, there is nothing for the person to
// prove: the binding is verified the moment it is made, and it is first come,
// first served. Once bound, the host behaves exactly like a custom domain — the
// site is served at its root, /v1 is same-origin, visitors sign in there with a
// host-only __Host- cookie — and so it counts as the site's own domain for
// private collections. Names nobody has claimed keep the legacy 301 to the path
// URL (LegacyHostRedirect).

// platformSubdomainLabel reports whether host is exactly one DNS label under
// siteDomain (e.g. "clay" for clay.simple-host.app) and returns that label.
// Multi-label hosts (x.lab.simple-host.app), the apex and non-platform hosts
// return ok=false.
func platformSubdomainLabel(host, siteDomain string) (string, bool) {
	return appsites.SubdomainLabel(host, siteDomain)
}

var errSubdomainReserved = appsites.ErrSubdomainReserved

// claimableSubdomain validates a requested <label>.<siteDomain>: one DNS label,
// letters/digits/hyphens, no leading or trailing hyphen, not reserved, and not
// one of the platform's own hosts (content host, CNAME target). While the
// base moves (sitebase.go) a name under either domain is accepted — older
// skills write the old one — and the name is always kept under the base
// handed out.
func (h *SiteHandler) claimableSubdomain(host string) (string, error) {
	label, _, ok := h.Hosts.UserHostLabel(host)
	base := h.Hosts.Addressing().HandoutBase()
	if !ok {
		return "", errors.New("a free address is one name under " + base + ", e.g. my-shop." + base)
	}
	return appsites.ClaimPlatformAddress(label, base, h.SiteDomain, h.ContentHost, h.CNAMETarget)
}

// bindPlatformSubdomain claims <label>.<siteDomain> for site: verified at once,
// refused if any other site holds it.
func (h *SiteHandler) bindPlatformSubdomain(w http.ResponseWriter, r *http.Request, site model.Site, requested string) {
	host, err := h.claimableSubdomain(requested)
	if err != nil {
		code := "invalid_name"
		if errors.Is(err, errSubdomainReserved) {
			code = "name_reserved"
		}
		httpx.WriteError(w, http.StatusBadRequest, code, err.Error())
		return
	}
	released, err := h.opsSitesSvc().ClaimPlatformSubdomain(r.Context(), site.ID, host)
	if err != nil {
		if errors.Is(err, model.ErrNameIsAccountAddress) {
			httpx.WriteError(w, http.StatusConflict, "domain_taken", "that name is someone's own address; pick another name")
			return
		}
		if errors.Is(err, model.ErrDomainTaken) {
			httpx.WriteError(w, http.StatusConflict, "domain_taken", "that address is taken by another site; pick another name")
			return
		}
		httpx.WriteInternal(w)
		return
	}
	// A site has one address of its own; the ones it replaces stop serving it
	// (a replaced claimed name redirects here: it was kept as a retired name).
	for _, d := range released {
		if err := h.Disk.UnbindDomain(d); err != nil {
			log.Printf("domain: unbind previous %s for %s: %v", d, site.Name, err)
		}
	}
	if err := h.Disk.BindDomain(site.UserID, site.Name, host); err != nil {
		log.Printf("domain: bind %s for %s/%s: %v", host, site.UserID, site.Name, err)
	}
	h.Hosts.SyncDomainRedirect(r.Context(), site.ID)
	now := time.Now()
	httpx.WriteJSON(w, http.StatusOK, domainResponse{
		Domain:     host,
		Status:     "active",
		BoundAt:    &now,
		VerifiedAt: &now,
	})
}

// BoundSubdomains serves claimed <name>.<siteDomain> hosts the way nginx serves
// a custom domain: /v1/ goes to the API (same-origin state, collections and
// visitor sign-in), everything else is the site's files from its live version.
// Every other request — unclaimed names included, which keep their legacy 301 —
// goes to fallback.
func (h *SiteHandler) BoundSubdomains(api, fallback http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := httpx.HostName(r)
		if !h.Hosts.IsPlatformSubdomain(host) {
			fallback.ServeHTTP(w, r)
			return
		}
		info, ok := h.Hosts.BoundPlatformSite(r.Context(), host)
		if !ok {
			fallback.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/v1/") {
			api.ServeHTTP(w, r)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		h.ServeSiteFileRel(w, r, info.UserID, info.Name, r.URL.Path)
	})
}

// ServeSiteFileRel serves one page of a site's live version: no directory
// listings, nothing outside the site's current directory (os.Root refuses
// escapes, symlinks included), GET and HEAD only.
//
// rel is the path inside the site ("/" is its root). A redirect to a page's
// other spelling is built from the request's own path, so it works for a
// site served at a host's root and for one served under /<site>/.
func (h *SiteHandler) ServeSiteFileRel(w http.ResponseWriter, r *http.Request, userID, siteName, rel string) {
	h.serveSiteFile(w, r, userID, siteName, rel, r.URL.EscapedPath())
}

// serveSiteFile is ServeSiteFileRel with the (escaped, server-built) public
// path a page redirect adds its "/" to or removes it from.
func (h *SiteHandler) serveSiteFile(w http.ResponseWriter, r *http.Request, userID, siteName, rel, publicPath string) {
	h.serveSiteFileWithProbe(w, r, userID, siteName, rel, publicPath, nil)
}

// serveSiteFileWithProbe opens the site's folder and passes the request
// through the visitor gate (h.gate) before anything about the site's files
// is answered: probe (a site-host legacy redirect or a selected-home
// collision, which could reveal whether a file exists) runs only once the
// gate lets the request in.
func (h *SiteHandler) serveSiteFileWithProbe(w http.ResponseWriter, r *http.Request, userID, siteName, rel, publicPath string, probe func() bool) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// Keep the directory we checked even if its name is reused mid-request.
	siteRoot, err := os.OpenRoot(h.Disk.SiteDir(userID, siteName))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer siteRoot.Close()
	then := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if probe != nil && probe() {
			return
		}
		if h.applyRootRedirect(w, r, siteRoot, rel, publicPath) {
			return
		}
		root, err := siteRoot.OpenRoot("current")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer root.Close()
		h.ServeRootFile(w, r, root, rel, publicPath)
	})
	h.Gate.ServeHTTP(w, appsites.WithServing(r, appsites.Serving{UserID: userID, Name: siteName, Rel: rel, Root: siteRoot, Then: then}))
}

// serveDirFile serves rel from dir the way serveSiteFile does (dir is a
// version tree: the live `current`, or a kept version for a preview).
func (h *SiteHandler) serveDirFile(w http.ResponseWriter, r *http.Request, dir, rel, publicPath string) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer root.Close()

	h.ServeRootFile(w, r, root, rel, publicPath)
}

// revalidatePage lets a later publish change a redirect or a rule's answer
// at once; a gate's private policy stands.
func revalidatePage(w http.ResponseWriter) {
	if w.Header().Get("Cache-Control") == "" {
		w.Header().Set("Cache-Control", "public, max-age=0, must-revalidate")
	}
}

// ServeRootFile answers rel inside one version tree as pagepath.Resolve
// decides: the page's file, the redirect to its other spelling, or the
// site's 404. publicPath is the escaped address being asked for; a redirect
// only adds or removes its trailing slash, so it lands on the page itself.
func (h *SiteHandler) ServeRootFile(w http.ResponseWriter, r *http.Request, root *os.Root, rel, publicPath string) {
	page := pagepath.Resolve(root, rel)
	switch page.Kind {
	case pagepath.NotFound:
		h.siteNotFound(w, r, root)
		return
	case pagepath.AddSlash, pagepath.DropSlash:
		target := publicPath + "/"
		if page.Kind == pagepath.DropSlash {
			// Every trailing slash goes, however it was spelled.
			for target = publicPath; ; {
				t := strings.TrimSuffix(strings.TrimSuffix(strings.TrimRight(target, "/"), "%2F"), "%2f")
				if t == target {
					break
				}
				target = t
			}
		}
		// Only ever a path on this host: "//x" would leave it.
		target = "/" + strings.TrimLeft(target, "/")
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
		revalidatePage(w)
		http.Redirect(w, r, target, http.StatusMovedPermanently)
		return
	}
	f, err := root.Open(page.File)
	if err != nil {
		h.siteNotFound(w, r, root)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		h.siteNotFound(w, r, root)
		return
	}
	http.ServeContent(w, r, st.Name(), st.ModTime(), f)
}

// siteNotFound answers 404 with the site's own 404.html when it has one.
func (h *SiteHandler) siteNotFound(w http.ResponseWriter, r *http.Request, root *os.Root) {
	if f, err := root.Open("404.html"); err == nil {
		defer f.Close()
		if st, err := f.Stat(); err == nil && !st.IsDir() {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusNotFound)
			if r.Method != http.MethodHead {
				_, _ = io.Copy(w, f)
			}
			return
		}
	}
	http.NotFound(w, r)
}
