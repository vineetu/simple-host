package servertest

import (
	"bytes"
	"html/template"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/internal/edition"
	"github.com/vsriram/simple-host/internal/handler"
	"github.com/vsriram/simple-host/internal/webassets"
	"github.com/vsriram/simple-host/products/hack/hackhttp"
)

func TestHackChromeHeader(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	page := []byte("<!--sh:header--><!--sh:footer-->")

	hackAssets := handler.NewPages(edition.ForKind(edition.Hack), "")
	on, err := hackAssets.WithChromeForTest(r, "", page)
	if err != nil {
		t.Fatal(err)
	}
	body := string(on)
	if !strings.Contains(body, `simple<b>·</b>hack`) {
		t.Error("hack chrome: missing simple·hack logo")
	}
	if !strings.Contains(body, `href="/signin"`) {
		t.Error("hack chrome: sign-in does not link /signin")
	}
	if !strings.Contains(body, `data-signed-in="Your events"`) {
		t.Error("hack chrome: sign-in label is not Your events")
	}
	if strings.Contains(body, `<b>-</b><span class="sh-logo-b">host</span>`) {
		t.Error("hack chrome: host logo still present")
	}
	for _, want := range []string{
		`href="https://simple-host.app/"`,
		`href="https://simple-host.app/hackathons"`,
		`href="/privacy.html"`,
		`href="/terms"`,
		`href="/report"`,
		">Self-host an event<",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("hack footer missing %s", want)
		}
	}
	if strings.Contains(body, "With thanks to Jacob Cole") {
		t.Error("hack footer still has the credit line")
	}
	headerPart := body
	if i := strings.Index(body, "<footer"); i >= 0 {
		headerPart = body[:i]
	}
	if strings.Contains(headerPart, "Self-host") {
		t.Error("hack header still has a Self-host link")
	}
	if !strings.Contains(body, `class="sh-header sh-hack"`) {
		t.Error("hack header missing sh-hack class")
	}

	hostAssets := handler.NewPages(edition.ForKind(edition.Host), "")
	off, err := hostAssets.WithChromeForTest(r, "", page)
	if err != nil {
		t.Fatal(err)
	}
	body = string(off)
	if !strings.Contains(body, `<b>-</b><span class="sh-logo-b">host</span>`) {
		t.Error("host chrome: missing simple-host logo")
	}
	if strings.Contains(body, `simple<b>·</b>hack`) {
		t.Error("host chrome: hack logo leaked")
	}
}

func TestHackHomePage(t *testing.T) {
	sh := chromeTestHandler(edition.Hack)
	mux := http.NewServeMux()
	hackhttp.RegisterHackHome(mux, sh.Pages())
	rec := get(t, mux, "simple-hack.app", "/")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"Simple Hack: hackathons, now for everyone.",
		`href="/events/new"`,
		"Want fresh ideas from your people? Run a hackathon. Free.",
		`href="/events"`,
		`id="film"`,
		`id="bigPlay"`,
		`id="againBtn"`,
		`id="playBtn"`,
		`id="seek"`,
		`id="muteBtn"`,
		`id="endCard"`,
		`class="sh-footer"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("hack home missing %q", want)
		}
	}
	if rec.Header().Get("X-Robots-Tag") == "noindex" {
		t.Error("product page should be indexable")
	}
}

func TestNonHackPartialsMatchBaseline(t *testing.T) {
	names := []string{"header.html", "footer.html", "head.html"}
	old := template.New("baseline")
	for _, name := range names {
		out, err := exec.Command("git", "show", "ea2c6ac:internal/handler/static/partials/"+name).Output()
		if err != nil {
			t.Skipf("baseline not available (needs git and commit ea2c6ac): %s: %v", name, err)
		}
		// Shared pages now name the existing favicon, versioned so a browser that
		// cached the pre-rebrand icon fetches the new one. The rest of the chrome
		// stays fixed.
		if name == "head.html" {
			out = append([]byte(`<link rel="icon" href="{{.Base}}/favicon.svg?v=c" type="image/svg+xml">`+"\n"+
				`<link rel="icon" type="image/png" sizes="32x32" href="{{.Base}}/favicon-32.png?v=c">`+"\n"+
				`<link rel="icon" href="{{.Base}}/favicon.ico?v=c" sizes="any">`+"\n"+
				`<link rel="apple-touch-icon" href="{{.Base}}/apple-touch-icon.png?v=c">`+"\n"), out...)
		}
		// The Host logo is two inks (2026-10-10), like Simple Hack's, with a dash
		// rather than a dot so it reads like the domain.
		if name == "header.html" {
			out = bytes.Replace(out, []byte(`">simple<b>·</b>host</a>`), []byte(`"><span class="sh-logo-a">simple</span><b>-</b><span class="sh-logo-b">host</span></a>`), 1)
		}
		// 2026-10-30: "Your sites" links straight to the account's own page when
		// the browser knows its handle (sh-owner-handle), and sign-out forgets it.
		if name == "header.html" {
			renderAt := []byte("      var render = function () {\n")
			out = bytes.Replace(out, renderAt, append([]byte(dashHrefBaselineJS), renderAt...), 1)
			out = bytes.Replace(out, []byte("        dash.textContent = on ? 'Your sites' : 'Sign in';\n"),
				[]byte("        dash.textContent = on ? 'Your sites' : 'Sign in';\n        dash.setAttribute('href', sitesHref(on));\n        if (acctLink) acctLink.setAttribute('href', sitesHref(on));\n"), 1)
			out = bytes.Replace(out, []byte("['apiKey', 'sh-email-nonce']"), []byte("['apiKey', 'sh-email-nonce', 'sh-owner-handle']"), 1)
		}
		// 2026-10-30: the footer gained a Cost and limits link next to Features
		// (SIM-190), a plain nav addition, not a step toward the hack chrome.
		if name == "footer.html" {
			// SIM-389 moves the API footer entry to the product docs.
			out = bytes.ReplaceAll(out, []byte("/docs.html"), []byte("/docs/"))
			out = bytes.ReplaceAll(out, []byte(">API</a>"), []byte(">Docs</a>"))
			before := `<a href="{{.Base}}/features"{{if eq .Current "features"}} aria-current="page"{{end}}>Features</a>` + "\n        "
			after := before + `<a href="{{.Base}}/pricing"{{if eq .Current "pricing"}} aria-current="page"{{end}}>Cost and limits</a>` + "\n        "
			out = bytes.Replace(out, []byte(before), []byte(after), 1)
		}
		if _, err := old.New(name).Parse(string(out)); err != nil {
			t.Fatalf("parse baseline %s: %v", name, err)
		}
	}
	for _, name := range []string{"theme.html", "dialog.html"} {
		b, err := fs.ReadFile(webassets.Static, "static/partials/"+name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := old.New(name).Parse(string(b)); err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
	}
	samples := []handler.ChromeData{
		{},
		{Current: "home"},
		{HackHome: true, Current: "home"},
		{Base: "https://simple-host.app", Current: "enterprise"},
		{Base: "https://simple-host.app", Current: "install"},
		{Current: "report"},
		{Current: "hackathons"},
		{Current: "dashboard"},
		{CSSVersion: "abc"},
	}
	for _, d := range samples {
		for _, name := range names {
			var got, want bytes.Buffer
			if err := handler.ChromeTemplates.ExecuteTemplate(&got, name, d); err != nil {
				t.Fatalf("current %s %+v: %v", name, d, err)
			}
			if err := old.ExecuteTemplate(&want, name, d); err != nil {
				t.Fatalf("baseline %s %+v: %v", name, d, err)
			}
			// The 2026-11-04 hosted-only markers wrap footer links; as the hosted
			// service renders them (markers dropped) the partials are unchanged.
			gotHosted := strings.NewReplacer("<!--sh:hosted-->", "", "<!--/sh:hosted-->", "").Replace(got.String())
			if gotHosted != want.String() {
				t.Errorf("%s mismatch for %+v\n--- got ---\n%s\n--- want ---\n%s", name, d, got.String(), want.String())
			}
		}
	}
	var hackHead bytes.Buffer
	if err := handler.ChromeTemplates.ExecuteTemplate(&hackHead, "head.html", handler.ChromeData{Hack: true}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(hackHead.String(), `<meta name="sh-hack" content="1">`) {
		t.Error("hack head missing sh-hack meta")
	}
}

const dashHrefBaselineJS = `      // "Your sites" goes straight to the account's own page when this browser
      // knows its handle (sh-owner-handle, kept by the owner page and the
      // dashboard after /v1/me), skipping the /dashboard hop.
      var dashHref = dash.getAttribute('href');
      var acctLink = acctMenu.querySelector('a');
      var sitesHref = function (on) {
        var h = '';
        try { h = on ? localStorage.getItem('sh-owner-handle') || '' : ''; } catch (e) {}
        return /^[a-z0-9-]+$/.test(h) ? '{{.Base}}/' + h : dashHref;
      };
`
