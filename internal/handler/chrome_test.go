package handler

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

// chromeTestHandler is a SiteHandler with no database. Every route exercised
// here avoids the DB: handle-shaped paths are the only ones that query it.
func chromeTestHandler() *SiteHandler {
	return &SiteHandler{siteDomain: "simple-host.app", contentHost: "sites.simple-host.app"}
}

func chromeTestMux(t *testing.T) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	RegisterUIRoutes(mux, "https://simple-host.app", chromeTestHandler())
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
		{`/site.css?v=` + siteCSSVersion, 1},
		{`With thanks to Jacob Cole and Tejas D Channappa.`, 1},
	} {
		if got := strings.Count(body, want.needle); got != want.n {
			t.Errorf("%s: %q appears %d times, want %d", label, want.needle, got, want.n)
		}
	}
	for _, banned := range []string{"<!--sh:", "/showcase\"", "/showcase'", ">Examples<", `nav class="top"`, `class="theme-toggle"`, `id="tt"`} {
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

func TestChromeOnEveryMainOriginPage(t *testing.T) {
	mux := chromeTestMux(t)
	for _, tc := range []struct {
		path, current string
	}{
		// the file server
		{"/", "/"},
		{"/install.html", "/install.html"},
		{"/docs.html", "/docs.html"},
		{"/architecture.html", "/architecture.html"},
		{"/privacy.html", "/privacy.html"},
		{"/features.html", "/features"},
		{"/enterprise.html", "/enterprise"},
		{"/hackathons.html", "/hackathons"},
		// serveStaticPage
		{"/dashboard", "/dashboard"},
		{"/features", "/features"},
		{"/enterprise", "/enterprise"},
		{"/enterprise/brief", "/enterprise"},
		{"/enterprise/architecture", "/enterprise"},
		{"/hackathons", "/hackathons"},
		{"/admin", ""},
		{"/analytics/my-site", ""},
	} {
		rec := get(t, mux, "simple-host.app", tc.path)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status %d", tc.path, rec.Code)
			continue
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Errorf("%s: Content-Type %q", tc.path, ct)
		}
		body := rec.Body.String()
		assertChrome(t, tc.path, body, tc.current)
		// Main-origin chrome links are relative.
		if !strings.Contains(body, `<a href="/install.html"`) {
			t.Errorf("%s: header link to Get started is not relative", tc.path)
		}
	}
}

func TestChromeOnShowcaseAndNotFound(t *testing.T) {
	h := chromeTestHandler()

	// The 404 via the nginx fallback, as the content host reaches it.
	req := httptest.NewRequest(http.MethodGet, "/internal/notfound", nil)
	req.Header.Set("X-Original-URI", "/nothing-here.html")
	rec := httptest.NewRecorder()
	h.notFound(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("notfound status %d", rec.Code)
	}
	body := rec.Body.String()
	assertChrome(t, "notfound", body, "")
	if strings.Contains(body, "__SH_") {
		t.Error("notfound: template placeholder left unsubstituted")
	}
	// Off the main origin, chrome links must name the main site.
	for _, want := range []string{`href="https://simple-host.app/install.html"`, `href="https://simple-host.app/site.css?v=`, `href="https://simple-host.app/dashboard"`, `location.replace('https:\/\/simple-host.app/')`} {
		if !strings.Contains(body, want) {
			t.Errorf("notfound on the content host: missing %s", want)
		}
	}

	// The showcase route with a reserved handle falls to renderNotFound
	// without a database lookup.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /internal/showcase/{handle}", h.showcase)
	rec = get(t, mux, "sites.simple-host.app", "/internal/showcase/sites")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("reserved showcase status %d", rec.Code)
	}
	assertChrome(t, "showcase→notfound", rec.Body.String(), "")

	// The showcase page itself, on both origins.
	data := showcaseData{Handle: "jane", SitesBaseURL: "https://sites.simple-host.app", MainURL: "https://simple-host.app", Sites: []showcaseSite{}}
	for _, tc := range []struct{ path, base string }{
		{"/jane", ""},
		{"/internal/showcase/jane", "https://simple-host.app"},
	} {
		r := httptest.NewRequest(http.MethodGet, tc.path, nil)
		if got := h.chromeBase(r); got != tc.base {
			t.Errorf("chromeBase(%s) = %q, want %q", tc.path, got, tc.base)
		}
		page, err := showcasePage(chromeDataFor(r, h.chromeBase(r)), data)
		if err != nil {
			t.Fatal(err)
		}
		body := string(page)
		assertChrome(t, "showcase "+tc.path, body, "")
		if !strings.Contains(body, `href="`+tc.base+`/install.html"`) {
			t.Errorf("showcase %s: chrome links not based on %q", tc.path, tc.base)
		}
		if strings.Contains(body, "__SH_HANDLE__") || strings.Contains(body, "/*__SHOWCASE_DATA__*/") {
			t.Errorf("showcase %s: template placeholder left unsubstituted", tc.path)
		}
		// The owner app's own controls survive under the chrome.
		if !strings.Contains(body, `id="manage-cta"`) {
			t.Errorf("showcase %s: lost its manage-cta control", tc.path)
		}
	}
}

func TestChromeOnRewrittenInstallPage(t *testing.T) {
	// On another instance install.html is served by serveRewrittenAsset, not
	// the file server. It must get the chrome too.
	SetInstanceHosts("hack.example.com", "", "")
	defer SetInstanceHosts("simple-host.app", "", "")
	mux := chromeTestMux(t)
	rec := get(t, mux, "hack.example.com", "/install.html")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	body := rec.Body.String()
	assertChrome(t, "instance install.html", body, "/install.html")
	if strings.Contains(body, "simple-host.app") {
		t.Error("instance install.html still names simple-host.app")
	}
}

func TestChromeOnHackathonHomepage(t *testing.T) {
	// simple-hack.app serves /hackathons as its homepage (nginx proxies / to
	// /hackathons). There "Home" would be this same page and "For hackathons"
	// a second link to it: Home points at the main product, the duplicate goes.
	mux := chromeTestMux(t)
	body := get(t, mux, "simple-hack.app", "/hackathons").Body.String()
	assertChrome(t, "simple-hack.app homepage", body, "")
	if !strings.Contains(body, `<a href="https://simple-host.app/">Simple Host</a>`) {
		t.Error("simple-hack.app: Home does not point at the main product")
	}
	if strings.Contains(body, `href="/hackathons"`) || strings.Contains(body, ">Home<") {
		t.Error("simple-hack.app: still links to itself from the chrome")
	}

	// Anywhere else it is the ordinary page.
	body = get(t, mux, "simple-host.app", "/hackathons").Body.String()
	if !strings.Contains(body, ">Home</a>") || !strings.Contains(body, `href="/hackathons" aria-current="page"`) {
		t.Error("simple-host.app/hackathons lost its ordinary chrome")
	}
	// Other pages on simple-hack.app are not the homepage and keep Home.
	body = get(t, mux, "simple-hack.app", "/install.html").Body.String()
	if !strings.Contains(body, ">Home</a>") {
		t.Error("simple-hack.app/install.html lost Home")
	}
}

func TestChromeFileServerLeavesTheRestAlone(t *testing.T) {
	mux := chromeTestMux(t)
	// The partials are fragments, never pages.
	for _, p := range []string{"/partials/header.html", "/partials/footer.html", "/partials/head.html"} {
		if rec := get(t, mux, "simple-host.app", p); rec.Code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", p, rec.Code)
		}
	}
	// Templates stay handler-only.
	for _, p := range []string{"/showcase.html", "/notfound.html", "/admin.html", "/analytics.html"} {
		if rec := get(t, mux, "simple-host.app", p); rec.Code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", p, rec.Code)
		}
	}
	// /index.html still redirects to /, as the file server always did.
	if rec := get(t, mux, "simple-host.app", "/index.html"); rec.Code != http.StatusMovedPermanently {
		t.Errorf("/index.html: status %d, want 301", rec.Code)
	}
	// The stylesheet is served, as CSS.
	rec := get(t, mux, "simple-host.app", "/site.css")
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/css") {
		t.Errorf("/site.css: status %d, type %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	// A missing page is still the file server's 404.
	if rec := get(t, mux, "simple-host.app", "/nope.html"); rec.Code != http.StatusNotFound {
		t.Errorf("/nope.html: status %d", rec.Code)
	}
}

func TestSetupPageHasNoChrome(t *testing.T) {
	// The first-run wizard on an unconfigured box stays exactly as written.
	raw, err := staticFiles.ReadFile("static/setup.html")
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	serveStaticPage("setup.html").ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Body.String() != string(raw) {
		t.Error("setup.html was altered in serving")
	}
}

func TestEveryPageSourceCarriesTheMarkers(t *testing.T) {
	// A new page that forgets the markers would ship with no header at all, or
	// with a hand-written one. Catch it at test time.
	pages, err := fs.Glob(staticFiles, "static/*.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range pages {
		if p == "static/setup.html" {
			continue
		}
		b, _ := staticFiles.ReadFile(p)
		src := string(b)
		for _, m := range []string{markerHead, markerHeader, markerFooter} {
			if n := strings.Count(src, m); n != 1 {
				t.Errorf("%s: %s appears %d times, want 1", p, m, n)
			}
		}
		for _, banned := range []string{"<header", "<footer", "sh-theme',t", "sh-theme', t", `href="/showcase"`} {
			if strings.Contains(src, banned) {
				t.Errorf("%s: carries its own chrome (%q); use the partials", p, banned)
			}
		}
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
	h := chromeTestHandler()
	mux := http.NewServeMux()
	RegisterUIRoutes(mux, "http://"+addr, h)
	sample := showcaseData{Handle: "jane", SitesBaseURL: "https://sites.simple-host.app", PublicShowcaseURL: "https://sites.simple-host.app/jane", OwnerAppURL: "https://simple-host.app/jane", MainURL: "https://simple-host.app",
		Sites: []showcaseSite{{Name: "recipes", URL: "https://sites.simple-host.app/jane/recipes/", CreatedAt: time.Now(), Visibility: "public"}}}
	mux.HandleFunc("GET /jane", func(w http.ResponseWriter, r *http.Request) {
		page, err := showcasePage(chromeDataFor(r, ""), sample)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(page)
	})
	mux.HandleFunc("GET /_404", func(w http.ResponseWriter, r *http.Request) { h.renderNotFound(w, r, "/nothing-here.html") })
	srv := &http.Server{Addr: addr, Handler: mux}
	go srv.ListenAndServe()
	d, _ := time.ParseDuration(os.Getenv("CHROME_SERVE_FOR"))
	if d == 0 {
		d = 5 * time.Minute
	}
	time.Sleep(d)
	srv.Close()
}
