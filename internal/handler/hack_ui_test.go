package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHackUIRoutes(t *testing.T) {
	raw, err := staticFiles.ReadFile("static/hack-app.html")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	for _, m := range []string{markerHead, markerHeader, markerFooter} {
		if n := strings.Count(src, m); n != 1 {
			t.Errorf("hack-app.html: %s appears %d times, want 1", m, n)
		}
	}
	if i, st := strings.Index(src, markerHead), strings.Index(src, "<style"); st >= 0 && i > st {
		t.Error("hack-app.html: <!--sh:head--> must come before the page's own <style>")
	}

	mux := http.NewServeMux()
	RegisterHackUI(mux)
	for _, path := range []string{
		"/signin",
		"/events",
		"/events/new",
		"/e/demo",
		"/e/demo/manage",
		"/join/abcdefgh",
		"/judge/abcdefghjkmn",
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
	}
}
