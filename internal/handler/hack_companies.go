package handler

import "net/http"

// RegisterHackCompanies serves the company pitch for the self-hosted Simple
// Hack package at /companies. It is mounted by RegisterHackHome, so it
// answers on simple-hack.app and on every full self-hosted Simple Hack
// instance (both run with EVENTS=hosted); the page describes the standalone
// package itself, so it reads the same wherever it is reached.
func RegisterHackCompanies(mux *http.ServeMux) {
	mux.Handle("GET /companies", adminUICSP(serveStaticPage("hack-companies.html")))
}
