package servertest

import (
	productedition "github.com/vsriram/simple-host/products/hack/edition"
	"strings"
	"testing"
)

// The Hack setup script is the platform's with Simple Hack's choice put in:
// no insertion marker is left, and the choice, its two renderers and its
// manifest are there. (products/hack/hackhttp checks each anchor one by one.)
func TestProductSetupScript(t *testing.T) {
	mux := chromeTestMux(t, productedition.Hack)
	r := get(t, mux, "", "/setup/setup.js")
	if r.Code != 200 || r.Header().Get("Content-Security-Policy") == "" {
		t.Fatalf("setup script status/CSP: %d %v", r.Code, r.Header())
	}
	body := r.Body.String()
	if strings.Contains(body, "/*sh:product-") {
		t.Error("an insertion marker is left in the Hack setup script")
	}
	for _, want := range []string{
		"hack: '/setup/simple-hack.json'",
		"function renderHackBasics(", "function renderHackOutput(",
		"if (S.product === 'hack' && S.step === 1) renderHackBasics();",
		"else if (S.product === 'hack' && S.step === 3) renderHackOutput();",
		"else if (want === 'simple-hack') S.product = 'hack';",
		"=== 'hack' ? 'hack' : 'small'",
		"var INSTALLER_RELEASE = ",
	} {
		if strings.Count(body, want) != 1 {
			t.Errorf("Hack setup script: %q appears %d times, want 1", want, strings.Count(body, want))
		}
	}
	host := get(t, chromeTestMux(t, productedition.Host), "", "/setup/setup.js").Body.String()
	if host == "" || len(body) <= len(host) {
		t.Errorf("Hack setup script (%d bytes) is not the platform's (%d bytes) plus the product's choice", len(body), len(host))
	}
}
