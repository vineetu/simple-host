package handler

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func hackHomeTestMux(t *testing.T, base string) *http.ServeMux {
	t.Helper()
	mode, chrome, previousBase := hackMode, hackChrome, hackInkBaseURL
	SetHackMode(true)
	SetHackChrome(true)
	SetHackInstanceURL(base)
	t.Cleanup(func() { SetHackMode(mode); SetHackChrome(chrome); SetHackInstanceURL(previousBase) })
	mux := chromeTestMux(t)
	RegisterHackHome(mux)
	return mux
}

func TestHackVideoHome(t *testing.T) {
	mux := hackHomeTestMux(t, "https://simple-hack.app")
	rec := get(t, mux, "simple-hack.app", "/")
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("home status/cache: %d %q", rec.Code, rec.Header().Get("Cache-Control"))
	}
	body := rec.Body.String()
	for _, want := range []string{
		`<video id="film" playsinline preload="none" disablepictureinpicture></video>`,
		`id="bigPlay"`, `id="playBtn"`, `id="seek"`, `id="muteBtn"`, `id="endCard"`, `id="againBtn"`,
		`Simple Hack in 33 seconds`, `Your hackathon, one link away.`,
		`<span class="ink-red">Hackathons,</span> now for <span class="ink-blue">everyone.</span>`,
		`Want fresh ideas from your people? Run a hackathon. Free.`,
		`href="/events/new">Create event`, `href="/events">Your events`, `href="/watch">Watch how it works`,
		`id="after"`, `href="/directory"`, `id="hostFig"`, `id="footFig"`,
		`/film/hack-film-390-poster.jpg?v=20261013`, `/film/hack-film-1280-poster.jpg?v=20261013`,
		`'/film/hack-film-' + (innerWidth >= 700 ? '1280' : '390') + (mp4 ? '.mp4' : '.webm') + '?v=20261013'`,
		`video.muted = false`, `class="sh-header sh-hack"`, `class="sh-footer"`, `/hack-ink.css?v=`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("home missing %s", want)
		}
	}
	for _, banned := range []string{`sh-film-seen`, `#scene-`, `id="replayBtn"`, `id="skip"`, `rough.js`, `autoplay`, `ontouch`, `wheel`} {
		if strings.Contains(body, banned) {
			t.Errorf("home contains %s", banned)
		}
	}
	for _, tag := range regexp.MustCompile(`(?i)<(?:script|link|img|iframe|source|video)\b[^>]*>`).FindAllString(body, -1) {
		if strings.Contains(tag, `rel="canonical"`) {
			continue
		}
		if regexp.MustCompile(`(?:src|srcset|href)=["'](?:https?:)?//`).MatchString(tag) {
			t.Errorf("external resource: %s", tag)
		}
	}
	assertStrictScriptCSP(t, "Hack video home", rec)
	if strings.Contains(rec.Header().Get("Content-Security-Policy"), "https:") {
		t.Error("CSP permits external resources")
	}
	for _, path := range []string{"/hack-home.html", "/hack-story.html"} {
		if rec := get(t, mux, "simple-hack.app", path); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d", path, rec.Code)
		}
	}
}

func TestHackVideoFilmAssets(t *testing.T) {
	mux := hackHomeTestMux(t, "https://simple-hack.app")
	if len(hackFilmTypes) != 6 {
		t.Fatal("expected six Hack film files")
	}
	for name, ctype := range hackFilmTypes {
		rec := get(t, mux, "simple-hack.app", "/film/"+name+"?v=20261013")
		if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != ctype || rec.Body.Len() == 0 || rec.Header().Get("Cache-Control") != "public, max-age=604800" || rec.Header().Get("ETag") == "" {
			t.Fatalf("%s: status/type/size/cache: %d %q %d %v", name, rec.Code, rec.Header().Get("Content-Type"), rec.Body.Len(), rec.Header())
		}
		if strings.HasSuffix(name, ".mp4") {
			req := httptest.NewRequest(http.MethodGet, "/film/"+name, nil)
			req.Header.Set("Range", "bytes=0-127")
			partial := httptest.NewRecorder()
			mux.ServeHTTP(partial, req)
			if partial.Code != http.StatusPartialContent || partial.Header().Get("Content-Type") != ctype || partial.Body.Len() != 128 || partial.Header().Get("Content-Range") != fmt.Sprintf("bytes 0-127/%d", rec.Body.Len()) {
				t.Fatalf("%s Range: %d %v", name, partial.Code, partial.Header())
			}
		}
	}
}

func TestSelfHostedHackKeepsDrawnHome(t *testing.T) {
	mux := hackHomeTestMux(t, "https://hack.example.org")
	rec := get(t, mux, "hack.example.org", "/")
	body := rec.Body.String()
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("drawn home: %d %v", rec.Code, rec.Header())
	}
	for _, want := range []string{`id="clipSlot"`, `id="replayBtn"`, `sh-film-seen`, `#scene-`, `https://hack.example.org/mcp`} {
		if !strings.Contains(body, want) {
			t.Errorf("drawn home missing %s", want)
		}
	}
	if strings.Contains(body, `/film/hack-film-`) || strings.Contains(body, "simple-hack.app") {
		t.Error("self-hosted home has canonical video or address")
	}
	assertStrictScriptCSP(t, "self-hosted Hack home", rec)
}
