package hackhttp

import (
	"bytes"
	"net/http"
	"time"

	"github.com/vsriram/simple-host/internal/httpx"
	"github.com/vsriram/simple-host/products/hack/webassets"
)

// homepageLinks is the homepage's Link header value. Duplicated from
// discovery.go's HomepageLinks (internal/handler, cleanup 3.2 slice 3): pure
// data with no instance dependency, so a copy here is simpler than an
// injected value for the one call site that reads it.
const homepageLinks = `</sitemap.xml>; rel="sitemap", </index.md>; rel="alternate"; type="text/markdown", </.well-known/api-catalog>; rel="api-catalog", </openapi.json>; rel="service-desc"`

// RegisterHackHome serves the simple-hack.app product page at /. The
// integrator calls this when EVENTS=hosted in place of the Simple Host
// homepage. The canonical instance uses the video page; self-hosted installs
// keep the drawn film so it can show their own address.
//
// Takes the hackPages interface, not *SiteHandler (cleanup 3.2 slice 2a):
// this only ever called sh.pipeline().
func RegisterHackHome(mux *http.ServeMux, pages hackPages) {
	assets := pages
	registerHackathons(mux, pages)
	story := assets.ServeStaticPage("hack-story.html")
	home := assets.ServeStaticPage("hack-home.html")
	canonical := assets.IsCanonicalInstance()
	page := "hack-story.html"
	if canonical {
		page = "hack-home.html"
	}
	markdown := assets.LandingMarkdownHandler(page, "Simple Hack", "Create events, join teams, and judge entries.", assets.InkBaseURL(), true)
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
		if canonical {
			home.ServeHTTP(w, r)
		} else {
			story.ServeHTTP(w, r)
		}
	})))
	mux.Handle("GET /directory", assets.AdminUICSP(assets.ServeStaticPage("hack-directory.html")))
	RegisterHackWatch(mux, pages)
	RegisterHackGetStarted(mux, pages)
	RegisterHackCompanies(mux, pages)
}

func registerHackathons(mux *http.ServeMux, pages hackPages) {
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
