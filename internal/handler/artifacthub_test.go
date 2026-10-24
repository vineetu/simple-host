package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	db "github.com/vsriram/simple-host/internal/db"
)

// Named arrivals keep the landing film and carry attribution without cookies.
func TestNamedSourceArrival(t *testing.T) {
	previous := hackChrome
	SetHackChrome(false)
	t.Cleanup(func() { SetHackChrome(previous) })
	mux := chromeTestMux(t)

	for _, source := range []string{"artifact-hub", "vercel-ship", "agentcon"} {
		rec := get(t, mux, "simple-host.app", "/?from="+source)
		body := rec.Body.String()
		if rec.Code != http.StatusOK || !strings.Contains(body, `<video id="film"`) {
			t.Fatalf("home with from=%s: %d", source, rec.Code)
		}
		if strings.Contains(body, "title: 'Artifact Hub") {
			t.Error("the shared film's page title must say Simple Host")
		}
		for _, want := range []string{`'` + source + `': 'everything'`, `file: 'artifact-film'`, `sessionStorage.setItem('sh-film', key)`, `Simple Host | Everything you make with AI, in one place`} {
			if !strings.Contains(body, want) {
				t.Errorf("home page for from=%s missing %s", source, want)
			}
		}
		if len(rec.Result().Cookies()) != 0 {
			t.Fatal("landing wrote a cookie")
		}
		if !strings.Contains(body, "window.SHArrival") {
			t.Fatal("no same-visit navigation helper")
		}

	}
	for _, path := range []string{"/", "/?from=%3Cb%3E"} {
		rec := get(t, mux, "simple-host.app", path)
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

func TestNamedSourceSignupSource(t *testing.T) {
	r := httptest.NewRequest("POST", "/v1/auth/verify", nil)
	if s := websiteSignup(r, "email"); s.Source != db.SignupWebsite {
		t.Fatalf("plain website sign-up: %q", s.Source)
	}
	r = httptest.NewRequest("POST", "/v1/auth/verify?from=something-else", nil)
	if s := websiteSignup(r, "email"); s.Source != db.SignupFromPrefix+"something-else" {
		t.Fatalf("unnamed arrival value: %q", s.Source)
	}

	for fromValue, wantSource := range map[string]string{
		"artifact-hub": db.SignupArtifactHub,
		"vercel-ship":  db.SignupVercelShip,
		"agentcon":     db.SignupAgentCon,
	} {
		r := httptest.NewRequest("POST", "/v1/auth/verify", nil)
		r.URL.RawQuery = "from=" + fromValue
		if s := websiteSignup(r, "google"); s.Source != wantSource || s.Method != "google" {
			t.Fatalf("%s arrival: %+v", fromValue, s)
		}
	}
}
