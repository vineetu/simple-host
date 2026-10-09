package handler

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Embedded public copy names simple-host.app, including without runtime rewriting.
func TestBaseTextDefaultIsTodaysText(t *testing.T) {
	SetInstanceHosts("simple-host.app", "", "")
	defer SetInstanceHosts("simple-host.app", "", "")
	err := fs.WalkDir(embeddedStatic, "static", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		raw, _ := embeddedStatic.ReadFile(p)
		got, err := staticFiles.ReadFile(p)
		if err != nil {
			return err
		}
		if bytes.Contains(got, []byte(canonicalBaseDomain)) {
			t.Errorf("%s: serves %s", p, canonicalBaseDomain)
		}
		if !bytes.Equal(got, raw) {
			t.Errorf("%s: served text differs from the embedded file", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// Headers through the file server: as for the embedded file itself.
	sub, _ := fs.Sub(staticFiles, "static")
	raw, _ := fs.Sub(embeddedStatic, "static")
	for _, name := range []string{"/llms.txt", "/openapi.yaml", "/auth.js", "/favicon.svg"} {
		a, b := httptest.NewRecorder(), httptest.NewRecorder()
		http.FileServerFS(sub).ServeHTTP(a, httptest.NewRequest("GET", name, nil))
		http.FileServerFS(raw).ServeHTTP(b, httptest.NewRequest("GET", name, nil))
		a.Header().Del("Content-Length")
		b.Header().Del("Content-Length")
		if a.Code != b.Code || len(a.Header()) != len(b.Header()) || a.Header().Get("Content-Type") != b.Header().Get("Content-Type") ||
			a.Header().Get("Last-Modified") != b.Header().Get("Last-Modified") {
			t.Errorf("%s: %d %v, embedded %d %v", name, a.Code, a.Header(), b.Code, b.Header())
		}
		if name == "/llms.txt" && bytes.Contains(a.Body.Bytes(), []byte(canonicalBaseDomain)) {
			t.Errorf("%s served the base", name)
		}
	}
}

// The paused move does not advertise .site even when the old canonical mode is selected.
func TestBaseTextCanonical(t *testing.T) {
	SetInstanceHosts("simple-host.app", "", "", "simple-host.site")
	defer SetInstanceHosts("simple-host.app", "", "")
	b, _ := staticFiles.ReadFile("static/llms.txt")
	if !bytes.Contains(b, []byte("<site>.<handle>.simple-host.app")) || !bytes.Contains(b, []byte("https://simple-host.app/")) {
		t.Fatal("llms.txt: public addresses must remain on .app")
	}
	if instanceHosts != nil || instanceNote != "" {
		t.Fatal("the hosted service rewrites its own text")
	}
	// Another install never serves the base, whatever it hands out.
	SetInstanceHosts("hack.example.com", "", "", "simple-host.site")
	b, _ = staticFiles.ReadFile("static/llms.txt")
	if bytes.Contains(b, []byte("simple-host.site")) || !bytes.Contains(instanceHosts.apply(b), []byte("hack.example.com")) {
		t.Fatal("other install")
	}
	if got := baseTextString("a <name>.simple-host.site b"); got != "a <name>.simple-host.app b" {
		t.Fatalf("baseTextString: %q", got)
	}
}

// auth.js as written is today's; while addresses move between two domains the
// served copy treats a one-label host under either as a person host.
func TestAuthJSBases(t *testing.T) {
	raw, _ := embeddedStatic.ReadFile("static/auth.js")
	if bytes.Count(raw, []byte(authJSOneLabel)) != 1 {
		t.Fatal("auth.js no longer has the person-host line the moving base patches (basetext.go)")
	}
	SetInstanceHosts("simple-host.app", "", "")
	defer SetInstanceHosts("simple-host.app", "", "")
	defer SetSiteBaseText(nil)
	if got, _ := staticFiles.ReadFile("static/auth.js"); !bytes.Equal(got, raw) {
		t.Fatal("auth.js changed without a moving base")
	}
	SetSiteBaseText([]string{"simple-host.site"})
	if got, _ := staticFiles.ReadFile("static/auth.js"); !bytes.Equal(got, raw) {
		t.Fatal("auth.js changed for one base")
	}
	SetSiteBaseText([]string{"simple-host.site", "simple-host.app"})
	patched, _ := staticFiles.ReadFile("static/auth.js")
	if bytes.Equal(patched, raw) || !bytes.Contains(patched, []byte(`["simple-host.site","simple-host.app"]`)) {
		t.Fatal("auth.js not patched")
	}
	sub, _ := fs.Sub(staticFiles, "static")
	rec := httptest.NewRecorder()
	http.FileServerFS(sub).ServeHTTP(rec, httptest.NewRequest("GET", "/auth.js", nil))
	if !bytes.Equal(rec.Body.Bytes(), patched) {
		t.Fatal("file server serves another auth.js")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	dir := t.TempDir()
	write := func(name string, b []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	cases, _ := json.Marshal([][2]string{
		{"https://olive.simple-host.site/shop/", "https://olive.simple-host.site/v1/sites/shop/me"},
		{"https://olive.simple-host.app/shop/", "https://olive.simple-host.app/v1/sites/shop/me"},
		{"https://shop.olive.simple-host.site/", "https://shop.olive.simple-host.site/v1/sites/shop/me"},
		{"https://clay.simple-host.site/", "https://clay.simple-host.site/v1/sites/clay/me"},
		{"https://www.simple-host.site/x/", "https://www.simple-host.site/v1/sites/www/me"},
		{"https://sites.simple-host.app/olive/shop/", "https://sites.simple-host.app/v1/u/olive/sites/shop/me"},
		{"https://recipes.brand.com/", "https://recipes.brand.com/v1/sites/recipes/me"},
	})
	run := func(file string) (string, error) {
		out, err := exec.Command("node", filepath.Join("..", "..", "web", "auth-base.test.js"), file, string(cases)).CombinedOutput()
		return string(out), err
	}
	if out, err := run(write("patched.js", patched)); err != nil {
		t.Fatalf("patched auth.js:\n%s", out)
	}
	// Unpatched, a person path under the new base would call the wrong API:
	// the reason for the patch.
	if out, err := run(write("raw.js", raw)); err == nil || !strings.Contains(out, "not ok 1 ") {
		t.Fatalf("unpatched auth.js passed the moving-base cases:\n%s", out)
	}
	// Today's cases, as written.
	if out, err := exec.Command("node", filepath.Join("..", "..", "web", "auth-base.test.js")).CombinedOutput(); err != nil {
		t.Fatalf("auth.js as written:\n%s", out)
	}
}
