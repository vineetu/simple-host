package handler

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func TestHackWatchPage(t *testing.T) {
	mode, chrome, base := hackMode, hackChrome, hackInkBaseURL
	SetHackMode(true)
	SetHackChrome(true)
	SetHackInstanceURL("https://simple-hack.app")
	t.Cleanup(func() { SetHackMode(mode); SetHackChrome(chrome); SetHackInstanceURL(base) })
	mux := chromeTestMux(t)
	RegisterHackHome(mux)
	rec := get(t, mux, "simple-hack.app", "/watch")
	body := rec.Body.String()
	if rec.Code != 200 || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("watch status/cache: %d %v", rec.Code, rec.Header())
	}
	for _, want := range []string{`Simple Hack: watch how it works`, `id="organizer-stage"`, `id="judge-stage"`, `id="participant-stage"`, `class="sh-header sh-hack"`, `class="sh-footer"`, `credentials:'omit'`, `href="/demo/judge"`, `href="/demo/join"`, `href="/get-started"`} {
		if !strings.Contains(body, want) {
			t.Errorf("watch missing %s", want)
		}
	}
	if n := strings.Count(body, `class="cap" data-i=`); n != 18 {
		t.Fatalf("watch captions: %d, want 18", n)
	}
	for _, tag := range regexp.MustCompile(`(?i)<(?:script|link|img|iframe|source)\b[^>]*>`).FindAllString(body, -1) {
		if regexp.MustCompile(`(?:src|href)=["'](?:https?:)?//`).MatchString(tag) {
			t.Errorf("external resource: %s", tag)
		}
	}
	assertStrictScriptCSP(t, "watch", rec)
	if strings.Contains(rec.Header().Get("Content-Security-Policy"), "google") {
		t.Error("watch CSP permits Google resources")
	}
	if hidden := get(t, mux, "simple-hack.app", "/hack-watch.html"); hidden.Code != 404 {
		t.Fatalf("filename route: %d", hidden.Code)
	}
	SetHackInstanceURL("https://hack.college.test")
	rewritten := get(t, mux, "hack.college.test", "/watch").Body.String()
	for _, want := range []string{"https://hack.college.test/mcp", "campushack.hack.college.test", "team.campushack.hack.college.test"} {
		if !strings.Contains(rewritten, want) {
			t.Errorf("instance rewrite missing %s", want)
		}
	}
	if strings.Contains(rewritten, "simple-hack.app") {
		t.Error("instance watch page still names canonical Hack host")
	}
}

func TestHackWatchNarration(t *testing.T) {
	mux := chromeTestMux(t)
	for _, role := range []string{"organizer", "judge", "participant"} {
		path := "/hack-watch-" + role + ".mp3?v=202610102300"
		rec := get(t, mux, "simple-hack.app", path)
		if rec.Code != 200 || rec.Header().Get("Content-Type") != "audio/mpeg" || rec.Body.Len() == 0 {
			t.Fatalf("%s status/type/size: %d %q %d", role, rec.Code, rec.Header().Get("Content-Type"), rec.Body.Len())
		}
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Range", "bytes=0-127")
		part := httptest.NewRecorder()
		mux.ServeHTTP(part, req)
		if part.Code != 206 || part.Header().Get("Content-Type") != "audio/mpeg" || part.Body.Len() != 128 || part.Header().Get("Content-Range") != fmt.Sprintf("bytes 0-127/%d", rec.Body.Len()) {
			t.Fatalf("%s Range: %d %v", role, part.Code, part.Header())
		}
	}
}

func TestHackWatchHostedOnly(t *testing.T) {
	a := newHackApp(t)
	sh := chromeTestHandler()
	sh.database = a.database
	mux := http.NewServeMux()
	RegisterUIRoutes(mux, "https://simple-host.app", sh)
	for _, path := range []string{"/watch", "/hack-watch.html", "/v1/hack/demo", "/demo/join", "/demo/judge"} {
		if rec := get(t, mux, "simple-host.app", path); rec.Code != 404 {
			t.Errorf("unregistered hosted route %s: %d", path, rec.Code)
		}
	}
}

func TestHackDemo(t *testing.T) {
	a := newHackApp(t)
	p := a.newPerson(t, "watch-organizer")
	// The demo lookup counts only events organised by the admin account.
	if _, err := a.database.Exec(`UPDATE users SET is_admin = true WHERE username = $1`, p.email); err != nil {
		t.Fatal(err)
	}
	slug := uniqueSlug()
	a.hack.RegisterDemo(a.mux, slug)
	request := func(path string) *httptest.ResponseRecorder {
		t.Helper()
		rec := httptest.NewRecorder()
		a.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s: cache %q", path, rec.Header().Get("Cache-Control"))
		}
		return rec
	}
	missing := func() {
		t.Helper()
		rec := request("/v1/hack/demo")
		if rec.Code != 404 || strings.TrimSpace(rec.Body.String()) != `{"error":"no demo event"}` {
			t.Fatalf("missing demo: %d %s", rec.Code, rec.Body.String())
		}
		for _, path := range []string{"/demo/join", "/demo/judge"} {
			if rec := request(path); rec.Code != 404 || !strings.Contains(rec.Body.String(), "Ask your organizer") {
				t.Errorf("missing %s: %d %s", path, rec.Code, rec.Body.String())
			}
		}
	}
	missing()
	created := a.createEvent(t, p, slug, map[string]any{"title": "Simple Hack demo"})
	if created.status != 201 {
		t.Fatalf("create demo: %d %s", created.status, created.body)
	}
	missing() // Draft links are unavailable.
	a.openEvent(t, p, slug)
	ev := created.json(t)["organiser"].(map[string]any)
	check := func() {
		t.Helper()
		rec := request("/v1/hack/demo")
		if rec.Code != 200 {
			t.Fatalf("open demo: %d %s", rec.Code, rec.Body.String())
		}
		for _, want := range []string{`"title":"Simple Hack demo"`, `"event_url":"https://` + slug + `.simple-hack.test/"`, `"join_url":"` + ev["join_url"].(string) + `"`, `"judge_url":"` + ev["judge_url"].(string) + `"`} {
			if !strings.Contains(rec.Body.String(), want) {
				t.Errorf("demo missing %s", want)
			}
		}
		for _, kind := range []string{"join", "judge"} {
			rec := request("/demo/" + kind)
			if rec.Code != 302 || rec.Header().Get("Location") != ev[kind+"_url"] {
				t.Errorf("%s redirect: %d %q", kind, rec.Code, rec.Header().Get("Location"))
			}
		}
	}
	check()
	for _, stage := range []string{"building", "closed", "judging", "results", "draft"} {
		r := a.at(t, "POST", "/v1/hack/events/"+slug+"/stage", map[string]string{"stage": stage}, a.key(p))
		if r.status != 200 {
			t.Fatalf("stage %s: %d %s", stage, r.status, r.body)
		}
		if stage == "building" {
			check()
		} else {
			missing()
		}
	}
	a.openEvent(t, p, slug)
	if _, err := a.database.Exec(`UPDATE events SET taken_down_at = now() WHERE slug = $1`, slug); err != nil {
		t.Fatal(err)
	}
	missing()
	// Used names stay reserved, so the weekly reset moves to a dated successor. Someone else's
	// demo-* event never counts; the admin's newest one does.
	other := a.newPerson(t, "watch-stranger")
	decoy := slug + "-202642"
	if r := a.createEvent(t, other, decoy, map[string]any{"title": "Not the demo"}); r.status != 201 {
		t.Fatalf("create decoy: %d %s", r.status, r.body)
	}
	a.openEvent(t, other, decoy)
	missing()
	successor := slug + "-202643"
	created = a.createEvent(t, p, successor, map[string]any{"title": "Simple Hack demo"})
	if created.status != 201 {
		t.Fatalf("create successor: %d %s", created.status, created.body)
	}
	a.openEvent(t, p, successor)
	ev = created.json(t)["organiser"].(map[string]any)
	rec := request("/v1/hack/demo")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"event_url":"https://`+successor+`.simple-hack.test/"`) || !strings.Contains(rec.Body.String(), `"judge_url":"`+ev["judge_url"].(string)+`"`) {
		t.Fatalf("successor demo: %d %s", rec.Code, rec.Body.String())
	}
}
