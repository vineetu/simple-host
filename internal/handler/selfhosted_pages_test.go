package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/internal/edition"
)

// Text only the hosted service can say (Claude's directory, the published
// plugins) is marked on the page; another server gets its own steps.
func TestHostedBlocks(t *testing.T) {
	page := []byte("a<!--sh:hosted-->directory<!--/sh:hosted--><!--sh:self-->custom<!--/sh:self-->b")
	if got := string(hostedBlocks(page, true)); got != "adirectoryb" {
		t.Errorf("hosted: %q", got)
	}
	if got := string(hostedBlocks(page, false)); got != "acustomb" {
		t.Errorf("self-hosted: %q", got)
	}
	if string(page) != "a<!--sh:hosted-->directory<!--/sh:hosted--><!--sh:self-->custom<!--/sh:self-->b" {
		t.Error("hostedBlocks changed its input")
	}
}

func TestSelfHostedGetStartedAndLLMS(t *testing.T) {
	SetInstanceHosts("box.example", "sites.box.example", "")
	defer SetInstanceHosts("simple-host.app", "", "")
	for _, name := range []string{"install.html", "llms.txt"} {
		rec := httptest.NewRecorder()
		newAssetPipeline(edition.ForKind(edition.Host), "").serveRewrittenAsset(name, instanceHosts, time.Time{}).ServeHTTP(rec, httptest.NewRequest("GET", "https://box.example/"+name, nil))
		body := rec.Body.String()
		if rec.Code != 200 {
			t.Fatalf("%s: %d", name, rec.Code)
		}
		for _, w := range []string{"Browse connectors", "Plugins</b>, then <b>Discover", "claude plugin install", "<!--sh:", "app directory and is in review"} {
			if strings.Contains(body, w) {
				t.Errorf("%s on a self-hosted server says %q", name, w)
			}
		}
		if !strings.Contains(body, "https://box.example/mcp") || !strings.Contains(body, "Add custom connector") {
			t.Errorf("%s lacks the custom connector steps for this server", name)
		}
	}
}

func TestSitemapNamesThisServer(t *testing.T) {
	SetInstanceHosts("box.example", "sites.box.example", "")
	defer SetInstanceHosts("simple-host.app", "", "")
	for host, want := range map[string]string{"evil.example": "https://box.example/sitemap.xml", "box.example": "https://box.example/sitemap.xml", "simple-hack.app": "https://simple-hack.app/sitemap.xml"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/robots.txt", nil)
		req.Host = host
		serveRobotsTxt(rec, req, false)
		if !strings.Contains(rec.Body.String(), "Sitemap: "+want) {
			t.Errorf("%s robots.txt: %q", host, rec.Body.String())
		}
		rec = httptest.NewRecorder()
		req = httptest.NewRequest("GET", "/sitemap.xml", nil)
		req.Host = host
		serveSitemapXML(rec, req, false)
		if !strings.Contains(rec.Body.String(), "<loc>"+strings.TrimSuffix(want, "sitemap.xml")+"</loc>") {
			t.Errorf("%s sitemap.xml does not list its own home page", host)
		}
	}
}

// A self-hosted Simple Hack names its own connector for Claude, never the
// hosted listing.
func TestSelfHostedHackGetStartedClaude(t *testing.T) {
	sh := chromeTestHandler(edition.Hack)
	sh.assets = newAssetPipeline(sh.ed, "https://hack.example")
	mux := http.NewServeMux()
	RegisterHackGetStarted(mux, sh)
	r := httptest.NewRecorder()
	mux.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/get-started", nil))
	page := r.Body.String()
	_, rest, _ := strings.Cut(page, `<details class="accordion" id="claude" name="pick-ai">`)
	card, _, _ := strings.Cut(rest, "</details>")
	if !strings.Contains(card, "Add custom connector") || !strings.Contains(card, "https://hack.example/mcp") {
		t.Errorf("self-hosted Hack Claude card lacks its own custom connector: %s", card)
	}
	for _, w := range []string{"Browse connectors", "<!--sh:", "in review in the ChatGPT app directory"} {
		if strings.Contains(page, w) {
			t.Errorf("self-hosted Hack Get started says %q", w)
		}
	}
}
