package handler

import "net/http"

// RegisterHackHome serves the simple-hack.app product page at /. The
// integrator calls this when EVENTS=hosted in place of the Simple Host
// homepage.
func RegisterHackHome(mux *http.ServeMux) {
	mux.Handle("GET /{$}", adminUICSP(serveStaticPage("hack-home.html")))
}
