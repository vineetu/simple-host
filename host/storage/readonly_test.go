package storage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenReadOnlyPreservesTree(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "by-id", "owner", "site", "unlisted")
	if err := os.MkdirAll(filepath.Dir(marker), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte("legacy"), 0600); err != nil {
		t.Fatal(err)
	}
	disk, err := OpenReadOnly(dir)
	if err != nil {
		t.Fatal(err)
	}
	if disk.SiteDir("owner", "site") != filepath.Dir(marker) {
		t.Fatal("wrong serving directory")
	}
	if b, err := os.ReadFile(marker); err != nil || string(b) != "legacy" {
		t.Fatal("read-only open removed a legacy marker")
	}
	missing := filepath.Join(dir, "missing")
	if _, err = OpenReadOnly(missing); !os.IsNotExist(err) {
		t.Fatalf("missing directory: %v", err)
	}
	if _, err = os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("created missing data directory")
	}
	if _, err = OpenReadOnly(marker); err == nil {
		t.Fatal("file accepted as data directory")
	}
	if _, err = LockMaintenanceReadOnly(dir); !os.IsNotExist(err) {
		t.Fatalf("created lock: %v", err)
	}
	lock, err := LockMaintenance(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	hold, err := LockMaintenanceReadOnly(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer hold.Close()
	if _, err = LockMaintenance(dir, true); err != ErrDataDirInUse {
		t.Fatalf("maintenance allowed while Static running: %v", err)
	}
}
