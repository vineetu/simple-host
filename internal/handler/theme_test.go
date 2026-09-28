package handler

import (
	"bytes"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// One light/dark setting for the whole site (INTENT, 2026-09-28): every page
// follows the visitor's system until they pick Light or Dark in the header's
// theme menu, and that one choice (localStorage 'sh-theme') applies to every
// page. The only code that reads the system setting or the stored choice is
// partials/theme.html; no page has theme logic or an auto-dark stylesheet of
// its own.

// themeScriptNeedle marks the shared theme script in a served page.
const themeScriptNeedle = "window.shTheme={"

// themeScriptFor renders partials/theme.html as pages receive it.
func themeScriptFor(t *testing.T) string {
	t.Helper()
	var buf bytes.Buffer
	if err := chromeTemplates.ExecuteTemplate(&buf, "theme.html", chromeData{}); err != nil {
		t.Fatal(err)
	}
	return strings.TrimRight(buf.String(), "\n")
}

// themeBanned are signs of a page deciding its own theme.
var themeBanned = []string{
	"prefers-color-scheme",
	"matchMedia",
	"themeDefault",
	"data-theme-default",
	"localStorage.setItem('sh-theme'",
	"localStorage.getItem('sh-theme'",
	`localStorage.setItem("sh-theme"`,
	`localStorage.getItem("sh-theme"`,
	"color-scheme: light dark",
	"color-scheme:light dark",
	"light-dark(",
}

// TestNoPageDecidesItsOwnTheme reads every embedded page, script, stylesheet
// and partial, and every Go file here that writes HTML: only theme.html may
// mention the system setting or read the stored choice.
func TestNoPageDecidesItsOwnTheme(t *testing.T) {
	err := fs.WalkDir(staticFiles, "static", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		switch filepath.Ext(p) {
		case ".html", ".css", ".js":
		default:
			return nil
		}
		if p == "static/partials/theme.html" || p == "static/swagger-ui-bundle.js" {
			return nil
		}
		b, _ := staticFiles.ReadFile(p)
		for _, banned := range themeBanned {
			if bytes.Contains(b, []byte(banned)) {
				t.Errorf("%s contains %q: the theme is decided only by partials/theme.html", p, banned)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	gos, _ := filepath.Glob("*.go")
	for _, p := range gos {
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		b, _ := os.ReadFile(p)
		for _, banned := range themeBanned {
			if bytes.Contains(b, []byte(banned)) {
				t.Errorf("%s contains %q: the theme is decided only by partials/theme.html", p, banned)
			}
		}
	}
	// The shared script itself: follows the system with no stored choice,
	// stores only light or dark, and "Match my system" clears the key.
	script := themeScriptFor(t)
	for _, want := range []string{"prefers-color-scheme: dark", "localStorage.getItem(K)", "localStorage.removeItem(K)", "K='sh-theme'", "return 'system'"} {
		if !strings.Contains(script, want) {
			t.Errorf("theme.html lacks %q", want)
		}
	}
}

// TestEveryServedPageCarriesTheOneThemeScript: each page, as served, has the
// shared script exactly once, in <head> before any stylesheet of its own
// (so the choice is applied before first paint), and every page with the
// header has the one theme menu.
func TestEveryServedPageCarriesTheOneThemeScript(t *testing.T) {
	mux := chromeTestMux(t)
	// Served pages under the apex CSP carry a nonce on <script>; compare the
	// script's body.
	script := strings.TrimPrefix(themeScriptFor(t), "<script>")
	check := func(label, body string, chrome bool) {
		t.Helper()
		if n := strings.Count(body, script); n != 1 {
			t.Errorf("%s: shared theme script appears %d times, want 1", label, n)
		}
		if n := strings.Count(body, "prefers-color-scheme"); n != 1 {
			t.Errorf("%s: prefers-color-scheme appears %d times, want 1 (inside the shared script)", label, n)
		}
		if i, h := strings.Index(body, script), strings.Index(body, "</head>"); h >= 0 && i > h {
			t.Errorf("%s: theme script is not in <head>", label)
		}
		if i, st := strings.Index(body, script), strings.Index(body, "<style"); st >= 0 && i > st {
			t.Errorf("%s: theme script comes after the page's own <style>", label)
		}
		if chrome {
			for _, want := range []string{`id="theme-toggle"`, `id="sh-theme-menu"`, `data-sh-theme="system"`, `data-sh-theme="light"`, `data-sh-theme="dark"`} {
				if n := strings.Count(body, want); n != 1 {
					t.Errorf("%s: %q appears %d times, want 1", label, want, n)
				}
			}
		}
	}
	for _, path := range []string{
		"/", "/dashboard", "/install.html", "/docs.html", "/architecture.html", "/privacy.html",
		"/features", "/enterprise", "/enterprise/brief", "/enterprise/architecture", "/hackathons",
		"/setup", "/costs", "/terms", "/support", "/admin", "/analytics/my-site", "/connect.html",
	} {
		rec := get(t, mux, "simple-host.app", path)
		if rec.Code != http.StatusOK {
			// connect.html is handler-only; its page is covered by the source check.
			if path == "/connect.html" {
				continue
			}
			t.Errorf("%s: status %d", path, rec.Code)
			continue
		}
		check(path, rec.Body.String(), true)
	}
	// Every page source with chrome is covered, including ones only a handler
	// serves (connect.html, notfound.html, showcase.html).
	pages, _ := fs.Glob(staticFiles, "static/*.html")
	for _, p := range pages {
		name := strings.TrimPrefix(p, "static/")
		page, err := chromePage(name, chromeData{})
		if err != nil {
			t.Fatal(err)
		}
		check(name, string(page), name != "setup.html")
	}
	// The 404 as the content host reaches it, and the Go-built pages.
	h := chromeTestHandler()
	req := httptest.NewRequest(http.MethodGet, "/internal/notfound", nil)
	req.Header.Set("X-Original-URI", "/nothing-here.html")
	rec := httptest.NewRecorder()
	h.notFound(rec, req)
	check("404", rec.Body.String(), true)
	for label, body := range map[string]string{
		"offline":       offlinePage,
		"taken down":    takedownPage,
		"sign-in fail":  oauthHTMLFailed,
		"service error": serviceErrorPage,
	} {
		check(label, body, false)
	}
}
