package handler

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"
)

func TestHostedHackStoryHome(t *testing.T) {
	previous := hackChrome
	SetHackChrome(true)
	t.Cleanup(func() { SetHackChrome(previous) })
	mux := http.NewServeMux()
	RegisterHackHome(mux)
	rec := get(t, mux, "simple-hack.app", "/")
	if rec.Code != 200 || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("home status/cache %d %s", rec.Code, rec.Header().Get("Cache-Control"))
	}
	body := rec.Body.String()
	for _, want := range []string{`id="film"`, `id="nextBtn"`, `id="replayBtn"`, `sh-film-seen`, `#scene-`, `href="/events/new"`, `href="/events"`, `href="/directory"`, `href="/get-started"`, `/hack-ink.css?v=`} {
		if !strings.Contains(body, want) {
			t.Errorf("story missing %s", want)
		}
	}
	if n := strings.Count(body, `class="cap" data-i=`); n != 12 {
		t.Errorf("scene count %d", n)
	}
	// Navigation links may go elsewhere; resource loads must stay local.
	for _, tag := range regexp.MustCompile(`(?i)<(?:script|link|img|iframe|source)\b[^>]*>`).FindAllString(body, -1) {
		if strings.Contains(tag, `rel="canonical"`) {
			continue
		}
		if regexp.MustCompile(`(?:src|href)=["'](?:https?:)?//`).MatchString(tag) {
			t.Errorf("external resource: %s", tag)
		}
	}
	if strings.Contains(body, "fonts.googleapis.com") || strings.Contains(body, "fonts.gstatic.com") {
		t.Error("external fonts")
	}
	assertStrictScriptCSP(t, "story", rec)
	if strings.Contains(rec.Header().Get("Content-Security-Policy"), "google") {
		t.Error("hosted CSP permits Google resources")
	}
}

func TestHostPresentationSeparateFromHackStory(t *testing.T) {
	previous := hackChrome
	SetHackChrome(false)
	t.Cleanup(func() { SetHackChrome(previous) })
	mux := chromeTestMux(t)
	for _, path := range []string{"/", "/dashboard"} {
		rec := get(t, mux, "simple-host.app", path)
		if rec.Code != 200 {
			t.Fatalf("%s status %d", path, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "/hack-ink.css") || strings.Contains(rec.Body.String(), "sh-film-seen") {
			t.Fatalf("%s loads Hack presentation", path)
		}
	}
	// Simple Host baseline; index includes the 2026-10-05 file-usage panel.
	for name, want := range map[string]string{
		"index.html": "32f3e7d6a9c960871d0d76786f3a12f2cf23b9337d58eb36a8c56ae0768ca362",
		"site.css":   "754c7f6ab8b78665a092815f1ec82945f68d79cefb096e9987ed4c560a131880",
	} {
		raw, err := staticFiles.ReadFile("static/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if got := fmt.Sprintf("%x", sha256.Sum256(raw)); got != want {
			t.Errorf("Simple Host %s changed: %s", name, got)
		}
	}
}

func TestHackEventColourSurfaces(t *testing.T) {
	a := newTeamSiteApp(t)
	org := a.newPerson(t, "ink-org")
	slug := a.makeEvent(t, org)
	base := "/v1/hack/events/" + slug
	wantTS(t, "colour choice", a.api(t, "PATCH", base, map[string]string{"accent_color": "yellow"}, org.key), 200, "")
	view := a.api(t, "GET", base, nil, org.key).json(t)["event"].(map[string]any)
	if view["accent_color"] != "yellow" || view["color"] != "#f2c94c" {
		t.Fatalf("colour readback %v", view["color"])
	}
	icon := a.api(t, "GET", base+"/icon", nil, "")
	if !strings.Contains(string(icon.body), `fill="#f2c94c"`) {
		t.Fatal("fallback icon differs from event colour")
	}
	wantTS(t, "invalid colour", a.api(t, "PATCH", base, map[string]string{"accent_color": "url(evil)"}, org.key), 400, "invalid_accent_color")
	wantTS(t, "reset colour", a.api(t, "PATCH", base, map[string]string{"accent_color": ""}, org.key), 200, "")
	view = a.api(t, "GET", base, nil, org.key).json(t)["event"].(map[string]any)
	if view["color"] != hackEventColor(slug, "") {
		t.Fatal("automatic colour not stable")
	}
}

// Filename routes receive the same nonce and caching policy as clean app routes.
func TestHostedHackHTMLCache(t *testing.T) {
	previous := hackChrome
	SetHackChrome(true)
	t.Cleanup(func() { SetHackChrome(previous) })
	mux := chromeTestMux(t)
	for _, path := range []string{"/privacy.html", "/terms.html", "/features"} {
		rec := get(t, mux, "simple-host.app", path)
		if rec.Code != 200 || rec.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s: status/cache %d %q", path, rec.Code, rec.Header().Get("Cache-Control"))
		}
		assertStrictScriptCSP(t, path, rec)
	}
	SetHackChrome(false)
	rec := get(t, mux, "simple-host.app", "/privacy.html")
	if rec.Header().Get("Cache-Control") != "" {
		t.Error("Simple Host filename caching changed")
	}
}

// The home is a film; existing email/Google returns must still reach the
// nonce-checking sign-in shell without exposing credentials in page markup.
func TestHackStorySignInReturn(t *testing.T) {
	mux := http.NewServeMux()
	RegisterHackHome(mux)
	for _, query := range []string{"token=one-time&cn=browser-hash", "cn=browser-hash", "token=one-time&next=%2Fevents"} {
		rec := get(t, mux, "simple-hack.app", "/?"+query)
		if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/signin?"+query || rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("sign-in return: %d %q", rec.Code, rec.Header().Get("Location"))
		}
		if strings.Contains(rec.Body.String(), `id="film"`) {
			t.Fatal("sign-in return rendered film")
		}
	}
}

func TestHostFilmAndLegacyDashboardReturn(t *testing.T) {
	previous := hackChrome
	SetHackChrome(false)
	t.Cleanup(func() { SetHackChrome(previous) })
	mux := chromeTestMux(t)
	rec := get(t, mux, "simple-host.app", "/")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `id="film"`) || strings.Count(rec.Body.String(), `class="cap" data-i=`) != 12 {
		t.Fatal("Host root must serve its twelve-scene film")
	}
	assertStrictScriptCSP(t, "host film", rec)
	for _, query := range []string{"token=one-time&cn=browser-hash", "cn=browser-hash", "new=1", "job=build-id"} {
		rec = get(t, mux, "simple-host.app", "/?"+query)
		if rec.Code != 302 || rec.Header().Get("Location") != "/dashboard?"+query || rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("legacy dashboard return %d %q", rec.Code, rec.Header().Get("Location"))
		}
	}
	rec = get(t, mux, "simple-host.app", "/dashboard")
	if strings.Contains(rec.Body.String(), `id="film"`) || !strings.Contains(rec.Body.String(), `id="email-form"`) {
		t.Fatal("dashboard sign-in shell replaced by film")
	}
}
