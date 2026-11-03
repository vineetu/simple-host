package hackhttp

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/internal/edition"
)

// TestHackUIRoutes checks every signed-in route RegisterHackUI serves: the
// new app shell (static/hack/app.html), not the retired classic page. A
// leftover ?ui=classic query parameter or sh_hack_ui cookie from before the
// classic app was deleted must be silently ignored: no switch, no cookie set.
func TestHackUIRoutes(t *testing.T) {
	signinJSTag := regexp.MustCompile(`/signin\.js\?v=[0-9a-f]{10}`)

	mux := http.NewServeMux()
	RegisterHackUI(mux, chromeTestHandler(edition.Hack).Pages(), "")
	for _, path := range []string{
		"/signin",
		"/signin?ui=classic",
		"/account",
		"/events",
		"/events?ui=next",
		"/events/new",
		"/e/demo/team",
		"/e/demo/judge",
		"/e/demo/manage",
		"/e/demo/apply",
		"/join/abcdefgh",
		"/judge/abcdefghjkmn",
		"/organiser/abcdefghjkmnpqrstuvwxyz23456789ab",
		"/team/abcdefgh",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		// A cookie from before the classic app was deleted must have no effect.
		req.Header.Set("Cookie", "sh_hack_ui=classic")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status %d", path, rec.Code)
			continue
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Errorf("%s: Content-Type %q", path, ct)
		}
		if rec.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s: Cache-Control %q", path, rec.Header().Get("Cache-Control"))
		}
		if rec.Header().Get("X-Robots-Tag") != "noindex" {
			t.Errorf("%s: X-Robots-Tag %q", path, rec.Header().Get("X-Robots-Tag"))
		}
		if rec.Header().Get("Content-Security-Policy") == "" {
			t.Errorf("%s: missing CSP", path)
		}
		if len(rec.Result().Cookies()) != 0 {
			t.Errorf("%s: set a cookie %v; the UI switch was deleted", path, rec.Result().Cookies())
		}
		// Unlike the retired classic page, the app shell has no inline
		// script for assertStrictScriptCSP (built for apex pages that do) to
		// check; app.js and arrival.js are both external, non-nonced <script
		// src> tags the CSP's script-src already covers by origin.
		body := rec.Body.String()
		if strings.Contains(body, "<!--sh:") {
			t.Errorf("%s: chrome markers leaked into the served page", path)
		}
		if !strings.Contains(body, `id="app"`) {
			t.Errorf("%s: not the app shell", path)
		}
		if !strings.Contains(body, `src="/hack/app.js"`) {
			t.Errorf("%s: missing the app's own script", path)
		}
		isSignin := strings.HasPrefix(path, "/signin")
		if isSignin && !signinJSTag.MatchString(body) {
			t.Errorf("%s: missing versioned /signin.js", path)
		}
		if !isSignin && strings.Contains(body, "/signin.js?") {
			t.Errorf("%s: sign-in card loaded outside /signin", path)
		}
	}
}

// TestHackVotePageUsesTheKit (Grok re-test cosmetic #2): the public vote page
// used to carry the old handwritten design and the site's own header/footer
// instead of the app kit, the only one of the three public pages
// (event page, directory, vote) that still did. It now matches the other
// two: same top bar and footer, .pub layout, light only, same ids and
// behaviour.
func TestHackVotePageUsesTheKit(t *testing.T) {
	mux := http.NewServeMux()
	RegisterHackUI(mux, chromeTestHandler(edition.Hack).Pages(), "")
	rec := get(t, mux, "simple-hack.app", "/e/demo/vote")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`<link rel="stylesheet" href="/hack/kit.css">`,
		`<link rel="stylesheet" href="/hack/public.css">`,
		`<a class="wordmark" href="/">simple<b>·</b>hack</a>`,
		`<a class="btn quiet" href="/events" id="top-account">Home</a>`,
		`<footer class="pub-foot">`,
		`<span>Made with Simple Hack</span>`,
		`id="vote-panel"`, `id="settings-panel"`, `id="options"`, `id="ranking-list"`,
		`id="enabled"`, `id="opens"`, `id="closes"`, `id="eligibility"`, `id="listed"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("vote page missing %q", want)
		}
	}
	for _, banned := range []string{"sh:header", "sh:footer", "<style>"} {
		if strings.Contains(body, banned) {
			t.Errorf("vote page still carries old chrome/markup: %q", banned)
		}
	}
	assertStrictScriptCSP(t, "vote", rec)

	rec2 := get(t, mux, "simple-hack.app", "/e/demo/voting-settings")
	if rec2.Code != http.StatusOK || !strings.Contains(rec2.Body.String(), `<link rel="stylesheet" href="/hack/kit.css">`) {
		t.Errorf("voting-settings page not on the kit: %d", rec2.Code)
	}
}

// TestHackVotingSettingsHasOwnConfirm (Grok re-test #1): the restyle dropped
// <!--sh:head--> from this page, so window.shConfirm (loaded by that marker
// via partials/dialog.html) is never defined here any more -- narrowing who
// can vote while existing votes would be removed used to throw a
// TypeError and silently fail to save. The page now carries its own small
// scrim/dialog confirm in its own nonce'd script, never shConfirm and never
// a native confirm/alert/prompt (also covered generally by
// TestNoNativeDialogs).
func TestHackVotingSettingsHasOwnConfirm(t *testing.T) {
	mux := http.NewServeMux()
	RegisterHackUI(mux, chromeTestHandler(edition.Hack).Pages(), "")
	rec := get(t, mux, "simple-hack.app", "/e/demo/voting-settings")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "function confirmDialog(") {
		t.Error("voting-settings page lacks its own confirmDialog")
	}
	if !strings.Contains(body, "confirmDialog({") {
		t.Error("the vote-removal save path doesn't call confirmDialog")
	}
	if strings.Contains(body, "shConfirm") {
		t.Error("voting-settings page still references shConfirm, which isn't loaded on this page")
	}
	assertStrictScriptCSP(t, "voting-settings", rec)
}
