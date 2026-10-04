package handler

import "net/http"

// RegisterHackHome serves the simple-hack.app product page at /. The
// integrator calls this when EVENTS=hosted in place of the Simple Host
// homepage.
func RegisterHackHome(mux *http.ServeMux) {
	mux.Handle("GET /{$}", adminUICSP(serveStaticPage("hack-story.html")))
	mux.Handle("GET /directory", adminUICSP(serveStaticPage("hack-directory.html")))
	RegisterHackGetStarted(mux)
}
