package siteshttp

import (
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/vsriram/simple-host/host/httpx"
	appsites "github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/sites/pagepath"
)

// A site may claim a free <name>.<SITE_DOMAIN> address through the ordinary custom-domain binding.
// The platform owns the zone and wildcard cert, so there is nothing to prove: the binding is verified at
// once, first come first served. Once bound the host behaves like a custom domain (site at its root,
// same-origin /v1, host-only __Host- cookie), so it counts as the site's own domain for private
// collections. Unclaimed names keep the legacy 301 (LegacyHostRedirect).

// BoundSubdomains serves claimed <name>.<siteDomain> hosts like a custom domain: /v1/ goes to the API,
// everything else is the site's live files. Any other request (unclaimed names keep their legacy 301) goes to fallback.
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

// ServeSiteFileRel serves one page of a site's live version: no directory listings, nothing outside the
// site's current directory (os.Root refuses escapes, symlinks included), GET and HEAD only. rel is the
// path inside the site; redirects are built from the request's own path so they work at a host root and under /<site>/.
func (h *SiteHandler) ServeSiteFileRel(w http.ResponseWriter, r *http.Request, userID, siteName, rel string) {
	h.serveSiteFile(w, r, userID, siteName, rel, r.URL.EscapedPath())
}

func (h *SiteHandler) serveSiteFile(w http.ResponseWriter, r *http.Request, userID, siteName, rel, publicPath string) {
	h.serveSiteFileWithProbe(w, r, userID, siteName, rel, publicPath, nil)
}

// serveSiteFileWithProbe passes the request through the visitor gate (h.gate) before anything about the
// site's files is answered: probe (which could reveal whether a file exists) runs only once the gate lets it in.
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

// ServeRootFile answers rel inside one version tree as pagepath.Resolve decides: the file, the redirect
// to its other spelling (only the trailing slash differs), or the site's 404.
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
	if h.Ed.Routes().HostedEvents {
		// A hackathon platform never shows a bare "404 page not found": a
		// site without its own 404.html gets the platform's page.
		h.renderNotFoundPage(w, r, "Page not found", "No page lives at this address.", "/", "Go to this site’s home page")
		return
	}
	http.NotFound(w, r)
}
