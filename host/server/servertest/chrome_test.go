package servertest

import (
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/sites/siteshttp"
	"github.com/vsriram/simple-host/host/web"

	"github.com/vsriram/simple-host/host/access/accesshttp"
	"github.com/vsriram/simple-host/host/edition"
)

// chromeTestHandler is a SiteHandler with no database. Every route exercised
// here avoids the DB: handle-shaped paths are the only ones that query it.
func chromeTestHandler(kind *edition.Edition) *siteshttp.SiteHandler {
	return chromeTestHandlerFor(kind)
}

// chromeTestHandlerFor is chromeTestHandler for an explicit edition (e.g.
// edition.ForKindWithLimits with an override).
func chromeTestHandlerFor(ed *edition.Edition) *siteshttp.SiteHandler {
	return WithHosts(&siteshttp.SiteHandler{Ed: ed, Assets: testAssetPipeline(ed, ""), Limits: ed.Limits(), SiteDomain: "simple-host.app", ContentHost: "sites.simple-host.app"})
}

func chromeTestMux(t *testing.T, kind *edition.Edition) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	siteshttp.RegisterUIRoutes(mux, "https://simple-host.app", chromeTestHandler(kind))
	return mux
}

func get(t *testing.T, h http.Handler, host, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if host != "" {
		req.Host = host
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

var ariaCurrentRe = regexp.MustCompile(`<a [^>]*aria-current="page"[^>]*>`)

// assertChrome checks one served page: the chrome once, no leaked markers, no
// hand-written nav left over, and aria-current on exactly the expected link
// (wantCurrentHref "" means no link should carry it).
func assertChrome(t *testing.T, label, body, wantCurrentHref string) {
	t.Helper()
	for _, want := range []struct {
		needle string
		n      int
	}{
		{`class="sh-header"`, 1},
		{`class="sh-footer"`, 1},
		{`<header`, 1},
		{`<footer`, 1},
		{`id="dash-link"`, 1},
		{`id="theme-toggle"`, 1},
		// Sign out: one implementation, reachable inline (desktop) and from
		// the phone account menu.
		{`window.shSignOut = function`, 1},
		{`data-sh-signout>`, 2},
		{`id="sh-acct-menu"`, 1},
		{`localStorage.removeItem(k)`, 1},
		{`/site.css?v=` + web.SiteCSSVersion, 1},
	} {
		if got := strings.Count(body, want.needle); got != want.n {
			t.Errorf("%s: %q appears %d times, want %d", label, want.needle, got, want.n)
		}
	}
	// The credit line is left off the enterprise pages (owner, 2026-09-26).
	credit := 1
	if wantCurrentHref == "/enterprise" {
		credit = 0
	}
	if got := strings.Count(body, `With thanks to Jacob Cole and Tejas D Channappa.`); got != credit {
		t.Errorf("%s: credit line appears %d times, want %d", label, got, credit)
	}
	for _, banned := range []string{"<!--sh:", `href="/showcase"`, `href='/showcase'`, ">Examples<", `nav class="top"`, `class="theme-toggle"`, `id="tt"`} {
		if strings.Contains(body, banned) {
			t.Errorf("%s: served page contains %q", label, banned)
		}
	}
	cur := ariaCurrentRe.FindAllString(body, -1)
	if wantCurrentHref == "" {
		if len(cur) != 0 {
			t.Errorf("%s: aria-current on %v, want none", label, cur)
		}
		return
	}
	if len(cur) != 1 || !strings.Contains(cur[0], `href="`+wantCurrentHref+`"`) {
		t.Errorf("%s: aria-current on %v, want exactly the link to %s", label, cur, wantCurrentHref)
	}
}

// TestServeChromeForScreenshots is not a test: with CHROME_SERVE_ADDR set it
// serves the UI routes (plus a sample showcase at /jane) so the pages can be
// rendered in a real browser. Skipped otherwise.
func TestServeChromeForScreenshots(t *testing.T) {
	addr := os.Getenv("CHROME_SERVE_ADDR")
	if addr == "" {
		t.Skip("CHROME_SERVE_ADDR unset")
	}
	h := chromeTestHandler(platformTestEdition())
	assets := h.Pipeline()
	mux := http.NewServeMux()
	siteshttp.RegisterUIRoutes(mux, "http://"+addr, h)
	sample := web.ShowcaseData{Handle: "jane", SitesBaseURL: "https://sites.simple-host.app", PublicShowcaseURL: "https://jane.simple-host.app/", OwnerAppURL: "https://simple-host.app/jane", MainURL: "https://simple-host.app",
		Sites: []web.ShowcaseSite{{Name: "recipes", URL: "https://recipes.jane.simple-host.app/", CreatedAt: time.Now(), Visibility: "public"}}}
	// Both under the apex CSP, as they are served in production.
	mux.Handle("GET /jane", assets.AdminUICSP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page, err := assets.ShowcasePage(assets.ChromeDataFor(r, ""), sample)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(httpx.StampNonce(r, page))
	})))
	mux.Handle("GET /_404", assets.AdminUICSP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { h.RenderNotFound(w, r, "/nothing-here.html") })))
	mux.HandleFunc("GET /_offline", h.ServeOffline)
	mux.HandleFunc("GET /_takendown", (&accesshttp.AccessHandler{Hosts: h.Hosts}).ServeTakedown)
	mux.Handle("GET /_wizard", assets.ServeStaticPage("setup.html"))
	srv := &http.Server{Addr: addr, Handler: mux}
	go srv.ListenAndServe()
	d, _ := time.ParseDuration(os.Getenv("CHROME_SERVE_FOR"))
	if d == 0 {
		d = 5 * time.Minute
	}
	time.Sleep(d)
	srv.Close()
}
