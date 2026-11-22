package web

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Embedded public copy names simple-host.app, including without runtime rewriting.
// The paused move does not advertise .site even when the old canonical mode is selected.
func TestBaseTextCanonical(t *testing.T) {
	SetInstanceHosts("simple-host.app", "", "", "simple-host.site")
	defer SetInstanceHosts("simple-host.app", "", "")
	b, _ := StaticFiles.ReadFile("static/llms.txt")
	if !bytes.Contains(b, []byte("<site>.<handle>.simple-host.app")) || !bytes.Contains(b, []byte("https://simple-host.app/")) {
		t.Fatal("llms.txt: public addresses must remain on .app")
	}
	// Another install never serves the base, whatever it hands out.
	SetInstanceHosts("hack.example.com", "", "", "simple-host.site")
	b, _ = StaticFiles.ReadFile("static/llms.txt")
	if bytes.Contains(b, []byte("simple-host.site")) || !bytes.Contains(InstanceHosts.Apply(b), []byte("hack.example.com")) {
		t.Fatal("other install")
	}
}

// TestAuthJSFlatHosts: served auth.js decides the flat case first, before
// the person-path heuristic, taking the part of a flat site host's one
// label before the first "--" as the site name for every path, only on a
// base with flat hosts; with none it is byte for byte unpatched.
func TestAuthJSFlatHosts(t *testing.T) {
	raw, _ := EmbeddedTree.ReadFile("static/auth.js")
	if bytes.Count(raw, []byte(authJSOwnHostChain)) != 1 {
		t.Fatal("auth.js no longer has the API-base chain the flat-hosts patch targets (basetext.go)")
	}
	SetFlatHostsText(nil)
	off, _ := StaticFiles.ReadFile("static/auth.js")
	if bytes.Contains(off, []byte(`sub.split("--")[0]`)) {
		t.Fatal("off: auth.js carries the flat-hosts patch")
	}

	bases := []string{"simple-host.app", "simple-host.site"}
	SetFlatHostsText(bases)
	t.Cleanup(func() { SetFlatHostsText(nil) })
	patched, _ := StaticFiles.ReadFile("static/auth.js")
	if !bytes.Contains(patched, []byte(authJSOwnHostChainFlat(bases))) || bytes.Contains(patched, []byte(authJSOwnHostChain)) {
		t.Fatal("flat: auth.js not patched (or the unpatched chain is still present)")
	}

	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "flat.js")
	if err := os.WriteFile(p, patched, 0o644); err != nil {
		t.Fatal(err)
	}
	cases, _ := json.Marshal([][2]string{
		// The bug this patch fixes: visiting a flat team site's root, the
		// unpatched auth.js would call /v1/sites/alpha--weekend/me.
		{"https://alpha--weekend.simple-host.app/", "https://alpha--weekend.simple-host.app/v1/sites/alpha/me"},
		// A sub-path on the flat host must resolve the same way, decided
		// before the person-path heuristic (oneLabel && seg) ever runs —
		// otherwise it would guess "weekend" is a person host and "about"
		// a site name.
		{"https://alpha--weekend.simple-host.app/about/", "https://alpha--weekend.simple-host.app/v1/sites/alpha/me"},
		// A two-level host (no "--") is unaffected.
		{"https://alpha.weekend.simple-host.app/", "https://alpha.weekend.simple-host.app/v1/sites/alpha/me"},
		// A new account's site under the split base.
		{"https://shop--ann.simple-host.site/menu/", "https://shop--ann.simple-host.site/v1/sites/shop/me"},
		// A trailing dot is the same host.
		{"https://shop--ann.simple-host.site./", "https://shop--ann.simple-host.site./v1/sites/shop/me"},
		// A custom domain whose first label has "--" is not a flat host.
		{"https://my--shop.example.com/", "https://my--shop.example.com/v1/sites/my--shop/me"},
	})
	out, err := exec.Command("node", filepath.Join("..", "webassets", "auth-base.test.js"), p, string(cases)).CombinedOutput()
	if err != nil {
		t.Fatalf("patched auth.js:\n%s", out)
	}
}
