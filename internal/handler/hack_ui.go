package handler

import "net/http"

// RegisterHackUI serves the signed-in simple-hack.app pages from one HTML
// shell that routes on location.pathname. The JSON API is registered
// separately. These pages are not indexed and must not be cached: they are
// the signed-in app, not the public product page.
func RegisterHackUI(mux *http.ServeMux) {
	inner := serveStaticPage("hack-app.html")
	page := adminUICSP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Robots-Tag", "noindex")
		inner.ServeHTTP(w, r)
	}))
	for _, p := range []string{
		"GET /signin",
		"GET /events",
		"GET /events/new",
		"GET /e/{slug}",
		"GET /e/{slug}/manage",
		"GET /join/{code}",
		"GET /judge/{code}",
	} {
		mux.Handle(p, page)
	}
}
