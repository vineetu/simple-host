package hackhttp

import "net/http"

// RegisterHackCompanies serves the company pitch for the self-hosted Simple
// Hack package at /companies. It is mounted by RegisterHackHome, so it
// answers on simple-hack.app and on every full self-hosted Simple Hack
// instance (both run with EVENTS=hosted); the page describes the standalone
// package itself, so it reads the same wherever it is reached.
//
// Takes the hackPages interface, not *SiteHandler (cleanup 3.2 slice 2a):
// this only ever called sh.pipeline().
func RegisterHackCompanies(mux *http.ServeMux, pages hackPages) {
	assets := pages
	mux.Handle("GET /companies", assets.AdminUICSP(assets.ServeStaticPage("hack-companies.html")))
}
