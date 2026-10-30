package handler

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func TestHackUIRoutes(t *testing.T) {
	raw, err := staticFiles.ReadFile("static/hack-app.html")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	for _, m := range []string{markerHead, markerHeader, markerFooter, markerSignin} {
		if n := strings.Count(src, m); n != 1 {
			t.Errorf("hack-app.html: %s appears %d times, want 1", m, n)
		}
	}
	if i, st := strings.Index(src, markerHead), strings.Index(src, "<style"); st >= 0 && i > st {
		t.Error("hack-app.html: <!--sh:head--> must come before the page's own <style>")
	}
	signinJSTag := regexp.MustCompile(`/signin\.js\?v=[0-9a-f]{10}`)

	mux := http.NewServeMux()
	RegisterHackUI(mux)
	prevChrome := hackChrome
	SetHackChrome(true)
	t.Cleanup(func() { SetHackChrome(prevChrome) })
	for _, path := range []string{
		"/signin",
		"/account",
		"/events",
		"/events/new",
		"/e/demo/team",
		"/e/demo/judge",
		"/e/demo/manage",
		"/join/abcdefgh",
		"/judge/abcdefghjkmn",
		"/organiser/abcdefghjkmnpqrstuvwxyz23456789ab",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		// The new app (static/hack/) is the default and has its own test in
		// hack_ui_next_test.go; this one checks the classic page, still served
		// to people who chose it.
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
		assertStrictScriptCSP(t, path, rec)
		body := rec.Body.String()
		// The hack-mode header carries an extra "sh-hack" class
		// (class="sh-header sh-hack"), so match the class token's
		// opening quote only, not its exact closing quote.
		if !strings.Contains(body, `class="sh-header`) || !strings.Contains(body, `class="sh-footer"`) {
			t.Errorf("%s: missing chrome", path)
		}
		if strings.Contains(body, "<!--sh:") {
			t.Errorf("%s: chrome markers leaked into the served page", path)
		}
		if !strings.Contains(body, `id="hack-app"`) {
			t.Errorf("%s: not the hack app page", path)
		}
		if !signinJSTag.MatchString(body) {
			t.Errorf("%s: missing versioned /signin.js", path)
		}
		if strings.Contains(body, `id="email-input"`) || strings.Contains(body, `id="code-input"`) || strings.Contains(body, `id="auth-btn"`) {
			t.Errorf("%s: old hack sign-in controls are still in the page", path)
		}
		if !strings.Contains(body, "This link does not work any more") {
			t.Errorf("%s: missing dead-link heading", path)
		}
		if !strings.Contains(body, `id="acct-email-input"`) {
			t.Errorf("%s: account email change was removed", path)
		}
		if !strings.Contains(body, `href="/account"`) {
			t.Errorf("%s: missing Account link", path)
		}
		if path == "/account" && !strings.Contains(body, `id="view-account"`) {
			t.Errorf("%s: missing account view", path)
		}
		// BROKEN #1 (Grok re-test): classic had no visible way back to the
		// new screens (the account page and the judge/organiser Help areas
		// are where someone stuck on ?ui=classic would look).
		for _, id := range []string{"judge-new-ui", "manage-new-ui", "acct-new-ui"} {
			if !strings.Contains(body, `id="`+id+`"`) {
				t.Errorf("%s: missing %q link back to the new screens", path, id)
			}
		}
		if !strings.Contains(body, `>Use the new screens</a>`) {
			t.Errorf("%s: missing visible 'Use the new screens' link text", path)
		}
		if !strings.Contains(body, `href="?ui=next"`) {
			t.Errorf("%s: 'Use the new screens' link does not point at ?ui=next", path)
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
	RegisterHackUI(mux)
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
	RegisterHackUI(mux)
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
