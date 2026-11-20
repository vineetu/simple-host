package servertest

// The Simple Hack cases of the shared page tests. They run against the real
// extension (installAssets), never a copy of its files or callbacks; the
// platform's own tests in host/handler cover the platform install only.

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	productedition "github.com/vsriram/simple-host/products/hack/edition"

	"github.com/vsriram/simple-host/host/access/auth"

	"github.com/vsriram/simple-host/host/handler"
	"github.com/vsriram/simple-host/products/hack/hackhttp"
	hackassets "github.com/vsriram/simple-host/products/hack/webassets"
)

// hackMuxAt is a Hack install at base ("" is simple-hack.app itself).
func hackMuxAt(t *testing.T, base, publicBaseURL string) (*http.ServeMux, *handler.SiteHandler) {
	t.Helper()
	sh := chromeTestHandlerWithBase(productedition.Hack, base)
	mux := http.NewServeMux()
	installAssets(sh, mux)
	handler.RegisterUIRoutes(mux, publicBaseURL, sh)
	return mux, sh
}

func TestHackAuthMarkdownConfiguration(t *testing.T) {
	t.Cleanup(func() {
		handler.SetEmailSignIn(false)
		handler.SetInstanceNote(handler.InstanceFacts{})
	})
	for _, tc := range []struct {
		name, base   string
		email        bool
		providers    []string
		want, absent []string
	}{
		{"hosted Hack", "https://simple-hack.app", true, []string{"google"}, []string{"an emailed code or Google", "https://simple-hack.app/mcp"}, []string{"Email-code sign-in is unavailable"}},
		{"company Hack", "https://hack.company.example", false, []string{"oidc"}, []string{"Acme SSO (company OpenID Connect)", "EMAIL_SIGNIN=off", "https://hack.company.example/mcp"}, []string{"simple-host.app", "simple-hack.app", "an emailed code or Google"}},
		{"company Hack with email", "https://hack.company.example", true, []string{"oidc"}, []string{"an emailed code or Acme SSO", "OIDC_CLIENT_ID"}, []string{"Email-code sign-in is unavailable"}},
		{"Hack no sign-in", "http://localhost:18480", false, nil, []string{"Browser sign-in is unavailable", "Email-code sign-in is unavailable"}, []string{"an emailed code or Google"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler.SetEmailSignIn(tc.email)
			handler.SetInstanceNote(handler.InstanceFacts{SignInProviders: tc.providers, OIDCLabel: "Acme SSO"})
			mux, _ := hackMuxAt(t, tc.base, tc.base)
			rec := get(t, mux, "", "/auth.md")
			if rec.Code != 200 || rec.Header().Get("Content-Type") != "text/markdown; charset=utf-8" {
				t.Fatalf("auth: %d %v", rec.Code, rec.Header())
			}
			body := rec.Body.String()
			for _, want := range tc.want {
				if !strings.Contains(body, want) {
					t.Errorf("missing %q", want)
				}
			}
			for _, absent := range tc.absent {
				if strings.Contains(body, absent) {
					t.Errorf("unexpected %q", absent)
				}
			}
			for _, want := range []string{"hack_get_my_teams", "owner", "deadline", "code_challenge_method=S256", "Never request codes", "/oauth/revoke"} {
				if !strings.Contains(body, want) {
					t.Errorf("missing %q", want)
				}
			}
			if strings.Contains(body, "{sign_in}") {
				t.Error("unfilled template")
			}
		})
	}
}

// The Hack app shell (hack/app.html, mounted through RenderAppShell) carries
// the install's contact in data-support-contact, which shell.js's Help menu
// reads. simple-hack.app keeps the literal default; another install carries
// its own contact.
func TestHackAppShellCarriesInstallContact(t *testing.T) {
	prevContact := auth.SupportContact
	t.Cleanup(func() { auth.SupportContact = prevContact })

	appHTML := hackAppHTML(t)
	if !strings.Contains(string(appHTML), `data-support-contact="support@simple-host.app"`) {
		t.Fatal("hack/app.html lost its default data-support-contact attribute")
	}

	t.Run("hosted", func(t *testing.T) {
		auth.SupportContact = "support@simple-host.app"
		sh := chromeTestHandler(productedition.Hack)
		page, err := sh.Pages().RenderAppShell(httptest.NewRequest(http.MethodGet, "/account", nil), appHTML, false)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(page), `data-support-contact="support@simple-host.app"`) {
			t.Error("hosted app shell must keep the literal default byte-for-byte")
		}
	})

	t.Run("self-hosted", func(t *testing.T) {
		auth.SupportContact = "ops@example.org"
		sh := chromeTestHandlerWithBase(productedition.Hack, "https://hack.example.test")
		page, err := sh.Pages().RenderAppShell(httptest.NewRequest(http.MethodGet, "/account", nil), appHTML, false)
		if err != nil {
			t.Fatal(err)
		}
		body := string(page)
		if strings.Contains(body, "support@simple-host.app") {
			t.Error("self-hosted app shell still names the hosted support address")
		}
		if !strings.Contains(body, `data-support-contact="ops@example.org"`) {
			t.Errorf("self-hosted app shell does not carry the configured contact: %s", body)
		}
	})
}

func hackAppHTML(t *testing.T) []byte {
	t.Helper()
	b, err := fs.ReadFile(chromeTestHandler(productedition.Hack).Pages().StaticFS(), "static/hack/app.html")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func assertNoHostedName(t *testing.T, label, body string) {
	t.Helper()
	lower := strings.ToLower(body)
	for _, name := range []string{"simple-hack.app", "simple-host.app", "vineet sriram"} {
		if strings.Contains(lower, name) {
			t.Errorf("%s: still names %s", label, name)
		}
	}
}

// A self-hosted Hack install names neither hosted service on its pages.
func TestSelfHostedHackNamesNoHostedDomain(t *testing.T) {
	prevContact := auth.SupportContact
	t.Cleanup(func() { auth.SupportContact = prevContact })
	auth.SupportContact = "ops@example.org"
	handler.SetInstanceHosts("hack.example.test", "", "")
	defer handler.SetInstanceHosts("simple-host.app", "", "")

	mux, sh := hackMuxAt(t, "https://hack.example.test", "https://hack.example.test")
	for _, p := range []string{
		"/support", "/privacy.html", "/terms", "/features",
		"/pricing", "/report", "/architecture.html", "/llms.txt",
		"/features.json", "/openapi.json",
	} {
		rec := get(t, mux, "hack.example.test", p)
		if rec.Code != 200 {
			t.Errorf("%s: %d", p, rec.Code)
		}
		assertNoHostedName(t, p, rec.Body.String())
	}
	// hack-get-started.html and connect.html are registered by other
	// handlers; they are rendered through the same page pipeline here.
	for _, name := range []string{"hack-get-started.html", "connect.html"} {
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.Host = "hack.example.test"
		rec := httptest.NewRecorder()
		sh.Pages().ServeStaticPage(name).ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatalf("%s: %d", name, rec.Code)
		}
		assertNoHostedName(t, name, rec.Body.String())
	}
	// The 404 page names the install's own domain, so the handler is built
	// with it (the shared fixture's site domain is the hosted one).
	self, _, _ := handler.NewSiteHandlers(productedition.ForKind(productedition.Hack), "https://hack.example.test", nil, nil, "hack.example.test", "sites.hack.example.test", "", "", "", "", nil, 0, "", nil, nil)
	selfMux := http.NewServeMux()
	installAssets(self, selfMux)
	handler.RegisterUIRoutes(selfMux, "https://hack.example.test", self)
	notFound := get(t, selfMux, "hack.example.test", "/__not_a_handle__")
	if notFound.Code != http.StatusNotFound || !strings.Contains(notFound.Body.String(), ">Go to hack.example.test<") {
		t.Errorf("404 page: %d, label does not name this install", notFound.Code)
	}
	assertNoHostedName(t, "404", notFound.Body.String())

	req := httptest.NewRequest(http.MethodGet, "/account", nil)
	req.Host = "hack.example.test"
	for _, signin := range []bool{false, true} {
		page, err := sh.Pages().RenderAppShell(req, hackAppHTML(t), signin)
		if err != nil {
			t.Fatal(err)
		}
		assertNoHostedName(t, "hack/app.html", string(page))
	}
}

// Simple Hack serves its own icon (the red dot) at the platform's addresses.
func TestHackProductIcons(t *testing.T) {
	mux := chromeTestMux(t, productedition.Hack)
	rec := get(t, mux, "", "/favicon.svg?v=c")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "#d15d48") || !strings.HasPrefix(rec.Header().Get("Content-Type"), "image/svg+xml") {
		t.Fatalf("favicon.svg: %d %q %q", rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "public, max-age=86400" {
		t.Fatalf("favicon.svg Cache-Control: %q", got)
	}
	for _, path := range []string{"/favicon.ico?v=c", "/favicon-32.png?v=c", "/apple-touch-icon.png?v=c"} {
		if rec := get(t, mux, "", path); rec.Code != 200 || rec.Body.Len() < 500 {
			t.Fatalf("%s: %d, %d bytes", path, rec.Code, rec.Body.Len())
		}
	}
}

var ()

// Each Hack chrome page, as served, carries the shared theme script exactly
// once, in <head> before any stylesheet of its own. This is the Hack half of
// host/handler's TestEveryServedPageCarriesTheOneThemeScript; the pages
// left the platform's static folder, so that test no longer reaches them.
func TestHackServedPagesCarryTheOneThemeScript(t *testing.T) {
	var buf bytes.Buffer
	if err := handler.ChromeTemplates.ExecuteTemplate(&buf, "theme.html", handler.ChromeData{}); err != nil {
		t.Fatal(err)
	}
	// Served pages carry a nonce on <script>; compare the script's body.
	script := strings.TrimPrefix(strings.TrimRight(buf.String(), "\n"), "<script>")
	sh := chromeTestHandlerWithBase(productedition.Hack, "https://simple-hack.app")
	mux := http.NewServeMux()
	installAssets(sh, mux)
	handler.RegisterUIRoutes(mux, "https://simple-hack.app", sh)
	hackhttp.RegisterHackHome(mux, sh.Pages(), hackAssetsFor(sh))
	for _, path := range []string{"/", "/companies", "/hackathons", "/get-started", "/watch"} {
		rec := get(t, mux, "simple-hack.app", path)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status %d", path, rec.Code)
			continue
		}
		body := rec.Body.String()
		if n := strings.Count(body, script); n != 1 {
			t.Errorf("%s: shared theme script appears %d times, want 1", path, n)
		}
		if n := strings.Count(body, "prefers-color-scheme"); n != 1 {
			t.Errorf("%s: prefers-color-scheme appears %d times, want 1 (inside the shared script)", path, n)
		}
		i := strings.Index(body, script)
		if h := strings.Index(body, "</head>"); h >= 0 && i > h {
			t.Errorf("%s: theme script is not in <head>", path)
		}
		if st := strings.Index(body, "<style"); st >= 0 && i > st {
			t.Errorf("%s: theme script comes after the page's own <style>", path)
		}
	}
}

func TestHackFeaturesHasNoConnectDomainLink(t *testing.T) {
	body := get(t, chromeTestMux(t, productedition.Hack), "", "/features.html").Body.String()
	if body == "" || strings.Contains(body, `href="/skills/connect-domain/SKILL.md"`) {
		t.Error("Hack features page links the connect-domain skill")
	}
}

// The "run your own" marketing shows on simple-hack.app only.
func TestHackSelfHostMarketingOnlyOnHostedService(t *testing.T) {
	for _, tc := range []struct {
		name, base string
		hosted     bool
	}{
		{"hosted Hack", "https://simple-hack.app", true},
		{"self Hack", "https://hack.example.com", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sh := chromeTestHandlerWithBase(productedition.Hack, tc.base)
			for _, page := range []struct{ name, marketing string }{
				{"hack-home.html", "Prefer your own server?"}, {"hack-home.html", "Running this inside a company?"}, {"hack-story.html", "Prefer your own server?"},
				{"hack-get-started.html", "Can I run my own?"}, {"features.html", `id="self-host"`},
				{"host-story.html", "Self-host on one small server"}, {"hack-companies.html", `href="#install"`},
			} {
				rec := httptest.NewRecorder()
				sh.Pages().ServeStaticPage(page.name).ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
				if rec.Code != 200 {
					t.Fatalf("%s: %d", page.name, rec.Code)
				}
				b := rec.Body.String()
				if strings.Contains(b, page.marketing) != tc.hosted {
					t.Errorf("%s marketing hosted=%v", page.name, tc.hosted)
				}
				if !tc.hosted && (strings.Contains(b, `href="/companies"`) || strings.Contains(b, "Self-host an event")) {
					t.Errorf("%s marketing navigation shown", page.name)
				}
			}
		})
	}
}

// The setup page on a Simple Hack install offers Simple Hack, with its manifest.
func TestHackSetupHelper(t *testing.T) {
	mux := chromeTestMux(t, productedition.Hack)
	page := get(t, mux, "example.test", "/setup")
	for _, want := range []string{
		"<title>Set up Simple Hack</title>",
		"<h1>Set up Simple Hack</h1>",
		`data-setup-product="hack"`,
	} {
		if !strings.Contains(page.Body.String(), want) {
			t.Errorf("/setup missing %q", want)
		}
	}
	rec := get(t, mux, "example.test", "/setup/simple-hack.json")
	if rec.Code != http.StatusOK {
		t.Fatalf("product manifest: %d", rec.Code)
	}
	var steps struct {
		Version string              `json:"version"`
		Steps   []map[string]string `json:"steps"`
		After   []string            `json:"after"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &steps); err != nil {
		t.Fatal(err)
	}
	if len(steps.Steps) != 5 || len(steps.After) != 2 {
		t.Fatalf("want five steps and two after-install lines, got %d and %d", len(steps.Steps), len(steps.After))
	}
	compose, err := os.ReadFile("../../../deploy/hack/standalone/compose.yaml")
	if err != nil {
		t.Fatal(err)
	}
	pin := regexp.MustCompile(`simple-hack:([0-9]+\.[0-9]+\.[0-9]+)`).FindSubmatch(compose)
	if pin == nil || steps.Version != string(pin[1]) {
		t.Errorf("helper version %q differs from compose image pin %q", steps.Version, pin)
	}
}

func TestHackAuthJSStillHasCard(t *testing.T) {
	got, err := fs.ReadFile(chromeTestHandler(productedition.Hack).Pages().StaticFS(), "static/auth.js")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(got, []byte("window.SHSignIn")) {
		t.Fatal("Hack auth.js is missing the sign-in card")
	}
	if !bytes.Contains(got, []byte("Simple Hack visitor sign-in")) {
		t.Fatal("Hack auth.js was not cut by the product rewrite")
	}
	if !bytes.Contains(got, []byte(`var signInBy = "Sign-in by Simple Hack";`)) || bytes.Contains(got, []byte(`var signInBy = "Sign-in by Simple Host";`)) {
		t.Fatal("Hack auth.js kept the Host visitor wording")
	}
	if !bytes.Contains(got, []byte(`var signInProduct = "Simple Hack";`)) {
		t.Fatal("Hack auth.js kept the Host product name")
	}
}

// The shared theme rule (host/handler/theme_test.go) applied to the
// product's own pages, scripts, stylesheets and docs: none decides its own
// theme. The Hack landings stay light through CSS alone.
func TestHackFilesDoNotDecideTheirOwnTheme(t *testing.T) {
	// A copy of themeBanned in host/handler/theme_test.go (unexported,
	// in a test file, so it cannot be imported); keep the two lists equal.
	banned := []string{
		"prefers-color-scheme", "matchMedia", "themeDefault", "data-theme-default",
		"localStorage.setItem('sh-theme'", "localStorage.getItem('sh-theme'",
		`localStorage.setItem("sh-theme"`, `localStorage.getItem("sh-theme"`,
		"color-scheme: light dark", "color-scheme:light dark", "light-dark(",
	}
	seen := 0
	err := fs.WalkDir(hackassets.Static, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		switch filepath.Ext(p) {
		case ".html", ".css", ".js":
		default:
			return nil
		}
		seen++
		b, _ := hackassets.Static.ReadFile(p)
		for _, word := range banned {
			// The approved film checks motion and hover, never OS colour;
			// Starlight queries viewport size and reduced motion.
			if word == "matchMedia" && (p == "static/hack-story.html" || strings.HasPrefix(p, "docs-site/")) {
				continue
			}
			if bytes.Contains(b, []byte(word)) {
				t.Errorf("%s contains %q: the theme is decided only by the shared theme script", p, word)
			}
		}
		return nil
	})
	if err != nil || seen == 0 {
		t.Fatalf("walked %d product files: %v", seen, err)
	}
}

// The product callback keeps the shared enterprise page's event link local.
func TestEnterpriseHackathonsLinkProductCallback(t *testing.T) {
	handler.SetInstanceHosts("simple-host.app", "", "")
	t.Cleanup(func() { handler.SetInstanceHosts("simple-host.app", "", "") })
	for _, base := range []string{"https://simple-hack.app", "https://hack.example.org"} {
		mux, _ := hackMuxAt(t, base, base)
		rec := get(t, mux, "", "/enterprise")
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d", base, rec.Code)
		}
		want := `href="` + base + `/hackathons"`
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("%s: missing %s", base, want)
		}
		if base != "https://simple-hack.app" && strings.Contains(rec.Body.String(), "simple-hack.app/hackathons") {
			t.Error("product link names the hosted service")
		}
	}
}
