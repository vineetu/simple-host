package handler

import (
	"io/fs"
	"net/http"
	"regexp"
	"strings"
)

// RegisterHackUI serves the signed-in simple-hack.app pages from one HTML
// shell (static/hack/app.html) that routes on location.pathname. The JSON
// API is registered separately. These pages are not indexed and must not be
// cached: they are the signed-in app, not the public product page.
//
// sitesBase is the domain team sites are served under (team.event.<base>),
// or "" when this server gives sites no hosts of their own; the judge
// route's CSP (hackAppCSP) may frame them.
//
// Takes the hackPages interface, not *SiteHandler (cleanup 3.2 slice 2a):
// this only ever called sh.pipeline().
func RegisterHackUI(mux *http.ServeMux, pages hackPages, sitesBase string) {
	assets := pages
	app := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Robots-Tag", "noindex")
		body, err := fs.ReadFile(assets.StaticFS(), "static/hack/app.html")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		// The sign-in script tag is only injected on /signin: every other
		// route mounts the app without it, since nothing there needs
		// window.SHSignIn. The kit owns this app's presentation, including
		// on the sign-in page (RenderAppShell always renders Hack=false).
		body, err = assets.RenderAppShell(r, body, r.URL.Path == "/signin")
		if err != nil {
			http.Error(w, "Could not load app", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(stampNonce(r, body))
	})
	page := assets.AdminUICSP(app)
	votePage := assets.AdminUICSP(assets.ServeStaticPage("hack-voting.html"))
	mux.Handle("GET /e/{slug}/vote", votePage)
	mux.Handle("GET /e/{slug}/voting-settings", votePage)
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

// hackAppCSP is AdminUICSP for the judge route, whose frame-src also lists
// the four video players videoEmbed builds URLs for and one wildcard for
// team sites (build-plan.md section 6). With no sites base (no per-site
// hosts) the wildcard is left out; entries are then never frameable.
func hackAppCSP(assets hackPages, next http.Handler, sitesBase string) http.Handler {
	src := "'self' blob:"
	if base := strings.Trim(strings.ToLower(sitesBase), "."); hackFrameBase.MatchString(base) {
		src += " https://*." + base
	}
	src += " " + strings.Join(videoFrameOrigins, " ")
	return assets.UICSP(next, src)
}

// hackFrameBase is a plain DNS name with at least one dot, so nothing odd
// (a space, a ';' or a bare TLD) ever reaches the CSP header.
var hackFrameBase = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)
