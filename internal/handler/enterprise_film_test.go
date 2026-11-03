package handler

import (
	"net/http"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/internal/edition"
)

// /enterprise opens with its own film in the home page's player: poster
// first, no autoplay, and every cut and poster served with its type.
func TestEnterpriseFilm(t *testing.T) {
	mux := chromeTestMux(t, edition.Host)

	rec := get(t, mux, "simple-host.app", "/enterprise")
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, `<video id="film" playsinline preload="none"`) || !strings.Contains(body, `file: 'enterprise-film'`) {
		t.Fatalf("/enterprise film player missing: %d", rec.Code)
	}
	if strings.Contains(body, "autoplay") {
		t.Error("the film must never autoplay")
	}
	for name, ctype := range enterpriseFilmTypes {
		rec := get(t, mux, "simple-host.app", "/film/"+name+"?v=20261016a")
		if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != ctype || rec.Body.Len() == 0 || rec.Header().Get("ETag") == "" {
			t.Fatalf("%s: %d %q %d", name, rec.Code, rec.Header().Get("Content-Type"), rec.Body.Len())
		}
	}
}
