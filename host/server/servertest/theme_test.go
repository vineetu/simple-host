package servertest

import (
	"bytes"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/access/accesshttp"
	"github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/host/web"

	"github.com/vsriram/simple-host/host/edition"
)

// themeScriptFor renders partials/theme.html as pages receive it.
func themeScriptFor(t *testing.T) string {
	t.Helper()
	var buf bytes.Buffer
	if err := web.ChromeTemplates.ExecuteTemplate(&buf, "theme.html", web.ChromeData{}); err != nil {
		t.Fatal(err)
	}
	return strings.TrimRight(buf.String(), "\n")
}

// TestEveryServedPageCarriesTheOneThemeScript: each page, as served, has the
// shared script exactly once, in <head> before any stylesheet of its own
// (so the choice is applied before first paint), and every page with the
// header has the one theme menu.
func TestEveryServedPageCarriesTheOneThemeScript(t *testing.T) {
	oldHosted := web.InstanceIsHostedService
	web.InstanceIsHostedService = true
	t.Cleanup(func() { web.InstanceIsHostedService = oldHosted })
	mux := chromeTestMux(t, platformTestEdition())
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
		"/", "/dashboard", "/install.html", "/architecture.html", "/privacy.html",
		"/features", "/enterprise", "/enterprise/brief", "/enterprise/architecture",
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
	pages, _ := fs.Glob(web.StaticFiles, "static/*.html")
	for _, p := range pages {
		name := strings.TrimPrefix(p, "static/")
		page, err := testAssetPipeline(edition.Default(config.DefaultLimits()), "").BuildChromePage(name, web.ChromeData{})
		if err != nil {
			t.Fatal(err)
		}
		check(name, string(page), name != "setup.html")
	}
	// The 404 as the content host reaches it, and the Go-built pages.
	h := chromeTestHandler(platformTestEdition())
	req := httptest.NewRequest(http.MethodGet, "/internal/notfound", nil)
	req.Header.Set("X-Original-URI", "/nothing-here.html")
	rec := httptest.NewRecorder()
	h.NotFound(rec, req)
	check("404", rec.Body.String(), true)
	for label, body := range map[string]string{
		"offline":        web.OfflinePage,
		"taken down":     web.TakedownPage,
		"sign-in fail":   accesshttp.OAuthHTMLFailed,
		"signup blocked": accesshttp.OAuthHTMLSignupBlocked,
		"service error":  web.ServiceErrorPage,
	} {
		check(label, body, false)
	}
}
