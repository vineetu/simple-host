package storage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAccessMarkerWriteRemoveAndRestore(t *testing.T) {
	d, err := NewDiskStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, on := range []bool{true, false} {
		if err := d.SetPasscodeMarker("owner", "missing", on); err != nil {
			t.Fatal(err)
		}
	}
	if d.HasPasscodeMarker("owner", "missing") {
		t.Fatal("created missing folder")
	}
	dir := d.SiteDir("owner", "page")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, on := range []bool{true, true, false, false, true} {
		if err := d.SetPasscodeMarker("owner", "page", on); err != nil {
			t.Fatal(err)
		}
		if d.HasPasscodeMarker("owner", "page") != on {
			t.Fatal("wrong marker state")
		}
	}
	if err := d.TrashSite("owner", "page", "site-id"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(d.TrashDir("owner", "site-id"), "passcode")); err != nil {
		t.Fatal(err)
	}
	if err := d.SetTrashedAccessMarker("owner", "site-id", true); err != nil {
		t.Fatal(err)
	}
	if err := d.RestoreSite("owner", "page", "site-id"); err != nil {
		t.Fatal(err)
	}
	if !d.HasPasscodeMarker("owner", "page") {
		t.Fatal("restore did not carry marker")
	}
}

func TestAccessMarkerFilesystemFailures(t *testing.T) {
	d, err := NewDiskStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(d.SiteDir("owner", "page"), "passcode")
	if err := os.MkdirAll(marker, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(marker, "block-removal"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, on := range []bool{true, false} {
		if err := d.SetPasscodeMarker("owner", "page", on); err == nil {
			t.Fatalf("on=%v: failed filesystem operation accepted", on)
		}
	}
	if err := d.SetPasscodeMarker("../escape", "page", true); err == nil {
		t.Fatal("invalid key")
	}
}
