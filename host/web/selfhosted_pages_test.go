package web

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/host/config"

	"github.com/vsriram/simple-host/host/edition"
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
		testAssetPipeline(edition.Default(config.DefaultLimits()), "").ServeRewrittenAsset(name, InstanceHosts, time.Time{}).ServeHTTP(rec, httptest.NewRequest("GET", "https://box.example/"+name, nil))
		body := rec.Body.String()
		if rec.Code != 200 {
			t.Fatalf("%s: %d", name, rec.Code)
		}
		for _, w := range []string{"Browse connectors", "Plugins</b>, then <b>Discover", "claude plugin install", "<!--sh:", "app directory and is in review"} {
			if strings.Contains(body, w) {
				t.Errorf("%s on a self-hosted server says %q", name, w)
			}
		}
		if name == "install.html" {
			for _, id := range []string{"lovable", "bolt", "other"} {
				_, rest, found := strings.Cut(body, `<details class="app" id="`+id+`"`)
				card, _, _ := strings.Cut(rest, "</ol>")
				if !found || !strings.Contains(card, "https://box.example/mcp") || strings.Contains(card, "simple-host.app/mcp") {
					t.Errorf("%s must show only this server's MCP address", id)
				}
			}
		}
		if !strings.Contains(body, "https://box.example/mcp") || !strings.Contains(body, "Add custom connector") {
			t.Errorf("%s lacks the custom connector steps for this server", name)
		}
	}
}
