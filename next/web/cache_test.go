package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// A page's style sheets and scripts are checked with the server every time,
// and an unchanged one costs a 304: a new page never meets an old sheet.
func TestPageCodeIsRevalidated(t *testing.T) {
	for _, name := range []string{"assets/landing/host-ink.css", "assets/landing/site.css", "header.js"} {
		h := func(w http.ResponseWriter, r *http.Request) { _ = serve(w, r, name, "public, max-age=3600") }
		w := httptest.NewRecorder()
		h(w, httptest.NewRequest("GET", "/"+name, nil))
		tag := w.Header().Get("ETag")
		if w.Code != 200 || w.Header().Get("Cache-Control") != "no-cache" || tag == "" {
			t.Fatalf("%s: %d cache %q etag %q", name, w.Code, w.Header().Get("Cache-Control"), tag)
		}
		again := httptest.NewRequest("GET", "/"+name, nil)
		again.Header.Set("If-None-Match", tag)
		w = httptest.NewRecorder()
		h(w, again)
		if w.Code != 304 {
			t.Fatalf("%s: unchanged copy answered %d, want 304", name, w.Code)
		}
	}
	if code("app/index-BztLmlcj.js") || code("assets/film/poster.jpg") {
		t.Fatal("hashed files and pictures keep their long cache")
	}
}
