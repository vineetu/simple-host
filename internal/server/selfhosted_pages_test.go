package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/internal/edition"
	"github.com/vsriram/simple-host/internal/hackhttp"
)

// A self-hosted Simple Hack names its own connector for Claude, never the
// hosted listing.
func TestSelfHostedHackGetStartedClaude(t *testing.T) {
	sh := chromeTestHandlerWithBase(edition.Hack, "https://hack.example")
	mux := http.NewServeMux()
	hackhttp.RegisterHackGetStarted(mux, sh.Pages())
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
