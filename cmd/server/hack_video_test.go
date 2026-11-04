package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/internal/hackhttp"
	"github.com/vsriram/simple-host/internal/handler"
)

// TestHackAppCSP: only the judge route widens frame-src, to exactly the four
// video players and one team-sites wildcard; every other app route keeps
// adminUICSP's.
func TestHackAppCSP(t *testing.T) {
	cspOf := func(mux *http.ServeMux, path string) string {
		t.Helper()
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d", path, rec.Code)
		}
		return rec.Header().Get("Content-Security-Policy")
	}
	frameSrc := func(csp string) string {
		for _, d := range strings.Split(csp, ";") {
			if d = strings.TrimSpace(d); strings.HasPrefix(d, "frame-src ") {
				return d
			}
		}
		return ""
	}
	mux := http.NewServeMux()
	hackhttp.RegisterHackUI(mux, handler.NewPages(nil, ""), "Simple-Hack.app.")
	want := "frame-src 'self' blob: https://*.simple-hack.app https://www.youtube-nocookie.com https://player.vimeo.com https://www.loom.com https://drive.google.com"
	judge := cspOf(mux, "/e/demo/judge")
	if got := frameSrc(judge); got != want {
		t.Fatalf("judge frame-src = %q, want %q", got, want)
	}
	if !strings.Contains(judge, "frame-ancestors 'none'") || !strings.Contains(judge, "script-src 'self' 'nonce-") || strings.Contains(judge, "unsafe-eval") {
		t.Fatalf("judge CSP lost the rest of the admin policy: %q", judge)
	}
	for _, p := range []string{"/events", "/e/demo/team", "/e/demo/manage", "/account", "/e/demo/vote"} {
		if got := frameSrc(cspOf(mux, p)); got != "frame-src 'self' blob:" {
			t.Errorf("%s frame-src = %q", p, got)
		}
	}
	// No per-site hosts, or a base that is not a plain DNS name: no wildcard.
	for _, base := range []string{"", "localhost", "a b.test", "x.test; script-src *", "*.test"} {
		m := http.NewServeMux()
		hackhttp.RegisterHackUI(m, handler.NewPages(nil, ""), base)
		if got := frameSrc(cspOf(m, "/e/demo/judge")); got != "frame-src 'self' blob: https://www.youtube-nocookie.com https://player.vimeo.com https://www.loom.com https://drive.google.com" {
			t.Errorf("base %q: frame-src = %q", base, got)
		}
	}
}
