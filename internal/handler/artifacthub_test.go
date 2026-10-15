package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	db "github.com/vsriram/simple-host/internal/db"
)

// artifact-hub.com sends visitors to /?from=artifact-hub (INTENT 2026-10-15):
// the same home page, playing the Artifact Hub film, and a session cookie so
// a sign-up later in the visit counts as its own source.
func TestArtifactHubArrival(t *testing.T) {
	previous := hackChrome
	SetHackChrome(false)
	t.Cleanup(func() { SetHackChrome(previous) })
	mux := chromeTestMux(t)

	rec := get(t, mux, "simple-host.app", "/?from=artifact-hub")
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, `<video id="film"`) {
		t.Fatalf("home with from=artifact-hub: %d", rec.Code)
	}
	for _, want := range []string{`'artifact-hub': {`, `file: 'artifact-film'`, `sessionStorage.setItem('sh-film', key)`, `Artifact Hub | Everything you make with AI, in one place`} {
		if !strings.Contains(body, want) {
			t.Errorf("home page missing %s", want)
		}
	}
	var c *http.Cookie
	for _, k := range rec.Result().Cookies() {
		if k.Name == arrivalCookie {
			c = k
		}
	}
	if c == nil || c.Value != arrivalArtifactHub || !c.HttpOnly || c.MaxAge != 0 || c.Expires.Year() > 1 || c.SameSite != http.SameSiteLaxMode {
		t.Fatalf("arrival cookie: %+v", c)
	}
	for _, path := range []string{"/", "/?from=elsewhere"} {
		rec = get(t, mux, "simple-host.app", path)
		if len(rec.Result().Cookies()) != 0 {
			t.Errorf("%s set a cookie", path)
		}
	}

	for name, ctype := range artifactFilmTypes {
		rec := get(t, mux, "simple-host.app", "/film/"+name+"?v=20261015")
		if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != ctype || rec.Body.Len() == 0 || rec.Header().Get("ETag") == "" {
			t.Fatalf("%s: %d %q %d", name, rec.Code, rec.Header().Get("Content-Type"), rec.Body.Len())
		}
	}
}

func TestArtifactHubSignupSource(t *testing.T) {
	r := httptest.NewRequest("POST", "/v1/auth/verify", nil)
	if s := websiteSignup(r, "email"); s.Source != db.SignupWebsite {
		t.Fatalf("plain website sign-up: %q", s.Source)
	}
	r.AddCookie(&http.Cookie{Name: arrivalCookie, Value: "something-else"})
	if s := websiteSignup(r, "email"); s.Source != db.SignupWebsite {
		t.Fatalf("unknown arrival value: %q", s.Source)
	}
	r = httptest.NewRequest("POST", "/v1/auth/verify", nil)
	r.AddCookie(&http.Cookie{Name: arrivalCookie, Value: arrivalArtifactHub})
	if s := websiteSignup(r, "google"); s.Source != db.SignupArtifactHub || s.Method != "google" {
		t.Fatalf("artifact-hub arrival: %+v", s)
	}
}
