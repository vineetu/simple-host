package handler

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/vsriram/simple-host/host/storage"
)

func TestVisitorPageTextPathResolution(t *testing.T) {
	disk, err := storage.NewDiskStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := disk.WriteFiles(context.Background(), "owner", "site", 1, map[string][]byte{
		"index.html":               []byte("<p>root page</p>"),
		"nested/index.html":        []byte("<p>nested page</p>"),
		"nested/child/inside.html": []byte("<p>child page</p>"),
	}); err != nil {
		t.Fatal(err)
	}
	if err := disk.UpdateCurrent("owner", "site", 1); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(disk.SiteDir("owner", "site"), "current")
	if err := os.Symlink("nested/child", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(dir, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "index.html"), filepath.Join(dir, "absolute.html")); err != nil {
		t.Fatal(err)
	}
	h := &SiteHandler{disk: disk}
	for _, tc := range []struct {
		page, want string
	}{
		{"/", "root page"},
		{"nested/", "nested page"},
		{"nested/../index.html", "root page"},
		{"./index.html", "root page"},
		{"nested//index.html", "nested page"},
		{"missing/../index.html", ""},
		{"index.html/../index.html", ""},
		{"link/../index.html", "nested page"},
		{"link/../../index.html", "root page"},
		{"../index.html", ""},
		{"nested/../../index.html", ""},
		{"escape/../index.html", ""},
		{"absolute.html", ""},
	} {
		t.Run(tc.page, func(t *testing.T) {
			got, truncated, err := h.visitorPageText("owner", "site", tc.page)
			if tc.want == "" {
				if err == nil || err.Error() != "page not found" {
					t.Fatalf("text = %q, error = %v; want page not found", got, err)
				}
				return
			}
			if err != nil || got != tc.want || truncated {
				t.Fatalf("text = %q, truncated = %v, error = %v; want %q", got, truncated, err, tc.want)
			}
		})
	}
}
