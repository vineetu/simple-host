package handler

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	db "github.com/vsriram/simple-host/internal/db"
)

// Outside sources send visitors to /?from=<key> (INTENT 2026-10-15): the same
// home page, playing a named film instead of the default one, and a 30-day
// first-party cookie so a later sign-up in that browser counts as its own
// source (cohorts_test.go covers the cookie itself).
// artifact-hub.com and Vercel Ship's post-poll link both play the same
// "everything you make with AI" film.
func TestNamedSourceArrival(t *testing.T) {
	previous := hackChrome
	SetHackChrome(false)
	t.Cleanup(func() { SetHackChrome(previous) })
	mux := chromeTestMux(t)

	for _, source := range []string{"artifact-hub", "vercel-ship"} {
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
		var c *http.Cookie
		for _, k := range rec.Result().Cookies() {
			if k.Name == arrivalCookie {
				c = k
			}
		}
		if c == nil {
			t.Fatalf("no arrival cookie for from=%s", source)
		}
		if v, _ := url.ParseQuery(c.Value); v.Get("f") != source || !c.HttpOnly || c.MaxAge != 30*24*3600 || c.SameSite != http.SameSiteLaxMode {
			t.Fatalf("arrival cookie for from=%s: %+v", source, c)
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
	r.AddCookie(&http.Cookie{Name: arrivalCookie, Value: arrivalValue("something-else")})
	if s := websiteSignup(r, "email"); s.Source != db.SignupFromPrefix+"something-else" {
		t.Fatalf("unnamed arrival value: %q", s.Source)
	}

	for cookieValue, wantSource := range map[string]string{
		"artifact-hub": db.SignupArtifactHub,
		"vercel-ship":  db.SignupVercelShip,
	} {
		r := httptest.NewRequest("POST", "/v1/auth/verify", nil)
		r.AddCookie(&http.Cookie{Name: arrivalCookie, Value: arrivalValue(cookieValue)})
		if s := websiteSignup(r, "google"); s.Source != wantSource || s.Method != "google" {
			t.Fatalf("%s arrival: %+v", cookieValue, s)
		}
	}
}

// arrivalValue is the cookie value the home page sets for from, just now.
func arrivalValue(from string) string {
	return url.Values{"f": {from}, "t": {strconv.FormatInt(time.Now().Unix(), 10)}}.Encode()
}
