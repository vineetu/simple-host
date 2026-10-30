package handler

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func TestHostedHackStoryHome(t *testing.T) {
	previousBase := hackInkBaseURL
	SetHackInstanceURL("https://hack.example.org")
	t.Cleanup(func() { SetHackInstanceURL(previousBase) })
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
	for _, want := range []string{`id="film"`, `id="playBtn"`, `id="restartBtn"`, `id="scrub"`, `id="soundBtn"`, `id="clipSlot"`, `id="replayBtn"`, `sh-film-seen`, `#scene-`, `href="/events/new"`, `href="/events"`, `href="/directory"`, `href="/get-started"`, `/hack-ink.css?v=`} {
		if !strings.Contains(body, want) {
			t.Errorf("story missing %s", want)
		}
	}
	if n := strings.Count(body, `class="cap" data-i=`); n != 8 {
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

func TestHostFilmAssets(t *testing.T) {
	mux := chromeTestMux(t)
	for name, ctype := range filmTypes {
		rec := get(t, mux, "simple-host.app", "/film/"+name+"?v=20261010")
		if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != ctype || rec.Body.Len() < 10000 || rec.Header().Get("Cache-Control") != "public, max-age=604800" || rec.Header().Get("ETag") == "" {
			t.Fatalf("%s status/type/size/cache: %d %q %d %v", name, rec.Code, rec.Header().Get("Content-Type"), rec.Body.Len(), rec.Header())
		}
		req := httptest.NewRequest(http.MethodGet, "/film/"+name, nil)
		req.Header.Set("Range", "bytes=0-1")
		rangeRec := httptest.NewRecorder()
		mux.ServeHTTP(rangeRec, req)
		if rangeRec.Code != http.StatusPartialContent || rangeRec.Header().Get("Content-Type") != ctype || rangeRec.Header().Get("Content-Range") != fmt.Sprintf("bytes 0-1/%d", rec.Body.Len()) {
			t.Fatalf("%s range: %d %v", name, rangeRec.Code, rangeRec.Header())
		}
		req = httptest.NewRequest(http.MethodGet, "/film/"+name, nil)
		req.Header.Set("If-None-Match", rec.Header().Get("ETag"))
		cached := httptest.NewRecorder()
		mux.ServeHTTP(cached, req)
		if cached.Code != http.StatusNotModified {
			t.Fatalf("%s revalidation: %d", name, cached.Code)
		}
	}
	if rec := get(t, mux, "simple-host.app", "/film/other.mp4"); rec.Code != http.StatusNotFound {
		t.Fatalf("unlisted film file: %d", rec.Code)
	}
}

func TestHackFilmNarrationAsset(t *testing.T) {
	mux := chromeTestMux(t)
	rec := get(t, mux, "simple-hack.app", "/hack-film-narration.mp3?v=202610100223")
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "audio/mpeg" || rec.Body.Len() == 0 {
		t.Fatalf("narration status/type/size: %d %q %d", rec.Code, rec.Header().Get("Content-Type"), rec.Body.Len())
	}
	req := httptest.NewRequest(http.MethodGet, "/hack-film-narration.mp3?v=202610100223", nil)
	req.Header.Set("Range", "bytes=0-127")
	rangeRec := httptest.NewRecorder()
	mux.ServeHTTP(rangeRec, req)
	if rangeRec.Code != http.StatusPartialContent || rangeRec.Header().Get("Content-Type") != "audio/mpeg" || rangeRec.Body.Len() != 128 || rangeRec.Header().Get("Content-Range") != fmt.Sprintf("bytes 0-127/%d", rec.Body.Len()) {
		t.Fatalf("narration range: %d %v, %d bytes", rangeRec.Code, rangeRec.Header(), rangeRec.Body.Len())
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
	// Simple Host baseline; index includes the 2026-10-05 file-usage copy, the 2026-10-10 10 MB KV/SQLite allowance, the 2026-10-12 legacy-storage 410 handling, the 2026-10-13 versioned icon links, the 2026-10-13 copy sweep (em-dashes out of user-visible strings), the 2026-10-16 company sign-in (oidc), the 2026-10-20 shared sign-in card on the dashboard, the 2026-10-23 Add photos action, sign-in return, and agent discovery metadata, the 2026-10-24 Rounded icon (?v=c) and same-visit OAuth attribution, and the 2026-10-28 admin ?all=1 scoping on loadSites (SIM-329), and the 2026-10-29 apex SSO bootstrap and sign-out refresh (SIM-338).
	for name, want := range map[string]string{
		"index.html": "e399c537dfae02b203ec52564cc94005fd3496723f76127e5ba2f041559e6ff8",
		"site.css":   "c93e3d24412adc460a901449787f92a56084b9c0be4dbce6332ccbb673e12a8c",
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
	body := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(body, `<video id="film" playsinline preload="none" disablepictureinpicture></video>`) {
		t.Fatal("Host root must serve its click-to-play film")
	}
	// Nothing loads, plays, loops or starts muted on its own: the video has
	// no source until the click, which plays it with sound.
	if strings.Contains(body, "simple-host-film-seen") || strings.Contains(body, `class="cap"`) {
		t.Error("host page keeps the old scene film")
	}
	for _, want := range []string{`'/film/' + FILM.file + '-' + (innerWidth >= 700 ? '1280' : '390') + (mp4 ? '.mp4' : '.webm') + '?v=' + FILM.v`, `host: { file: 'host-film'`, `/film/host-film-390-poster.jpg?v=`, `/film/host-film-1280-poster.jpg?v=`, `id="bigPlay"`, `id="playBtn"`, `id="seek"`, `id="muteBtn"`, `id="endCard"`, `id="againBtn"`, `href="/install.html"`, `href="/dashboard"`, "video.muted = false"} {
		if !strings.Contains(body, want) {
			t.Errorf("host film missing %s", want)
		}
	}
	if strings.Count(body, `class="sh-logo"`) > 1 {
		t.Error("logo shown more than once")
	}
	assertStrictScriptCSP(t, "host film", rec)
	for _, query := range []string{"token=one-time&cn=browser-hash", "cn=browser-hash", "new=1", "job=build-id"} {
		rec = get(t, mux, "simple-host.app", "/?"+query)
		if rec.Code != 302 || rec.Header().Get("Location") != "/dashboard?"+query || rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("legacy dashboard return %d %q", rec.Code, rec.Header().Get("Location"))
		}
	}
	rec = get(t, mux, "simple-host.app", "/dashboard")
	if strings.Contains(rec.Body.String(), `id="film"`) || !strings.Contains(rec.Body.String(), `id="signin-mount"`) {
		t.Fatal("dashboard sign-in shell replaced by film")
	}
}

// Two chapters share one player (2026-10-19): the default "Build a website"
// chapter and the "everything you make with AI" chapter a chapter tap or
// the end card's "Next" button can switch to, in place, without a reload.
func TestHomeTwoFilmChapters(t *testing.T) {
	previous := hackChrome
	SetHackChrome(false)
	t.Cleanup(func() { SetHackChrome(previous) })
	mux := chromeTestMux(t)
	rec := get(t, mux, "simple-host.app", "/")
	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("home status %d", rec.Code)
	}
	for _, want := range []string{
		`role="tablist"`,
		`id="chapterHost" data-film="host" aria-selected="true"`,
		`id="chapterEverything" data-film="everything" aria-selected="false" tabindex="-1"`,
		`id="nextBtn"`, `Next: Everything you make with AI`,
		`chapter: 'Build a website'`, `chapter: 'Everything you make with AI'`,
		`var HASH_FILM = { build: 'host', everything: 'everything' };`,
		`var OTHER_FILM = { host: 'everything', everything: 'host' };`,
		`function applyFilm(key)`, `function () { switchFilm(OTHER_FILM[FILM_KEY]); start(); }`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("home page missing %s", want)
		}
	}
	// The host entry now carries the same fields as the shared film, so
	// switching back to it (a chapter tap, or #film=build) restores the
	// static markup's own defaults exactly.
	for _, want := range []string{
		`title: 'simple-host | Describe a website. It goes online.'`,
		`label: 'Simple Host in 25 seconds'`,
		`alt: 'Your site is ready: a spice pantry shop at pantry.you.simple-host.app, with jars, a cart, and a Sign in button.'`,
		`end: 'Your shop, live in about 10 minutes.'`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("host film entry missing %s", want)
		}
	}
	// The existing end-card content (Get started, Watch again) stays.
	for _, want := range []string{`href="/install.html"`, `id="againBtn"`} {
		if !strings.Contains(body, want) {
			t.Errorf("end card lost %s", want)
		}
	}
}
