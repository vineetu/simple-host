package storage

import (
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
