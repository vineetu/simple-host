package hackhttp

import "net/http"

// RegisterHackCompanies serves the company pitch for the self-hosted Simple
// Hack package at /companies.
func RegisterHackCompanies(mux *http.ServeMux, pages hackPages) {
	assets := pages
	mux.Handle("GET /companies", assets.AdminUICSP(assets.ServeStaticPage("hack-companies.html")))
}
