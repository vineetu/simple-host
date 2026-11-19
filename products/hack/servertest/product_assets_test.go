package servertest

import (
	"errors"
	productedition "github.com/vsriram/simple-host/products/hack/edition"
	"io/fs"
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

// Hack's docs root is not a static file name: the file server misses
// /docs-site, as it did before the product's files moved, so the address
// answers 404 and never redirects into the product's folder. (The bare
// fixture has no database for the handle lookup that follows the miss, so
// this checks the lookup the file server uses.)
func TestHackDocsRootIsNotServedAsAFile(t *testing.T) {
	static := chromeTestHandler(productedition.Hack).Pages().StaticFS()
	for _, name := range []string{"static/docs-site", "static/docs-site/index.html"} {
		if _, err := fs.Stat(static, name); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s: static lookup reached the docs root: %v", name, err)
		}
	}
	if _, err := fs.Stat(static, "static/hack-ink.css"); err != nil {
		t.Errorf("a listed product file is no longer found: %v", err)
	}
}
