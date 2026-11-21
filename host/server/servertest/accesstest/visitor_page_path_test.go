package accesstest

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// visitor_read_site reads the page a browser gets at the same address (the
// path rules are Access's VisitorPageText).
func TestVisitorReadPageAddresses(t *testing.T) {
	a := NewApp(t)
	owner := a.NewPerson(t, "pageowner")
	sid := a.Deploy(t, owner, "shop")
	dir := filepath.Join(a.Disk.SiteDir(owner.ID, "shop"), "current")
	for name, body := range map[string]string{
		"index.html":            "<p>root page</p>",
		"about.html":            "<p>About us</p>",
		"blog.html":             "blog file",
		"blog/post.html":        "post",
		"nested/index.html":     "<p>nested page</p>",
		"guide.html/index.html": "guide folder",
		"notes.txt":             "plain notes",
	} {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	outside := filepath.Join(t.TempDir(), "secret.html")
	if err := os.WriteFile(outside, []byte("outside the site"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "escape.html")); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ page, want string }{
		{"/", "root page"},
		{"index.html", "root page"},
		{"about", "About us"},
		{"about/", "About us"},
		{"about.html", "About us"},
		{"blog", "blog file"},
		{"blog/", "blog file"},
		{"blog/.", "blog file"},
		{"blog/post", "post"},
		{"nested", "nested page"},
		{"nested/", "nested page"},
		{"nested/../about", "About us"},
		{"guide.html", "guide folder"},
		{"guide.html/.", "guide folder"},
		{"notes.txt", "plain notes"},
		{"missing", ""},
		{"blog/missing/", ""},
		{"escape", ""},
		{"escape.html", ""},
	} {
		got, _, err := a.Access.VisitorPageText(httptest.NewRequest("GET", "/mcp", nil), sid, owner.ID, "shop", tc.page)
		if tc.want == "" {
			if err == nil {
				t.Errorf("%s: read %q, want page not found", tc.page, got)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("%s: %q %v, want %q", tc.page, got, err, tc.want)
		}
	}
}
