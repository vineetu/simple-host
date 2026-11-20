package handler

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/host/mcp"
)

// visitor_read_site reads the page a browser gets at the same address.
func TestVisitorReadPageAddresses(t *testing.T) {
	f := newVisitorFixture(t)
	dir := filepath.Join(f.a.sites.disk.SiteDir(f.ownerID, "shop"), "current")
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
		args := map[string]any{"address": "https://" + f.host, "page": tc.page}
		if tc.want == "" {
			f.call(t, "visitor_read_site", args, "page not found")
			continue
		}
		out := f.call(t, "visitor_read_site", args, "")["visitor_data"].(map[string]any)
		if out["page_text"] != tc.want {
			t.Errorf("%s: %v, want %q", tc.page, out["page_text"], tc.want)
		}
	}
}

// A passcode change that lands after the tool checked the passcode, and
// before it opened the page, must still keep the page from being read.
func TestVisitorPageReadDuringPasscodeChange(t *testing.T) {
	f := newVisitorFixture(t)
	h := f.a.sites
	if err := h.SetPasscodeKey(testPasscodeKey); err != nil {
		t.Fatal(err)
	}
	withPasscodeLimits(t, func(l *config.Limits) { l.SitePasscodes = true }, &h.limitSet, &h.passcode.limitSet)
	changed, err := h.passcode.seal(f.sid, "654321")
	if err != nil {
		t.Fatal(err)
	}
	args := map[string]any{"address": "https://" + f.host, "page": "index.html", "passcode": "123456"}
	for _, wait := range []time.Duration{200 * time.Millisecond, 2 * time.Second, 10 * time.Second} {
		if r := f.a.at(t, "PUT", pcSiteDomain, "/v1/sites/shop/lock", map[string]any{"passcode": "123456"}, f.key); r.status != 200 {
			t.Fatal(r.status, string(r.body))
		}
		// The owner's change holds the site's lock, as the lock route does.
		unlock := h.lockSite(f.ownerID, "shop")
		result := make(chan error, 1)
		go func() {
			_, err := f.a.conn.mcpConfig.VisitorTool(httptest.NewRequest("GET", "/mcp", nil), mcp.Caller{Owner: f.ids["bob"]}, "visitor_read_site", args)
			result <- err
		}()
		time.Sleep(wait) // the read has checked "123456" and waits for the lock
		_, dbErr := f.a.database.Exec(`UPDATE sites SET passcode_enc=$2, passcode_generation=passcode_generation+1 WHERE id=$1`, f.sid, changed)
		unlock()
		if dbErr != nil {
			t.Fatal(dbErr)
		}
		err := <-result
		if mcp.RefusalCode(err) != "site_locked" {
			t.Fatalf("page read across a passcode change: %v, want site_locked", err)
		}
		if !strings.Contains(err.Error(), "isn't right") {
			return // refused under the lock, after its first check had passed
		}
		// The read had not reached its first check yet; give it longer.
	}
	t.Fatal("the read never reached the site lock before the passcode changed")
}
