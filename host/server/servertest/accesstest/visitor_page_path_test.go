package accesstest

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/host/mcp"
	"github.com/vsriram/simple-host/host/server/servertest"
)

// visitor_read_site reads the page a browser gets at the same address.
func TestVisitorReadPageAddresses(t *testing.T) {
	f := servertest.NewVisitorFixture(t)
	dir := filepath.Join(f.A.Sites.Disk.SiteDir(f.OwnerID, "shop"), "current")
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
		args := map[string]any{"address": "https://" + f.Host, "page": tc.page}
		if tc.want == "" {
			f.Call(t, "visitor_read_site", args, "page not found")
			continue
		}
		out := f.Call(t, "visitor_read_site", args, "")["visitor_data"].(map[string]any)
		if out["page_text"] != tc.want {
			t.Errorf("%s: %v, want %q", tc.page, out["page_text"], tc.want)
		}
	}
}

// A passcode change that lands after the tool checked the passcode, and
// before it opened the page, must still keep the page from being read.
func TestVisitorPageReadDuringPasscodeChange(t *testing.T) {
	f := servertest.NewVisitorFixture(t)
	h := f.A.Access
	if err := h.SetPasscodeKey(servertest.TestPasscodeKey); err != nil {
		t.Fatal(err)
	}
	servertest.WithPasscodeLimits(t, func(l *config.Limits) { l.SitePasscodes = true }, &h.Limits, &h.Passcode.Limits, &f.A.Sites.Limits, &f.A.Data.Limits)
	changed, err := h.Passcode.Seal(f.Sid, "654321")
	if err != nil {
		t.Fatal(err)
	}
	args := map[string]any{"address": "https://" + f.Host, "page": "index.html", "passcode": "123456"}
	for _, wait := range []time.Duration{200 * time.Millisecond, 2 * time.Second, 10 * time.Second} {
		if r := f.A.At(t, "PUT", servertest.PcSiteDomain, "/v1/sites/shop/lock", map[string]any{"passcode": "123456"}, f.Key); r.Status != 200 {
			t.Fatal(r.Status, string(r.Body))
		}
		// The owner's change holds the site's lock, as the lock route does.
		unlock := f.A.Sites.LockSite(f.OwnerID, "shop")
		result := make(chan error, 1)
		go func() {
			_, err := f.A.Conn.MCPConfig.VisitorTool(httptest.NewRequest("GET", "/mcp", nil), mcp.Caller{Owner: f.Ids["bob"]}, "visitor_read_site", args)
			result <- err
		}()
		time.Sleep(wait) // the read has checked "123456" and waits for the lock
		_, dbErr := f.A.Database.Exec(`UPDATE sites SET passcode_enc=$2, passcode_generation=passcode_generation+1 WHERE id=$1`, f.Sid, changed)
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
