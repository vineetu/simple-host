package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRedirectsPromotion(t *testing.T) {
	d, err := NewDiskStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{"index.html": []byte("home"), "_redirects": []byte("/a /b"), "sub/_redirects": []byte("ordinary")}
	if e := d.WriteFiles(context.Background(), "user", "site", 1, files); e != nil {
		t.Fatal(e)
	}
	if e := d.UpdateCurrent("user", "site", 1); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(filepath.Join(d.SiteDir("user", "site"), "current", "_redirects")); !os.IsNotExist(e) {
		t.Fatal(e)
	}
	for _, p := range []string{"v1/_redirects", "current/sub/_redirects", "redirects"} {
		if _, e := os.Stat(filepath.Join(d.SiteDir("user", "site"), p)); e != nil {
			t.Fatal(e)
		}
	}
	if b, e := d.ReadRedirects("user", "site"); e != nil || string(b) != "/a /b" {
		t.Fatalf("%s %v", b, e)
	}
	delete(files, "_redirects")
	if e := d.WriteFiles(context.Background(), "user", "site", 2, files); e != nil {
		t.Fatal(e)
	}
	if e := d.UpdateCurrent("user", "site", 2); e != nil {
		t.Fatal(e)
	}
	if _, e := d.ReadRedirects("user", "site"); !os.IsNotExist(e) {
		t.Fatal(e)
	}
}

func TestRedirectsFollowSiteLifecycle(t *testing.T) {
	d, e := NewDiskStorage(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	if e = d.WriteFiles(context.Background(), "user", "site", 1, map[string][]byte{"index.html": []byte("home"), "_redirects": []byte("/a /b")}); e != nil {
		t.Fatal(e)
	}
	if e = d.UpdateCurrent("user", "site", 1); e != nil {
		t.Fatal(e)
	}
	if e = d.RenameSite("user", "site", "renamed"); e != nil {
		t.Fatal(e)
	}
	if _, e = d.ReadRedirects("user", "renamed"); e != nil {
		t.Fatal(e)
	}
	if e = d.TrashSite("user", "renamed", "site-id"); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(filepath.Join(d.TrashDir("user", "site-id"), "redirects")); e != nil {
		t.Fatal(e)
	}
	if e = d.RestoreSite("user", "renamed", "site-id"); e != nil {
		t.Fatal(e)
	}
	if _, e = d.ReadRedirects("user", "renamed"); e != nil {
		t.Fatal(e)
	}
	if e = d.DeleteSite("user", "renamed"); e != nil {
		t.Fatal(e)
	}
	if _, e = d.ReadRedirects("user", "renamed"); !os.IsNotExist(e) {
		t.Fatal(e)
	}
}

// A folder named _redirects is ordinary content: it is served and no
// marker is written.
func TestRedirectsFolderIsContent(t *testing.T) {
	d, err := NewDiskStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{"index.html": []byte("home"), "_redirects/help.html": []byte("help")}
	if err := d.WriteFiles(context.Background(), "u", "s", 1, files); err != nil {
		t.Fatal(err)
	}
	if err := d.UpdateCurrent("u", "s", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(d.SiteDir("u", "s"), "current", "_redirects", "help.html")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(d.RedirectsFile("u", "s")); !os.IsNotExist(err) {
		t.Fatalf("marker written for a folder: %v", err)
	}
}
