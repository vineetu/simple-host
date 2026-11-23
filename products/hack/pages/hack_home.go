package pages

import (
	"bytes"
	"net/http"
	"time"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/products/hack/webassets"
)

// homepageLinks is the homepage's Link header value (a copy of host/handler's
// HomepageLinks: pure data, simpler than an injected value).
const homepageLinks = `</sitemap.xml>; rel="sitemap", </index.md>; rel="alternate"; type="text/markdown", </.well-known/api-catalog>; rel="api-catalog", </openapi.json>; rel="service-desc"`

// RegisterHackHome serves the simple-hack.app product page at /.
func RegisterHackHome(mux *http.ServeMux, pages Pages, product *Assets) {
	assets := pages
	registerHackathons(mux, pages)
	home := assets.ServeStaticPage("hack-home.html")
	markdown := assets.LandingMarkdownHandler("hack-home.html", "Simple Hack", "Create events, join teams, and judge entries.", assets.InkBaseURL(), true)
	mux.HandleFunc("GET /index.md", markdown)
	mux.HandleFunc("GET /llms.md", markdown)
	mux.Handle("GET /{$}", assets.AdminUICSP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", "Accept")
		w.Header().Set("Link", homepageLinks)
		// Existing email and Google returns are consumed by the sign-in shell,
		// which checks the browser nonce. The landing film has no sign-in logic.
		if q := r.URL.Query(); q.Has("token") || q.Has("cn") {
			w.Header().Set("Cache-Control", "no-store")
			http.Redirect(w, r, "/signin?"+r.URL.RawQuery, http.StatusFound)
			return
		}
		if httpx.AcceptsMarkdown(r) {
			markdown(w, r)
			return
		}
		home.ServeHTTP(w, r)
	})))
	mux.Handle("GET /directory", assets.AdminUICSP(assets.ServeStaticPage("hack-directory.html")))
	RegisterHackWatch(mux, pages)
	RegisterHackGetStarted(mux, pages, product)
	RegisterHackCompanies(mux, pages)
}

func registerHackathons(mux *http.ServeMux, pages Pages) {
	raw, err := webassets.Static.ReadFile("static/hackathons.html")
	if err != nil {
		panic(err)
	}
	page := pages.AdminUICSP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := pages.RenderPage(r, "hackathons.html", raw)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		if r.URL.Path == "/hackathons.html" {
			http.ServeContent(w, r, "hackathons.html", time.Time{}, bytes.NewReader(body))
			return
		}
		_, _ = w.Write(body)
	}))
	mux.Handle("GET /hackathons", page)
	mux.Handle("GET /hackathons.html", page)
}
