package handler

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/vsriram/simple-host/host/app/sites"
)

func TestExportRedirectsWithUnlistableTree(t *testing.T) {
	for _, tree := range []string{"live", "trash"} {
		t.Run(tree, func(t *testing.T) {
			a := newPrivateApp(t)
			owner := a.newPerson(t, "export-redirects-review")
			a.deploy(t, owner, "blog")
			userID, _ := a.userID(t, owner)
			siteID := a.siteID(t, owner, "blog")
			files := sites.FileVersion{UserID: userID, SiteName: "blog"}
			dir := a.sites.disk.SiteDir(userID, "blog")
			if tree == "trash" {
				if err := a.sites.disk.TrashSite(userID, "blog", siteID); err != nil {
					t.Fatal(err)
				}
				files.TrashID = siteID
				dir = a.sites.disk.TrashDir(userID, siteID)
			}
			const rules = "/old /new 302\n"
			if err := os.WriteFile(filepath.Join(dir, "redirects"), []byte(rules), 0644); err != nil {
				t.Fatal(err)
			}
			current := filepath.Join(dir, "current")
			if err := os.Chmod(current, 0111); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(current, 0755) })
			if _, err := os.ReadDir(current); err == nil {
				t.Skip("directory permissions do not restrict this user")
			} else if !os.IsPermission(err) {
				t.Fatal(err)
			}
			contents := map[string]string{}
			err := a.sites.writeSiteArchive(context.Background(), func(name string, _ time.Time, _ int64, r io.Reader) error {
				b, err := io.ReadAll(r)
				contents[name] = string(b)
				return err
			}, "blog", siteID, files)
			if err != nil {
				t.Fatal(err)
			}
			want := map[string]string{"blog/state.json": "null", "blog/files/_redirects": rules}
			if !reflect.DeepEqual(contents, want) {
				t.Fatalf("archive contents = %v, want %v", contents, want)
			}
		})
	}
}

func TestExportSkipsUnreadableRegularFile(t *testing.T) {
	for _, tree := range []string{"live", "trash"} {
		t.Run(tree, func(t *testing.T) {
			a := newPrivateApp(t)
			owner := a.newPerson(t, "export-review")
			a.deploy(t, owner, "blog")
			userID, _ := a.userID(t, owner)
			siteID := a.siteID(t, owner, "blog")
			files := sites.FileVersion{UserID: userID, SiteName: "blog"}
			dir := a.sites.disk.SiteDir(userID, "blog")
			if tree == "trash" {
				if err := a.sites.disk.TrashSite(userID, "blog", siteID); err != nil {
					t.Fatal(err)
				}
				files.TrashID = siteID
				dir = a.sites.disk.TrashDir(userID, siteID)
			}
			path := filepath.Join(dir, "current", "unreadable.txt")
			if err := os.WriteFile(path, []byte("unreadable bytes"), 0000); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(path, 0644) })
			if f, err := os.Open(path); err == nil {
				f.Close()
				t.Skip("file permissions do not restrict this user")
			} else if !os.IsPermission(err) {
				t.Fatal(err)
			}

			for _, format := range []string{"tar", "zip"} {
				t.Run(format, func(t *testing.T) {
					var buf bytes.Buffer
					tw, zw := tar.NewWriter(&buf), zip.NewWriter(&buf)
					put, finish := tarPut(tw), tw.Close
					if format == "zip" {
						put, finish = zipPut(zw), zw.Close
					}
					var names []string
					err := a.sites.writeSiteArchive(context.Background(), func(name string, modTime time.Time, size int64, r io.Reader) error {
						names = append(names, name)
						return put(name, modTime, size, r)
					}, "blog", siteID, files)
					if err != nil {
						t.Fatal(err)
					}
					if err := finish(); err != nil {
						t.Fatal(err)
					}
					if len(names) != 3 || names[0] != "blog/state.json" || names[1] != "blog/files/index.html" || names[2] != "blog/files/sub/index.html" {
						t.Fatalf("archive entries = %v", names)
					}
				})
			}
		})
	}
}
