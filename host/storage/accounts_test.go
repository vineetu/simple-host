package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestDeleteUserFiles(t *testing.T) {
	d, err := NewDiskStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"owner", "other"} {
		if err := d.WriteFiles(context.Background(), id, "site", 1, map[string][]byte{"index.html": []byte("hello")}); err != nil {
			t.Fatal(err)
		}
		if err := d.EnsureHandleLink(id, id); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.DeleteUser("owner", "owner"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(d.dataDir, "by-id", "owner"), filepath.Join(d.dataDir, "handles", "owner")} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("%s still exists: %v", path, err)
		}
	}
	if _, err := os.Stat(filepath.Join(d.dataDir, "handles", "other", "site", "v1", "index.html")); err != nil {
		t.Fatal(err)
	}
	if err := d.DeleteUser("owner", "owner"); err != nil {
		t.Fatal(err)
	}
}

func TestRemoveHandleLink(t *testing.T) {
	d, err := NewDiskStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := d.EnsureHandleLink("old", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := d.RemoveHandleLink("old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(d.dataDir, "handles", "real"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := d.RemoveHandleLink("real"); err == nil {
		t.Fatal("removed real directory")
	}
	if err := d.DeleteUser("../outside", ""); err == nil {
		t.Fatal("accepted invalid user path")
	}
}

func TestRemoveHandleLinkOfLeavesOthers(t *testing.T) {
	d, err := NewDiskStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// "shared" points at another account: erasing "owner" must leave it.
	if err := d.EnsureHandleLink("shared", "other"); err != nil {
		t.Fatal(err)
	}
	if err := d.EnsureHandleLink("mine", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := d.RemoveHandleLinkOf("shared", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := d.DeleteUser("owner", "shared"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(d.dataDir, "handles", "shared")); err != nil {
		t.Fatalf("another account's link went: %v", err)
	}
	if err := d.RemoveHandleLinkOf("mine", "owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(d.dataDir, "handles", "mine")); !os.IsNotExist(err) {
		t.Fatalf("own link kept: %v", err)
	}
	if err := d.RemoveHandleLinkOf("missing", "owner"); err != nil {
		t.Fatal(err)
	}
}
