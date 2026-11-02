package handler

import (
	"bytes"
	"net/http"
	"regexp"
	"strings"
)

// RegisterHackUI serves the signed-in simple-hack.app pages from one HTML
// shell that routes on location.pathname. The JSON API is registered
// separately. These pages are not indexed and must not be cached: they are
// the signed-in app, not the public product page.
//
// sitesBase is the domain team sites are served under (team.event.<base>),
// or "" when this server gives sites no hosts of their own; the judge
// route's CSP (hackAppCSP) may frame them.
func RegisterHackUI(mux *http.ServeMux, sitesBase string) {
	classic := serveStaticPage("hack-app.html")
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := staticFiles.ReadFile("static/hack/app.html")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path != "/signin" {
			body = bytes.ReplaceAll(body, []byte(markerSignin), nil)
		}
		// The kit owns this app's presentation, including on the sign-in page.
		data := chromeDataFor(r, "")
		data.Hack = false
		body, err = withChrome(body, data)
		if err != nil {
			http.Error(w, "Could not load app", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(stampNonce(r, body))
	})
	app := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Robots-Tag", "noindex")
		useNext := true // The new app is the default (owner, 2026-10-30); ?ui=classic keeps the old one reachable.
		if cookie, err := r.Cookie("sh_hack_ui"); err == nil {
			useNext = cookie.Value == "next"
		}
		// "new" is accepted as an alias of "next" (Grok retest BROKEN #1:
		// classic is sticky once chosen via a cookie no page offers a way back
		// out of; a visitor guessing the opposite of ?ui=classic typed ?ui=new
		// and got no effect).
		if choice := r.URL.Query().Get("ui"); choice == "next" || choice == "new" || choice == "classic" {
			useNext = choice != "classic"
			cookie := &http.Cookie{Name: "sh_hack_ui", Value: "next", Path: "/", SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https"), MaxAge: 365 * 24 * 60 * 60}
			if !useNext {
				cookie.Value = "classic" // remembered, now that next is the default
			}
			http.SetCookie(w, cookie)
		}
		// Sign-in stays on the shared page (site chrome, SSO handoff); people
		// return to the new app afterwards because the cookie is kept.
		if useNext && r.URL.Path != "/signin" {
			next.ServeHTTP(w, r)
		} else {
			classic.ServeHTTP(w, r)
		}
	})
	page := adminUICSP(app)
	votePage := adminUICSP(serveStaticPage("hack-voting.html"))
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
	mux.Handle("GET /e/{slug}/judge", hackAppCSP(app, sitesBase))
}

// hackAppCSP is adminUICSP for the judge route, whose frame-src also lists
// the four video players videoEmbed builds URLs for and one wildcard for
// team sites (build-plan.md section 6). With no sites base (no per-site
// hosts) the wildcard is left out; entries are then never frameable.
func hackAppCSP(next http.Handler, sitesBase string) http.Handler {
	src := "'self' blob:"
	if base := strings.Trim(strings.ToLower(sitesBase), "."); hackFrameBase.MatchString(base) {
		src += " https://*." + base
	}
	src += " " + strings.Join(videoFrameOrigins, " ")
	return uiCSP(next, src)
}

// hackFrameBase is a plain DNS name with at least one dot, so nothing odd
// (a space, a ';' or a bare TLD) ever reaches the CSP header.
var hackFrameBase = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)
