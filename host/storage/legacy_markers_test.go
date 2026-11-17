package storage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRemoveLegacyUnlistedMarkersOnOpen(t *testing.T) {
	root := t.TempDir()
	for _, tree := range []string{"by-id", "deleted"} {
		dir := filepath.Join(root, tree, "owner", "site")
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		for _, file := range []string{"unlisted", "passcode", "offline", "suspended"} {
			if err := os.WriteFile(filepath.Join(dir, file), nil, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	for i := 0; i < 2; i++ {
		if _, err := NewDiskStorage(root); err != nil {
			t.Fatal(err)
		}
		for _, tree := range []string{"by-id", "deleted"} {
			for _, file := range []string{"unlisted", "passcode", "offline", "suspended"} {
				_, err := os.Stat(filepath.Join(root, tree, "owner", "site", file))
				if file == "unlisted" {
					if !os.IsNotExist(err) {
						t.Fatalf("legacy marker: %v", err)
					}
				} else if err != nil {
					t.Fatalf("changed %s: %v", file, err)
				}
			}
		}
	}
}
