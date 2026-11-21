package siteshttp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/web"
)

func TestPhotosPage(t *testing.T) {
	mux := chromeTestMux(t, platformTestEdition())
	r := get(t, mux, "simple-host.app", "/photos?site=home")
	if r.Code != 200 || !strings.Contains(r.Body.String(), "Save to site") || !strings.Contains(r.Body.String(), themeScriptNeedle) || !strings.Contains(r.Body.String(), "/signin.js") {
		t.Fatalf("page: %d", r.Code)
	}
	policy := r.Header().Get("Content-Security-Policy")
	if r.Header().Get("Cache-Control") != "no-store" || !strings.Contains(policy, "worker-src 'self'") || strings.Contains(policy, "wasm-unsafe-eval") || !strings.Contains(policy, "blob:") {
		t.Fatal("page headers")
	}
	t.Setenv("PHOTO_MAX_BYTES", "200000")
	t.Setenv("PHOTO_MAX_DIMENSION", "1024")
	limited := get(t, mux, "simple-host.app", "/photos")
	if !strings.Contains(limited.Body.String(), `data-max-bytes="200000"`) || !strings.Contains(limited.Body.String(), `data-max-dimension="1024"`) {
		t.Fatal("page did not receive server limits")
	}
	if raw := get(t, mux, "simple-host.app", "/photos.html"); raw.Code != 404 {
		t.Fatalf("raw template: %d", raw.Code)
	}
	for _, file := range []string{"libheif.js", "libheif.wasm", "UTIF.js", "pako.min.js", "decoder.js", "wasm.js"} {
		r := get(t, mux, "simple-host.app", "/photo-codecs/v3/"+file)
		if r.Code != 200 || !strings.Contains(r.Header().Get("Cache-Control"), "immutable") {
			t.Fatalf("asset %s: %d", file, r.Code)
		}
	}
	for _, path := range []string{"/photos", "/v1/sites/home/photos"} {
		rec := httptest.NewRecorder()
		web.PhotosHostOnly(false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Fatal("Hack route reached") })).ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != 404 {
			t.Fatalf("Hack: %d", rec.Code)
		}
	}
}
