package web

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// One light/dark setting for the whole site (INTENT, 2026-09-28): every page
// follows the visitor's system until they pick Light or Dark in the header's
// theme menu, and that one choice (localStorage 'sh-theme') applies to every
// page. The only code that reads the system setting or the stored choice is
// partials/theme.html; no page has theme logic or an auto-dark stylesheet of
// its own.

// themeScriptNeedle marks the shared theme script in a served page.
const themeScriptNeedle = "window.shTheme={"

// themeScriptFor renders partials/theme.html as pages receive it.
func themeScriptFor(t *testing.T) string {
	t.Helper()
	var buf bytes.Buffer
	if err := ChromeTemplates.ExecuteTemplate(&buf, "theme.html", ChromeData{}); err != nil {
		t.Fatal(err)
	}
	return strings.TrimRight(buf.String(), "\n")
}

// themeBanned are signs of a page deciding its own theme.
var themeBanned = []string{
	"prefers-color-scheme",
	"matchMedia",
	"themeDefault",
	"data-theme-default",
	"localStorage.setItem('sh-theme'",
	"localStorage.getItem('sh-theme'",
	`localStorage.setItem("sh-theme"`,
	`localStorage.getItem("sh-theme"`,
	"color-scheme: light dark",
	"color-scheme:light dark",
	"light-dark(",
}

// TestNoPageDecidesItsOwnTheme reads every embedded page, script, stylesheet
// and partial, and every Go file here that writes HTML: only theme.html may
// mention the system setting or read the stored choice.
func TestNoPageDecidesItsOwnTheme(t *testing.T) {
	err := fs.WalkDir(StaticFiles, "static", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		switch filepath.Ext(p) {
		case ".html", ".css", ".js":
		default:
			return nil
		}
		if p == "static/partials/theme.html" || p == "static/signin.js" {
			return nil
		}
		b, _ := StaticFiles.ReadFile(p)
		for _, banned := range themeBanned {
			// signin.js (spliced into auth.js at serve time) uses light-dark()
			// for the visitor "plain" skin, following the page's color-scheme.
			if p == "static/auth.js" && banned == "light-dark(" {
				continue
			}
			if bytes.Contains(b, []byte(banned)) {
				t.Errorf("%s contains %q: the theme is decided only by partials/theme.html", p, banned)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	gos := routeGoFiles()
	for _, p := range gos {
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		b, _ := os.ReadFile(p)
		for _, banned := range themeBanned {
			if bytes.Contains(b, []byte(banned)) {
				t.Errorf("%s contains %q: the theme is decided only by partials/theme.html", p, banned)
			}
		}
	}
	// The shared script itself: follows the system with no stored choice,
	// stores only light or dark, and "Match my system" clears the key.
	script := themeScriptFor(t)
	for _, want := range []string{"prefers-color-scheme: dark", "localStorage.getItem(K)", "localStorage.removeItem(K)", "K='sh-theme'", "return 'system'"} {
		if !strings.Contains(script, want) {
			t.Errorf("theme.html lacks %q", want)
		}
	}
}
