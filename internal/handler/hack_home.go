package handler

import "net/http"

// RegisterHackHome serves the simple-hack.app product page at /. The
// integrator calls this when EVENTS=hosted in place of the Simple Host
// homepage. The canonical instance uses the video page; self-hosted installs
// keep the drawn film so it can show their own address.
func RegisterHackHome(mux *http.ServeMux, sh *SiteHandler) {
	assets := sh.pipeline()
	story := assets.serveStaticPage("hack-story.html")
	home := assets.serveStaticPage("hack-home.html")
	canonical := assets.inkBaseURL() == canonicalHackBaseURL
	page := "hack-story.html"
	if canonical {
		page = "hack-home.html"
	}
	markdown := landingMarkdownHandler(assets, page, "Simple Hack", "Create events, join teams, and judge entries.", assets.inkBaseURL(), true)
	mux.HandleFunc("GET /index.md", markdown)
	mux.HandleFunc("GET /llms.md", markdown)
	mux.Handle("GET /{$}", assets.adminUICSP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", "Accept")
		w.Header().Set("Link", homepageLinks)
		// Existing email and Google returns are consumed by the sign-in shell,
		// which checks the browser nonce. The landing film has no sign-in logic.
		if q := r.URL.Query(); q.Has("token") || q.Has("cn") {
			w.Header().Set("Cache-Control", "no-store")
			http.Redirect(w, r, "/signin?"+r.URL.RawQuery, http.StatusFound)
			return
		}
		if acceptsMarkdown(r) {
			markdown(w, r)
			return
		}
		if canonical {
			home.ServeHTTP(w, r)
		} else {
			story.ServeHTTP(w, r)
		}
	})))
	mux.Handle("GET /directory", assets.adminUICSP(assets.serveStaticPage("hack-directory.html")))
	RegisterHackWatch(mux, sh)
	RegisterHackGetStarted(mux, sh)
	RegisterHackCompanies(mux, sh)
}
