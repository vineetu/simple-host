package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHackUINext(t *testing.T) {
	previous := hackChrome
	SetHackChrome(true)
	t.Cleanup(func() { SetHackChrome(previous) })
	mux := chromeTestMux(t) // Includes the existing embedded file server.
	RegisterHackUI(mux)
	for _, tc := range []struct {
		name, path, cookie string
		next               bool
		age                int
	}{
		{"default", "/events", "", false, 0},
		{"opt in", "/events?ui=next", "", true, 365 * 24 * 60 * 60},
		{"cookie", "/events", "sh_hack_ui=next", true, 0},
		{"opt out", "/events?ui=classic", "sh_hack_ui=next", false, -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", tc.path, nil)
			req.Header.Set("Cookie", tc.cookie)
			req.Header.Set("X-Forwarded-Proto", "https")
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d", rec.Code)
			}
			if got := strings.Contains(rec.Body.String(), `src="/hack/app.js"`); got != tc.next {
				t.Fatalf("next = %v, want %v", got, tc.next)
			}
			if rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("X-Robots-Tag") != "noindex" || rec.Header().Get("Content-Security-Policy") == "" {
				t.Fatal("missing app headers")
			}
			if tc.age != 0 {
				cookies := rec.Result().Cookies()
				if len(cookies) != 1 {
					t.Fatalf("cookies: %d", len(cookies))
				}
				c := cookies[0]
				if c.Name != "sh_hack_ui" || c.Path != "/" || c.MaxAge != tc.age || !c.Secure || c.SameSite != http.SameSiteLaxMode {
					t.Errorf("incorrect UI cookie attributes")
				}
			}
			if tc.next && strings.Contains(rec.Body.String(), "/signin.js?") {
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
	// Sign-in always uses the shared page (chrome, SSO handoff), even with the
	// new UI chosen; the cookie is kept so people return to the new app.
	rec := get(t, mux, "simple-host.app", "/signin?ui=next")
	body := rec.Body.String()
	if strings.Contains(body, `src="/hack/app.js"`) {
		t.Error("/signin must serve the shared sign-in page, not the new app")
	}
	if !strings.Contains(body, "/signin.js?") || !strings.Contains(body, "window.SHArrival") {
		t.Error("shared sign-in page lacks its sign-in script or arrival helper")
	}
}
