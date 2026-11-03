package hackhttp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/internal/handler"
)

func TestVideoEmbed(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"https://www.youtube.com/watch?v=dQw4w9WgXcQ", "https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ"},
		{"https://youtube.com/watch?v=dQw4w9WgXcQ&t=10s", "https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ"},
		{"https://m.youtube.com/watch?v=dQw4w9WgXcQ", "https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ"},
		{"https://youtu.be/dQw4w9WgXcQ?si=abc", "https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ"},
		{"https://www.youtube.com/shorts/dQw4w9WgXcQ", "https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ"},
		{"http://vimeo.com/123456789", "https://player.vimeo.com/video/123456789"},
		{"https://www.loom.com/share/0123456789abcdef0123456789abcdef", "https://www.loom.com/embed/0123456789abcdef0123456789abcdef"},
		{"https://drive.google.com/file/d/1AbCdEfGhIjKlMnOpQrStUvWx/view?usp=sharing", "https://drive.google.com/file/d/1AbCdEfGhIjKlMnOpQrStUvWx/preview"},
		// Hostile or off-table links never embed.
		{"https://youtube.com.evil.test/watch?v=dQw4w9WgXcQ", ""},
		{"https://evil.test/youtube.com/watch?v=dQw4w9WgXcQ", ""},
		{"javascript:alert(1)//youtu.be/dQw4w9WgXcQ", ""},
		{"https://youtu.be/dQw4w9WgXc\"", ""},
		{`https://www.youtube.com/watch?v=dQw4w9WgX"Q`, ""},
		{"https://www.youtube.com/watch?v=dQw4w9WgXcQx", ""},
		{"https://youtu.be/dQw4w9WgXcQ/extra", ""},
		{"https://user@youtu.be/dQw4w9WgXcQ", ""},
		{"https://youtu.be:8443/dQw4w9WgXcQ", ""},
		{"https://drive.google.com/file/d/short/view", ""},
		{"https://drive.google.com/open?id=1AbCdEfGhIjKlMnOpQrStUvWx", ""},
		{"https://vimeo.com/channels/staff/123", ""},
		{"https://www.loom.com/share/NOTHEX0123456789abcdef0123456789", ""},
		{"https://www.dropbox.com/s/abc/demo.mp4", ""},
		{"ftp://youtu.be/dQw4w9WgXcQ", ""},
		{"", ""},
	} {
		if got := videoEmbed(c.in); got != c.want {
			t.Errorf("videoEmbed(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

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
	RegisterHackUI(mux, handler.NewPages(nil, ""), "Simple-Hack.app.")
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
		RegisterHackUI(m, handler.NewPages(nil, ""), base)
		if got := frameSrc(cspOf(m, "/e/demo/judge")); got != "frame-src 'self' blob: https://www.youtube-nocookie.com https://player.vimeo.com https://www.loom.com https://drive.google.com" {
			t.Errorf("base %q: frame-src = %q", base, got)
		}
	}
}
