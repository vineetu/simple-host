package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRenameSiteMovesFilesAndDomain(t *testing.T) {
	d, err := NewDiskStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(d.SiteDir("user-id", "before"), "current"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d.SiteDir("user-id", "before"), "current", "index.html"), []byte("renamed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := d.BindDomain("user-id", "before", "www.example.com"); err != nil {
		t.Fatal(err)
	}
	if err := d.RenameSite("user-id", "before", "after", "www.example.com"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(d.SiteDir("user-id", "after"), "current", "index.html"))
	if err != nil || string(b) != "renamed" {
		t.Fatalf("renamed file = %q, %v", b, err)
	}
	if _, err := os.Stat(d.SiteDir("user-id", "before")); !os.IsNotExist(err) {
		t.Fatalf("old directory still exists: %v", err)
	}
	target, err := os.Readlink(filepath.Join(d.DataDir(), "domains", "www.example.com"))
	if err != nil || target != filepath.Join("..", "by-id", "user-id", "after") {
		t.Fatalf("domain target = %q, %v", target, err)
	}
}

// Changes moves on every write that alters what the site tree holds, so the
// admin size report can tell its cached reading is out of date.
func TestChangesMovesOnSiteWrites(t *testing.T) {
	d, err := NewDiskStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	last := d.Changes()
	moved := func(what string) {
		t.Helper()
		if now := d.Changes(); now <= last {
			t.Errorf("%s did not move Changes (%d)", what, now)
		} else {
			last = now
		}
	}
	if err := d.WriteFiles(context.Background(), "user-1", "site", 1, map[string][]byte{"index.html": []byte("x")}); err != nil {
		t.Fatal(err)
	}
	moved("WriteFiles")
	if err := d.UpdateCurrent("user-1", "site", 1); err != nil {
		t.Fatal(err)
	}
	moved("UpdateCurrent")
	if err := d.DeleteVersion("user-1", "site", 1); err != nil {
		t.Fatal(err)
	}
	moved("DeleteVersion")
	if err := d.DeleteSite("user-1", "site"); err != nil {
		t.Fatal(err)
	}
	moved("DeleteSite")
}
