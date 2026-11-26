package pages

import (
	"io/fs"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/vsriram/simple-host/products/hack/core"
	"github.com/vsriram/simple-host/products/hack/web/dist"

	"github.com/vsriram/simple-host/host/httpx"
)

// RegisterHackUI serves the signed-in and link pages of simple-hack.app: every
// address below answers the app's index.html (web/hack, embedded from
// products/hack/web/dist), which routes on location.pathname.
func RegisterHackUI(mux *http.ServeMux, pages Pages, sitesBase string) {
	assets := pages
	serveAppAssets(mux)
	app := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Robots-Tag", "noindex")
		body, err := fs.ReadFile(dist.Files, "files/index.html")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		body, err = assets.RenderAppShell(r, body)
		if err != nil {
			http.Error(w, "Could not load app", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(httpx.StampNonce(r, body))
	})
	page := assets.AdminUICSP(announcementImages(app))
	// The old voting settings page lives on in the Results section; an old bookmark lands there.
	mux.HandleFunc("GET /e/{slug}/voting-settings", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/e/"+url.PathEscape(r.PathValue("slug"))+"/manage?s=results", http.StatusFound)
	})
	for _, p := range []string{
		"GET /signin",
		"GET /account",
		"GET /events",
		"GET /events/new",
		"GET /e/{slug}/team",
		"GET /e/{slug}/manage",
		"GET /e/{slug}/vote",  // People's choice.
		"GET /e/{slug}/apply", // public-apply (owner, 2026-10-31): served signed out, same as /join/{code}.
		"GET /join/{code}",
		"GET /judge/{code}",
		"GET /organiser/{code}",
		"GET /team/{code}", // public-team-link (teams redesign, 2026-10-31): served signed in or out, same as /join/{code}.
	} {
		mux.Handle(p, page)
	}
	// The judge's scoring screen embeds the entry's video and frames the
	// team's site, so only this route widens frame-src.
	mux.Handle("GET /e/{slug}/judge", hackAppCSP(assets, app, sitesBase))
}

// hackAppCSP is AdminUICSP for the judge route, whose frame-src also lists the four video players core.VideoEmbed builds URLs for and one wildcard for team sites.
func hackAppCSP(assets Pages, next http.Handler, sitesBase string) http.Handler {
	src := "'self' blob:"
	if base := strings.Trim(strings.ToLower(sitesBase), "."); hackFrameBase.MatchString(base) {
		src += " https://*." + base
	}
	src += " " + strings.Join(core.VideoFrameOrigins, " ")
	return assets.UICSP(announcementImages(next), src)
}

// hackFrameBase is a plain DNS name with at least one dot, so nothing odd
// (a space, a ';' or a bare TLD) ever reaches the CSP header.
var hackFrameBase = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)

// announcementImages lets the Hack app shell show YouTube thumbnails on announcement cards: img-src gains https://i.ytimg.com and nothing else.
func announcementImages(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		csp := w.Header().Get("Content-Security-Policy")
		w.Header().Set("Content-Security-Policy", strings.Replace(csp, "img-src 'self' data:", "img-src 'self' data: https://i.ytimg.com", 1))
		next.ServeHTTP(w, r)
	})
}

// serveAppAssets answers /hack/app/* from the app's build. Names carry a
// content hash, so they are cached for good.
func serveAppAssets(mux *http.ServeMux) {
	sub, err := fs.Sub(dist.Files, "files")
	if err != nil {
		panic(err)
	}
	files := http.FileServerFS(sub)
	mux.Handle("GET /hack/app/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		files.ServeHTTP(w, r)
	}))
}
