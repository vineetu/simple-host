package handler

import "net/http"

// RegisterHackHome serves the simple-hack.app product page at /. The
// integrator calls this when EVENTS=hosted in place of the Simple Host
// homepage. The canonical instance uses the video page; self-hosted installs
// keep the drawn film so it can show their own address.
func RegisterHackHome(mux *http.ServeMux) {
	story := serveStaticPage("hack-story.html")
	home := serveStaticPage("hack-home.html")
	page := "hack-story.html"
	if hackInkBaseURL == "https://simple-hack.app" {
		page = "hack-home.html"
	}
	markdown := landingMarkdownHandler(page, "Simple Hack", "Create events, join teams, and judge entries.", hackInkBaseURL)
	mux.HandleFunc("GET /index.md", markdown)
	mux.HandleFunc("GET /llms.md", markdown)
	mux.Handle("GET /{$}", adminUICSP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", "Accept")
		w.Header().Set("Link", homepageLinks)
		// Existing email and Google returns are consumed by the sign-in shell,
		// which checks the browser nonce. The landing film has no sign-in logic.
		if q := r.URL.Query(); q.Has("token") || q.Has("cn") {
			w.Header().Set("Cache-Control", "no-store")
			http.Redirect(w, r, "/signin?"+r.URL.RawQuery, http.StatusFound)
			return
		}
		// A ?from= link is remembered for a later sign-up, as on Simple Host.
		setArrivalCookie(w, r)
		if acceptsMarkdown(r) {
			markdown(w, r)
			return
		}
		if hackInkBaseURL == "https://simple-hack.app" {
			home.ServeHTTP(w, r)
		} else {
			story.ServeHTTP(w, r)
		}
	})))
	mux.Handle("GET /directory", adminUICSP(serveStaticPage("hack-directory.html")))
	RegisterHackWatch(mux)
	RegisterHackGetStarted(mux)
	RegisterHackCompanies(mux)
}
