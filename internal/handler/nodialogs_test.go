package handler

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// nativeDialogCall matches a call to the browser's own confirm, alert or
// prompt: bare, or through window/self/globalThis/top. A method of something
// else (x.prompt(), shConfirm()) does not match.
var nativeDialogCall = regexp.MustCompile(`(?:^|[^A-Za-z0-9_$.])(?:(?:window|self|globalThis|top)\s*\.\s*)?(?:confirm|alert|prompt)\s*\(`)

// nativeDialogAllowed are third-party bundles we serve but do not write.
var nativeDialogAllowed = map[string]bool{
	"static/swagger-ui-bundle.js": true,
}

// TestNoNativeDialogs: no page or script this server hands out calls the
// browser's native confirm, alert or prompt. An AI browser agent driving the
// page cannot see or press those, so the page hangs (an owner's agent could
// not change its handle: the PATCH was never sent). Pages use the in-page
// shConfirm / shPrompt / shAlert from partials/dialog.html instead.
func TestNoNativeDialogs(t *testing.T) {
	check := func(name string, b []byte) {
		for i, line := range bytes.Split(b, []byte("\n")) {
			if m := nativeDialogCall.Find(line); m != nil {
				t.Errorf("%s:%d calls a native dialog (%q); use shConfirm / shPrompt / shAlert", name, i+1, strings.TrimSpace(string(m)))
			}
		}
	}
	err := fs.WalkDir(staticFiles, "static", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		switch filepath.Ext(p) {
		case ".html", ".js", ".htm", ".mjs":
		default:
			return nil
		}
		if nativeDialogAllowed[p] {
			return nil
		}
		b, err := staticFiles.ReadFile(p)
		if err != nil {
			return err
		}
		check(p, b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// Pages built in Go (sign-in results, offline and take-down pages, …).
	gos, _ := filepath.Glob("*.go")
	for _, p := range gos {
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		b, _ := os.ReadFile(p)
		check(p, b)
	}
	// The matcher itself catches every form it is meant to.
	for _, bad := range []string{"if (!confirm('x')) return;", "window.prompt('a', b)", "alert (1)", "x = window . confirm(msg)", "globalThis.alert('x')"} {
		if !nativeDialogCall.MatchString(bad) {
			t.Errorf("matcher misses %q", bad)
		}
	}
	for _, ok := range []string{"await shConfirm({})", "shPrompt({})", "shAlert({})", "form.confirm(x)", "myconfirm(x)"} {
		if nativeDialogCall.MatchString(ok) {
			t.Errorf("matcher flags %q", ok)
		}
	}
}

// TestEveryChromedPageHasTheInPageDialog: the shared head carries the helper,
// so every page with <!--sh:head--> can call shConfirm.
func TestEveryChromedPageHasTheInPageDialog(t *testing.T) {
	var buf bytes.Buffer
	if err := chromeTemplates.ExecuteTemplate(&buf, "head.html", chromeData{}); err != nil {
		t.Fatal(err)
	}
	head := buf.String()
	for _, want := range []string{"window.shConfirm", "window.shPrompt", "window.shAlert", "'aria-modal'", "showModal", "<script>"} {
		if !strings.Contains(head, want) {
			t.Errorf("head partial lacks %q", want)
		}
	}
}
