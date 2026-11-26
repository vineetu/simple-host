package pages

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/vsriram/simple-host/products/hack/core"

	"github.com/vsriram/simple-host/host/httpx"
)

// RegisterHackUI serves the signed-in simple-hack.app pages from one HTML shell
// that routes on location.pathname: the new app's (web/hack) for the screens
// it serves, the old static/hack/app.html for the rest (hack_ui_switch.go).
func RegisterHackUI(mux *http.ServeMux, pages Pages, sitesBase string) {
	assets := pages
	serveNewAssets(mux)
	app := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Robots-Tag", "noindex")
		body, isNew, err := appPage(r, assets.StaticFS())
		if err != nil {
			http.NotFound(w, r)
			return
		}
		// The sign-in script tag is only injected on /signin: every other route
		// mounts the app without it, since nothing there needs window.SHSignIn.
		// (/signin is always the old app's.)
		body, err = assets.RenderAppShell(r, body, r.URL.Path == "/signin" && !isNew)
		if err != nil {
			http.Error(w, "Could not load app", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(httpx.StampNonce(r, body))
	})
	page := assets.AdminUICSP(announcementImages(app))
	mux.Handle("GET /e/{slug}/vote", assets.AdminUICSP(assets.ServeStaticPage("hack-voting.html")))
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
		"GET /e/{slug}/apply", // public-apply (owner, 2026-10-31): served signed out, same as /join/{code}.
		"GET /join/{code}",
		"GET /judge/{code}",
		"GET /organiser/{code}",
		"GET /team/{code}", // public-team-link (teams redesign, 2026-10-31): served signed in or out, same as /join/{code}.
	} {
		mux.Handle(p, page)
	}
	// The judge's scoring screen embeds the entry's video and frames the
	// team's site, so only this route widens frame-src (app.js reloads the
	// page when it navigates here from a route served with the narrower one).
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
