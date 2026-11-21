package siteshttp

import (
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/host/web"

	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/edition"
)

// chromeTestHandler is a SiteHandler with no database. Every route exercised
// here avoids the DB: handle-shaped paths are the only ones that query it.
func chromeTestHandler(kind *edition.Edition) *SiteHandler {
	return chromeTestHandlerFor(kind)
}

// chromeTestHandlerFor is chromeTestHandler for an explicit edition (e.g.
// edition.ForKindWithLimits with an override).
func chromeTestHandlerFor(ed *edition.Edition) *SiteHandler {
	return withHosts(&SiteHandler{Ed: ed, Assets: testAssetPipeline(ed, ""), Limits: ed.Limits(), SiteDomain: "simple-host.app", ContentHost: "sites.simple-host.app"})
}

func chromeTestMux(t *testing.T, kind *edition.Edition) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	RegisterUIRoutes(mux, "https://simple-host.app", chromeTestHandler(kind))
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

func TestChromeOnEveryMainOriginPage(t *testing.T) {
	prevHosted := web.InstanceIsHostedService
	web.InstanceIsHostedService = true
	t.Cleanup(func() { web.InstanceIsHostedService = prevHosted })
	mux := chromeTestMux(t, platformTestEdition())
	for _, tc := range []struct {
		path, current string
	}{
		// the file server
		{"/", "/"},
		{"/install.html", "/install.html"},
		// Docs chrome is checked with its external scripts in TestDocsRoutes.
		{"/architecture.html", "/architecture.html"},
		{"/privacy.html", "/privacy.html"},
		{"/features.html", "/features"},
		{"/enterprise.html", "/enterprise"},
		// ServeStaticPage
		{"/dashboard", "/dashboard"},
		{"/features", "/features"},
		{"/enterprise", "/enterprise"},
		{"/enterprise/brief", "/enterprise"},
		{"/enterprise/architecture", "/enterprise"},
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
	h := chromeTestHandler(platformTestEdition())

	// The 404 via the nginx fallback, as the content host reaches it.
	req := httptest.NewRequest(http.MethodGet, "/internal/notfound", nil)
	req.Header.Set("X-Original-URI", "/nothing-here.html")
	rec := httptest.NewRecorder()
	h.NotFound(rec, req)
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

	// The showcase route with a reserved handle falls to RenderNotFound
	// without a database lookup.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /internal/showcase/{handle}", h.Showcase)
	rec = get(t, mux, "sites.simple-host.app", "/internal/showcase/sites")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("reserved showcase status %d", rec.Code)
	}
	assertChrome(t, "showcase→notfound", rec.Body.String(), "")

	// The showcase page itself, on both origins.
	data := web.ShowcaseData{Handle: "jane", SitesBaseURL: "https://sites.simple-host.app", MainURL: "https://simple-host.app", Sites: []web.ShowcaseSite{}}
	for _, tc := range []struct{ path, base string }{
		{"/jane", ""},
		{"/internal/showcase/jane", "https://simple-host.app"},
	} {
		r := httptest.NewRequest(http.MethodGet, tc.path, nil)
		if got := h.chromeBase(r); got != tc.base {
			t.Errorf("chromeBase(%s) = %q, want %q", tc.path, got, tc.base)
		}
		page, err := h.Pipeline().ShowcasePage(h.Pipeline().ChromeDataFor(r, h.chromeBase(r)), data)
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
		webmcp := "/webmcp.js?v=1"
		if tc.base != "" {
			webmcp = tc.base + webmcp
		}
		if !strings.Contains(body, `src="`+webmcp+`"`) {
			t.Errorf("showcase %s: webmcp script src, want %s", tc.path, webmcp)
		}
	}
}

// TestNotFoundLabelHostedVsSelfHosted covers gap 3: the previous worker made
// the 404 back-link always say "Go to "+siteDomain, which also changed
// hosted simple-hack.app's label (its siteDomain is "simple-hack.app", not
// "simple-host.app"). The baseline literal "Go to simple-host.app" must
// survive untouched on every hosted instance (cross-brand, on purpose); only
// a genuinely self-hosted install should name itself.
func TestNotFoundLabelHostedVsSelfHosted(t *testing.T) {
	prevHosted := web.InstanceIsHostedService
	t.Cleanup(func() { web.InstanceIsHostedService = prevHosted })

	// Hosted Host edition (simple-host.app).
	web.InstanceIsHostedService = true
	h := chromeTestHandlerFor(edition.Default(config.DefaultLimits()))
	rec := httptest.NewRecorder()
	h.RenderNotFound(rec, httptest.NewRequest(http.MethodGet, "/__not_a_handle__", nil), "/__not_a_handle__")
	if !strings.Contains(rec.Body.String(), ">Go to simple-host.app<") {
		t.Error("hosted Host 404: label is not the baseline literal")
	}

	// Hosted Hack edition (simple-hack.app): same literal, cross-brand.
	hProduct := chromeTestHandlerFor(groupTestEdition())
	hProduct.SiteDomain = "simple-hack.app"
	rec = httptest.NewRecorder()
	hProduct.RenderNotFound(rec, httptest.NewRequest(http.MethodGet, "/__not_a_handle__", nil), "/__not_a_handle__")
	if !strings.Contains(rec.Body.String(), ">Go to simple-host.app<") {
		t.Error("hosted Hack 404: label is not the baseline literal")
	}

	// Self-hosted: names itself, not simple-host.app.
	web.InstanceIsHostedService = false
	hSelf := chromeTestHandlerFor(edition.Default(config.DefaultLimits()))
	hSelf.SiteDomain = "host.example.test"
	rec = httptest.NewRecorder()
	hSelf.RenderNotFound(rec, httptest.NewRequest(http.MethodGet, "/__not_a_handle__", nil), "/__not_a_handle__")
	body := rec.Body.String()
	if !strings.Contains(body, ">Go to host.example.test<") {
		t.Error("self-hosted 404: label does not name this install")
	}
	if strings.Contains(body, "simple-host.app") {
		t.Error("self-hosted 404: label still names simple-host.app")
	}
}

// TestNotFoundLabelFileServerBranch covers the second call site (ui.go's
// static-file-not-found branch, a separate hardcoded duplicate of the same
// label, item 24 of the audit).
func TestNotFoundLabelFileServerBranch(t *testing.T) {
	prevHosted := web.InstanceIsHostedService
	t.Cleanup(func() { web.InstanceIsHostedService = prevHosted })

	web.InstanceIsHostedService = true
	mux := http.NewServeMux()
	RegisterUIRoutes(mux, "https://simple-host.app", chromeTestHandlerFor(edition.Default(config.DefaultLimits())))
	body := get(t, mux, "simple-host.app", "/does.not.exist").Body.String()
	if !strings.Contains(body, ">Go to simple-host.app<") {
		t.Error("hosted file-server 404: label is not the baseline literal")
	}

	web.InstanceIsHostedService = false
	web.SetInstanceHosts("host.example.test", "", "")
	defer web.SetInstanceHosts("simple-host.app", "", "")
	hSelf := chromeTestHandlerFor(edition.Default(config.DefaultLimits()))
	hSelf.SiteDomain = "host.example.test"
	hSelf.ContentHost = "sites.host.example.test"
	mux2 := http.NewServeMux()
	RegisterUIRoutes(mux2, "https://host.example.test", hSelf)
	body2 := get(t, mux2, "host.example.test", "/does.not.exist").Body.String()
	if !strings.Contains(body2, ">Go to host.example.test<") {
		t.Errorf("self-hosted file-server 404: label missing own domain: %s", body2)
	}
	if strings.Contains(body2, "simple-host.app") {
		t.Error("self-hosted file-server 404: label still names simple-host.app")
	}
}

func TestChromeOnRewrittenInstallPage(t *testing.T) {
	// On another instance install.html is served by ServeRewrittenAsset, not
	// the file server. It must get the chrome too.
	web.SetInstanceHosts("hack.example.com", "", "")
	defer web.SetInstanceHosts("simple-host.app", "", "")
	mux := chromeTestMux(t, platformTestEdition())
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

// assertNoHostedDomain fails label if body names either real hosted service,
// case-insensitively (a hosted-domain hit the self-host audit's end-to-end
// script also checks for, scripts/../shots/selfhost-e2e.mjs).
func assertNoHostedDomain(t *testing.T, label, body string) {
	t.Helper()
	lower := strings.ToLower(body)
	if strings.Contains(lower, "simple-hack.app") {
		t.Errorf("%s: still names simple-hack.app:\n%s", label, body)
	}
	if strings.Contains(lower, "simple-host.app") {
		t.Errorf("%s: still names simple-host.app:\n%s", label, body)
	}
	// Redundant with the two checks above (support@ at a hosted domain
	// already contains "simple-host.app"/"simple-hack.app"), kept as an
	// explicit, narrower check so a future change to either domain string
	// does not silently stop covering the support address specifically.
	if strings.Contains(lower, "support@simple-hack.app") || strings.Contains(lower, "support@simple-host.app") {
		t.Errorf("%s: still shows support@ at a hosted domain:\n%s", label, body)
	}
	// "vineet sriram" (ownership attribution), not bare "vineet": the
	// GitHub username in skill/Release links (github.com/vineetu/...) also
	// contains "vineet" and is not an ownership leak.
	if strings.Contains(lower, "vineet sriram") {
		t.Errorf("%s: still names Vineet Sriram:\n%s", label, body)
	}
}

// assertNoSupportOrOwnershipLeak is assertNoHostedDomain's narrower sibling,
// for a page whose content legitimately keeps naming the hosted domain
// (features.json and openapi.json's shared, pre-existing documentation
// text describes cross-product behaviour -- "On simple-hack.app (EVENTS=
// hosted) only a team key deploys" -- factually, regardless of which
// install is serving it; rewriting that prose to be domain-agnostic is a
// separate, much larger undertaking than this review). What must still
// never leak on these two is the operator's own identity: a hosted support
// address, or Vineet Sriram's name.
func assertNoSupportOrOwnershipLeak(t *testing.T, label, body string) {
	t.Helper()
	lower := strings.ToLower(body)
	if strings.Contains(lower, "support@simple-hack.app") || strings.Contains(lower, "support@simple-host.app") {
		t.Errorf("%s: still shows support@ at a hosted domain:\n%s", label, body)
	}
	if strings.Contains(lower, "vineet sriram") {
		t.Errorf("%s: still names Vineet Sriram:\n%s", label, body)
	}
}

// TestSelfHostedNoHostedDomainAcrossPages is finding 5's test extension: a
// self-hosted install -- Hack edition or Host edition -- must not leak
// either real hosted domain on any served chromed page, not just the three
// (support.html, terms.html, privacy.html) this fix directly targeted.
func TestSelfHostedNoHostedDomainAcrossPages(t *testing.T) {
	prevContact := auth.SupportContact
	t.Cleanup(func() { auth.SupportContact = prevContact })
	auth.SupportContact = "ops@example.org"

	t.Run("self-hosted Host", func(t *testing.T) {
		web.SetInstanceHosts("host.example.test", "", "")
		defer web.SetInstanceHosts("simple-host.app", "", "")
		mux := chromeTestMux(t, platformTestEdition())
		for _, p := range []string{
			"/install.html", "/support", "/privacy.html", "/terms",
			"/features", "/pricing", "/report",
			"/architecture.html", "/llms.txt",
		} {
			assertNoHostedDomain(t, p, get(t, mux, "host.example.test", p).Body.String())
		}
		// features.json and openapi.json keep legitimate, pre-existing
		// cross-product documentation text (see
		// assertNoSupportOrOwnershipLeak); only the operator's own identity
		// must not leak from them.
		for _, p := range []string{"/features.json", "/openapi.json"} {
			assertNoSupportOrOwnershipLeak(t, p, get(t, mux, "host.example.test", p).Body.String())
		}
		// The landing page ("/") and an unmatched path both fall through to
		// a database lookup first (showcase.go's OwnerAppOrStatic, treating
		// the path as a possible handle); render the landing page's static
		// content (index.html) and the 404 page directly through the same
		// pipeline instead, the way TestNotFoundLabelHostedVsSelfHosted
		// does for the 404 page.
		sh := chromeTestHandler(platformTestEdition())
		sh.SiteDomain = "host.example.test" // renderNotFound's label names this, not hostedOwner()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Host = "host.example.test"
		rec := httptest.NewRecorder()
		sh.RenderNotFound(rec, httptest.NewRequest(http.MethodGet, "/__not_a_handle__", nil), "/__not_a_handle__")
		assertNoHostedDomain(t, "404", rec.Body.String())

		page, err := sh.Pages().BuildChromePage("index.html", sh.Pages().ChromeDataFor(req, ""))
		if err != nil {
			t.Fatal(err)
		}
		assertNoHostedDomain(t, "index.html (landing)", string(page))
	})
}

// TestSelfHostedSupportAddress covers gap 2: with neither SUPPORT_EMAIL nor
// IDLE_REPLY_TO set, a self-hosted install must not show Vineet's
// support@simple-host.app anywhere on its pages (not even half-Rewritten
// into a nonexistent "support@<this domain>"); with one set, the mailto
// links and plain-text mentions use it instead (existing behaviour).
func TestSelfHostedSupportAddress(t *testing.T) {
	prevContact := auth.SupportContact
	t.Cleanup(func() { auth.SupportContact = prevContact })
	web.SetInstanceHosts("host.example.test", "", "")
	defer web.SetInstanceHosts("simple-host.app", "", "")
	mux := chromeTestMux(t, platformTestEdition())

	t.Run("no setting", func(t *testing.T) {
		auth.SupportContact = "whoever runs this server"
		for _, path := range []string{"/support", "/terms", "/privacy.html", "/features", "/pricing"} {
			body := get(t, mux, "host.example.test", path).Body.String()
			if strings.Contains(strings.ToLower(body), "simple-host.app") {
				t.Errorf("%s: still names simple-host.app", path)
			}
			if strings.Contains(body, "support@") {
				t.Errorf("%s: still shows a support@ address with no setting configured", path)
			}
			if path == "/support" && !strings.Contains(body, "the person who runs this server") {
				t.Errorf("%s: missing the self-hosted contact phrase", path)
			}
		}
	})

	t.Run("SUPPORT_EMAIL set", func(t *testing.T) {
		auth.SupportContact = "ops@example.org"
		body := get(t, mux, "host.example.test", "/support").Body.String()
		if strings.Contains(body, "support@simple-host.app") {
			t.Error("/support: still names the hosted address after SUPPORT_EMAIL is set")
		}
		if !strings.Contains(body, `<a href="mailto:ops@example.org">ops@example.org</a>`) {
			t.Error("/support: mailto link does not use the configured SUPPORT_EMAIL")
		}
	})
}

// TestSupportAddressDefendsAgainstUnsafeCharacters is the regression for the
// finding this covers: SUPPORT_EMAIL is validated strictly at config load
// (limits.go's supportEmailShape rejects an apostrophe or a $ outright,
// TestSupportEmailInvalidIsLoggedAndIgnored in host/config), but
// auth.SupportContact can also come from IDLE_REPLY_TO, which is not
// validated as strictly. This test sets SupportContact directly to values
// that validation would have rejected, to prove chrome.go's own defenses
// hold regardless of how an unsafe value got there: a literal replacement
// (never regexp.ReplaceAll, so a "$1" in the address can't expand as a
// submatch reference) that never runs inside /report's executable <script>
// (an apostrophe there would have broken its string literal).
func TestSupportAddressDefendsAgainstUnsafeCharacters(t *testing.T) {
	prevContact := auth.SupportContact
	t.Cleanup(func() { auth.SupportContact = prevContact })
	web.SetInstanceHosts("host.example.test", "", "")
	defer web.SetInstanceHosts("simple-host.app", "", "")
	mux := chromeTestMux(t, platformTestEdition())

	for name, addr := range map[string]string{
		"apostrophe": "o'connor@example.org",
		"dollar":     "support$1@example.org",
	} {
		t.Run(name, func(t *testing.T) {
			auth.SupportContact = addr
			rec := get(t, mux, "host.example.test", "/report")
			body := rec.Body.String()

			// report.html's own script is the one with the fetch('/report'
			// ...) call; every served script carries a CSP nonce attribute,
			// so a bare "<script>" match would miss it.
			fetchIdx := strings.Index(body, "fetch('/report'")
			scriptStart := strings.LastIndex(body[:fetchIdx], "<script")
			scriptEnd := strings.Index(body[fetchIdx:], "</script>")
			if fetchIdx < 0 || scriptStart < 0 || scriptEnd < 0 {
				t.Fatalf("/report: could not find its inline <script> (status %d):\n%s", rec.Code, body)
			}
			script := body[scriptStart : fetchIdx+scriptEnd]
			if strings.Contains(script, addr) {
				t.Errorf("/report: the configured contact %q was spliced into the script instead of staying out of it:\n%s", addr, script)
			}

			wantAttr := `data-support-contact="` + html.EscapeString(addr) + `"`
			if !strings.Contains(body, wantAttr) {
				t.Errorf("/report: body does not carry %q (script reads the contact from it)", wantAttr)
			}

			escAddr := html.EscapeString(addr)
			wantLink := `<a href="mailto:` + escAddr + `">` + escAddr + `</a>`
			if !strings.Contains(body, wantLink) {
				t.Errorf("/report: mailto link %q not found verbatim (literal replacement must not mangle it)", wantLink)
			}
		})
	}
}

func TestHostNavigationOmitsProductPage(t *testing.T) {
	for _, hosted := range []bool{false, true} {
		t.Run(fmt.Sprint(hosted), func(t *testing.T) {
			previous := web.InstanceIsHostedService
			web.InstanceIsHostedService = hosted
			t.Cleanup(func() { web.InstanceIsHostedService = previous })
			mux := chromeTestMux(t, platformTestEdition())
			body := get(t, mux, "simple-host.app", "/").Body.String()
			if strings.Contains(body, `href="/hackathons"`) {
				t.Error("Host navigation links to hackathons")
			}
		})
	}
}

func TestChromeFileServerLeavesTheRestAlone(t *testing.T) {
	mux := chromeTestMux(t, platformTestEdition())
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
	// A missing page keeps 404 and shares Host's ink chrome.
	if rec := get(t, mux, "simple-host.app", "/nope.html"); rec.Code != http.StatusNotFound {
		t.Errorf("/nope.html: status %d", rec.Code)
	} else {
		assertChrome(t, "missing page", rec.Body.String(), "")
		if !strings.Contains(rec.Body.String(), "/host-ink.css?v=") {
			t.Error("missing page lost Host ink")
		}
	}
}

// The static GET catch-all must not turn absent POST endpoints into 405.
func TestAbsentPostEndpoint(t *testing.T) {
	mux := chromeTestMux(t, platformTestEdition())
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/no-such-endpoint", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("absent endpoint: %d, want 404", rec.Code)
	}
}

// Each product serves its own icon at the same addresses: teal dot for
// Simple Host, red dot for Simple Hack.
func TestProductIconPerMode(t *testing.T) {
	// A product's own icons are tested with the product (products/hack/servertest).
	for _, mode := range []struct {
		product bool
		dot     string
	}{{false, "#157e8a"}} {
		mux := chromeTestMux(t, platformTestEdition())
		rec := get(t, mux, "", "/favicon.svg?v=c")
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), mode.dot) || !strings.HasPrefix(rec.Header().Get("Content-Type"), "image/svg+xml") {
			t.Fatalf("hack=%v favicon.svg: %d %q %q", mode.product, rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
		}
		if got := rec.Header().Get("Cache-Control"); got != "public, max-age=86400" {
			t.Fatalf("hack=%v favicon.svg Cache-Control: %q", mode.product, got)
		}
		for _, path := range []string{"/favicon.ico?v=c", "/favicon-32.png?v=c", "/apple-touch-icon.png?v=c"} {
			if rec := get(t, mux, "", path); rec.Code != 200 || rec.Body.Len() < 500 {
				t.Fatalf("hack=%v %s: %d, %d bytes", mode.product, path, rec.Code, rec.Body.Len())
			}
		}
	}
}

// Every chrome page links the versioned icon addresses, so a browser that
// cached the old pre-rebrand favicon treats the new one as a different URL.
func TestHeadPartialVersionsIconLinks(t *testing.T) {
	mux := chromeTestMux(t, platformTestEdition())
	rec := get(t, mux, "", "/")
	body := rec.Body.String()
	for _, want := range []string{
		`href="/favicon.svg?v=c"`,
		`href="/favicon-32.png?v=c"`,
		`href="/favicon.ico?v=c"`,
		`href="/apple-touch-icon.png?v=c"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("index.html missing %s", want)
		}
	}
}
