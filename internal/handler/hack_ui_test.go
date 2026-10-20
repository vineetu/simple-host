package handler

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func TestHackUIRoutes(t *testing.T) {
	raw, err := staticFiles.ReadFile("static/hack-app.html")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	for _, m := range []string{markerHead, markerHeader, markerFooter, markerSigninJS} {
		if n := strings.Count(src, m); n != 1 {
			t.Errorf("hack-app.html: %s appears %d times, want 1", m, n)
		}
	}
	if i, st := strings.Index(src, markerHead), strings.Index(src, "<style"); st >= 0 && i > st {
		t.Error("hack-app.html: <!--sh:head--> must come before the page's own <style>")
	}
	signinJSTag := regexp.MustCompile(`/signin\.js\?v=[0-9a-f]{10}`)

	mux := http.NewServeMux()
	RegisterHackUI(mux)
	prevChrome := hackChrome
	SetHackChrome(true)
	t.Cleanup(func() { SetHackChrome(prevChrome) })
	for _, path := range []string{
		"/signin",
		"/account",
		"/events",
		"/events/new",
		"/e/demo/team",
		"/e/demo/judge",
		"/e/demo/manage",
		"/join/abcdefgh",
		"/judge/abcdefghjkmn",
		"/organiser/abcdefghjkmnpqrstuvwxyz23456789ab",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status %d", path, rec.Code)
			continue
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Errorf("%s: Content-Type %q", path, ct)
		}
		if rec.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s: Cache-Control %q", path, rec.Header().Get("Cache-Control"))
		}
		if rec.Header().Get("X-Robots-Tag") != "noindex" {
			t.Errorf("%s: X-Robots-Tag %q", path, rec.Header().Get("X-Robots-Tag"))
		}
		if rec.Header().Get("Content-Security-Policy") == "" {
			t.Errorf("%s: missing CSP", path)
		}
		assertStrictScriptCSP(t, path, rec)
		body := rec.Body.String()
		// The hack-mode header carries an extra "sh-hack" class
		// (class="sh-header sh-hack"), so match the class token's
		// opening quote only, not its exact closing quote.
		if !strings.Contains(body, `class="sh-header`) || !strings.Contains(body, `class="sh-footer"`) {
			t.Errorf("%s: missing chrome", path)
		}
		if strings.Contains(body, "<!--sh:") {
			t.Errorf("%s: chrome markers leaked into the served page", path)
		}
		if !strings.Contains(body, `id="hack-app"`) {
			t.Errorf("%s: not the hack app page", path)
		}
		if !signinJSTag.MatchString(body) {
			t.Errorf("%s: missing versioned /signin.js", path)
		}
		if strings.Contains(body, `id="email-input"`) || strings.Contains(body, `id="code-input"`) || strings.Contains(body, `id="auth-btn"`) {
			t.Errorf("%s: old hack sign-in controls are still in the page", path)
		}
		if !strings.Contains(body, "This link does not work any more") {
			t.Errorf("%s: missing dead-link heading", path)
		}
		if !strings.Contains(body, `id="acct-email-input"`) {
			t.Errorf("%s: account email change was removed", path)
		}
		if !strings.Contains(body, `href="/account"`) {
			t.Errorf("%s: missing Account link", path)
		}
		if path == "/account" && !strings.Contains(body, `id="view-account"`) {
			t.Errorf("%s: missing account view", path)
		}
	}
}
