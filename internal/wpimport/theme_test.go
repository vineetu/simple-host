package wpimport

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestThemeCopy(t *testing.T) {
	for _, tt := range []struct {
		name string
		fail bool
	}{{"complete", false}, {"asset failure", true}} {
		t.Run(tt.name, func(t *testing.T) {
			c := testConverter()
			c.in.ThemeURL = "https://example.org/"
			pages := map[string]string{
				"https://example.org/":                         `<!doctype html><html><head><link rel="stylesheet" href="/theme/main.css"><link rel="stylesheet" href="https://fonts.googleapis.com/css?family=Test"><style id="global-styles-inline-css">.entry{color:red}</style></head><body class="home page-id-2 wp-custom">Hello</body></html>`,
				"https://example.org/theme/main.css":           `@import "more.css"; .cover{background:url('../assets/paper.png')} .data{background:url(data:image/png;base64,eA==)}`,
				"https://example.org/theme/more.css":           `.more{color:blue}`,
				"https://example.org/assets/paper.png":         "png",
				"https://fonts.googleapis.com/css?family=Test": `@font-face{font-family:Test;src:url(https://fonts.gstatic.com/test.woff2)}`,
				"https://fonts.gstatic.com/test.woff2":         "font",
			}
			c.in.Fetch = func(_ context.Context, u string) ([]byte, error) {
				if tt.fail && strings.Contains(u, "paper.png") {
					return nil, errors.New("offline")
				}
				s, ok := pages[u]
				if !ok {
					return nil, errors.New("unexpected fetch: " + u)
				}
				return []byte(s), nil
			}
			c.copyTheme()
			if c.result.Report.ThemeCopied == tt.fail {
				t.Fatal(c.result.Report)
			}
			if c.bodyClasses != "home page-id-2 wp-custom" {
				t.Error(c.bodyClasses)
			}
			if !tt.fail {
				if len(c.themeLinks) != 3 {
					t.Fatal(c.themeLinks)
				}
				for p, b := range c.result.Files {
					if strings.HasSuffix(p, "css") && (strings.Contains(string(b), "https://fonts.gstatic.com") || strings.Contains(string(b), "../assets/")) {
						t.Error("theme asset not localized", p, string(b))
					}
				}
				c.putPage("/about/", "About", "", "<p>Body</p>", nil)
				h := string(c.result.Files["about/index.html"])
				if !strings.Contains(h, `class="home page-id-2 wp-custom"`) || !strings.Contains(h, "../sh-import/theme/") {
					t.Error(h)
				}
			} else if len(c.result.Files) != 0 || len(c.themeLinks) != 0 || len(c.result.Report.Warnings) == 0 {
				t.Error("failed theme was partially emitted")
			}
		})
	}
}
func TestThemeLimits(t *testing.T) {
	for _, tt := range []struct {
		name        string
		count, size int
	}{{"file count", 41, 10}, {"byte limit", 1, 6 << 20}} {
		t.Run(tt.name, func(t *testing.T) {
			c := testConverter()
			c.in.ThemeURL = "https://example.org/"
			c.in.Fetch = func(_ context.Context, u string) ([]byte, error) {
				if u == c.in.ThemeURL {
					var s strings.Builder
					s.WriteString("<html><head>")
					for n := 0; n < tt.count; n++ {
						s.WriteString(`<link rel="stylesheet" href="/` + jsonNumber(float64(n)) + `.css">`)
					}
					s.WriteString("</head><body></body></html>")
					return []byte(s.String()), nil
				}
				return []byte(strings.Repeat("x", tt.size)), nil
			}
			c.copyTheme()
			if c.result.Report.ThemeCopied || len(c.result.Files) != 0 || len(c.result.Report.Warnings) == 0 {
				t.Error(c.result.Report)
			}
		})
	}
}
