package handler

import "net/http"

// RegisterHackHome serves the simple-hack.app product page at /. The
// integrator calls this when EVENTS=hosted in place of the Simple Host
// homepage.
func RegisterHackHome(mux *http.ServeMux) {
	story := serveStaticPage("hack-story.html")
	mux.Handle("GET /{$}", adminUICSP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Existing email and Google returns are consumed by the sign-in shell,
		// which checks the browser nonce. The landing film has no sign-in logic.
		if q := r.URL.Query(); q.Has("token") || q.Has("cn") {
			w.Header().Set("Cache-Control", "no-store")
			http.Redirect(w, r, "/signin?"+r.URL.RawQuery, http.StatusFound)
			return
		}
		story.ServeHTTP(w, r)
	})))
	mux.Handle("GET /directory", adminUICSP(serveStaticPage("hack-directory.html")))
	RegisterHackWatch(mux)
	RegisterHackGetStarted(mux)
}
