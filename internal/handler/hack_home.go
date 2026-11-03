package handler

import "net/http"

// RegisterHackHome serves the simple-hack.app product page at /. The
// integrator calls this when EVENTS=hosted in place of the Simple Host
// homepage. The canonical instance uses the video page; self-hosted installs
// keep the drawn film so it can show their own address.
//
// Takes the hackPages interface, not *SiteHandler (cleanup 3.2 slice 2a):
// this only ever called sh.pipeline().
func RegisterHackHome(mux *http.ServeMux, pages hackPages) {
	assets := pages
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
		w.Header().Set("Link", HomepageLinks)
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
	mux.Handle("GET /directory", assets.AdminUICSP(assets.ServeStaticPage("hack-directory.html")))
	RegisterHackWatch(mux, pages)
	RegisterHackGetStarted(mux, pages)
	RegisterHackCompanies(mux, pages)
}
