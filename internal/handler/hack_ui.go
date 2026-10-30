package handler

import (
	"bytes"
	"net/http"
	"strings"
)

// RegisterHackUI serves the signed-in simple-hack.app pages from one HTML
// shell that routes on location.pathname. The JSON API is registered
// separately. These pages are not indexed and must not be cached: they are
// the signed-in app, not the public product page.
func RegisterHackUI(mux *http.ServeMux) {
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
	page := adminUICSP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Robots-Tag", "noindex")
		useNext := false // Keep classic as the default until every role is verified.
		if cookie, err := r.Cookie("sh_hack_ui"); err == nil {
			useNext = cookie.Value == "next"
		}
		if choice := r.URL.Query().Get("ui"); choice == "next" || choice == "classic" {
			useNext = choice == "next"
			cookie := &http.Cookie{Name: "sh_hack_ui", Value: "next", Path: "/", SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https"), MaxAge: 365 * 24 * 60 * 60}
			if !useNext {
				cookie.Value = ""
				cookie.MaxAge = -1
			}
			http.SetCookie(w, cookie)
		}
		if useNext {
			next.ServeHTTP(w, r)
		} else {
			classic.ServeHTTP(w, r)
		}
	}))
	votePage := adminUICSP(serveStaticPage("hack-voting.html"))
	mux.Handle("GET /e/{slug}/vote", votePage)
	mux.Handle("GET /e/{slug}/voting-settings", votePage)
	for _, p := range []string{
		"GET /signin",
		"GET /account",
		"GET /events",
		"GET /events/new",
		"GET /e/{slug}/team",
		"GET /e/{slug}/judge",
		"GET /e/{slug}/manage",
		"GET /join/{code}",
		"GET /judge/{code}",
		"GET /organiser/{code}",
	} {
		mux.Handle(p, page)
	}
}
