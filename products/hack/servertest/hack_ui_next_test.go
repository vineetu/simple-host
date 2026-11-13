package servertest

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/internal/edition"
	"github.com/vsriram/simple-host/internal/handler"
	"github.com/vsriram/simple-host/products/hack/hackhttp"
)

// TestHackUINext (cleanup step 2.2: the classic app is gone) checks that the
// app shell is served everywhere regardless of query string or an old
// sh_hack_ui cookie left over from before the ?ui= switch was deleted, that
// no cookie is ever set, and that the static assets it depends on are still
// reachable.
func TestHackUINext(t *testing.T) {
	sh := chromeTestHandler(edition.Hack)
	mux := http.NewServeMux()
	handler.RegisterUIRoutes(mux, "https://simple-host.app", sh) // Includes the existing embedded file server.
	hackhttp.RegisterHackUI(mux, sh.Pages(), "")
	for _, tc := range []struct{ name, path, cookie string }{
		{"default", "/events", ""},
		{"old classic cookie", "/events", "sh_hack_ui=classic"},
		{"old next cookie", "/events", "sh_hack_ui=next"},
		{"leftover ?ui=classic", "/events?ui=classic", "sh_hack_ui=next"},
		{"leftover ?ui=next", "/events?ui=next", "sh_hack_ui=classic"},
		{"leftover ?ui=new", "/events?ui=new", "sh_hack_ui=classic"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", tc.path, nil)
			if tc.cookie != "" {
				req.Header.Set("Cookie", tc.cookie)
			}
			req.Header.Set("X-Forwarded-Proto", "https")
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d", rec.Code)
			}
			if !strings.Contains(rec.Body.String(), `src="/hack/app.js"`) {
				t.Fatal("the app shell was not served")
			}
			if rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("X-Robots-Tag") != "noindex" || rec.Header().Get("Content-Security-Policy") == "" {
				t.Fatal("missing app headers")
			}
			if cookies := rec.Result().Cookies(); len(cookies) != 0 {
				t.Errorf("set a cookie %v; the UI switch was deleted", cookies)
			}
			if strings.Contains(rec.Body.String(), "/signin.js?") {
				t.Error("sign-in card loaded outside /signin")
			}
		})
	}
	for _, path := range []string{"/hack/app.js", "/hack/kit.css"} {
		rec := get(t, mux, "simple-host.app", path)
		want := "javascript"
		if strings.HasSuffix(path, ".css") {
			want = "text/css"
		}
		if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Content-Type"), want) {
			t.Errorf("%s: status %d type %s", path, rec.Code, rec.Header().Get("Content-Type"))
		}
	}
	// /signin serves the same app shell as every other route now; the
	// sign-in script tag and the arrival helper (statically included in
	// static/hack/app.html, not the chrome's own injector) are both still there.
	rec := get(t, mux, "simple-host.app", "/signin?ui=classic")
	body := rec.Body.String()
	if !strings.Contains(body, `src="/hack/app.js"`) {
		t.Error("/signin must serve the app shell")
	}
	if !strings.Contains(body, "/signin.js?") {
		t.Error("/signin lacks its sign-in script")
	}
	if !strings.Contains(body, `src="/arrival.js"`) {
		t.Error("/signin lacks the arrival helper")
	}
}
