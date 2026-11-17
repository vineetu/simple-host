package storage

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenValidatedFileRejectsSymlinkReplacement(t *testing.T) {
	for _, replace := range []string{"file", "parent"} {
		t.Run(replace, func(t *testing.T) {
			dir := t.TempDir()
			outside := t.TempDir()
			for _, base := range []string{dir, outside} {
				if err := os.Mkdir(filepath.Join(base, "assets"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(base, "assets", "page.html"), []byte(base), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			root, err := openFileRoot(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			path, err := filePathWithin(dir, "assets/page.html")
			if err != nil {
				t.Fatal(err)
			}
			// Replace the file or its parent after validation, before opening.
			rel := filepath.Join("assets", "page.html")
			if replace == "parent" {
				rel = "assets"
			}
			if err := os.RemoveAll(filepath.Join(dir, rel)); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(outside, rel), filepath.Join(dir, rel)); err != nil {
				t.Fatal(err)
			}
			f, err := openValidatedFile(root, path)
			if err == nil {
				f.Close()
				t.Fatal("opened an escaping replacement symlink")
			}
		})
	}
}

func TestDiskOpenFileSymlinks(t *testing.T) {
	d, err := NewDiskStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	v := FileVersion{UserID: "owner", SiteName: "site", Version: 1}
	ctx := context.Background()
	if err := d.WriteFiles(ctx, v.UserID, v.SiteName, v.Version, map[string][]byte{"page.html": []byte("page")}); err != nil {
		t.Fatal(err)
	}
	dir := d.VersionDir(v.UserID, v.SiteName, v.Version)
	outside := filepath.Join(t.TempDir(), "outside.html")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, target := range map[string]string{
		"relative.html": "page.html",
		"absolute.html": filepath.Join(dir, "page.html"),
		"escape.html":   outside,
	} {
		if err := os.Symlink(target, filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
		f, info, err := d.OpenFile(ctx, v, name)
		if name == "escape.html" {
			if err == nil {
				f.Close()
				t.Fatal("opened escaping symlink")
			}
			if !errors.Is(err, ErrInvalidFilePath) {
				t.Fatalf("escaping symlink error = %v", err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("open %s: %v", name, err)
		}
		body, err := io.ReadAll(f)
		f.Close()
		if err != nil || string(body) != "page" || info.Size() != 4 {
			t.Fatalf("%s: body=%q info=%v err=%v", name, body, info, err)
		}
	}
}

func TestDiskOpenFileRelativeDataDir(t *testing.T) {
	t.Chdir(t.TempDir())
	d, err := NewDiskStorage("./data/sites")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	v := FileVersion{UserID: "owner", SiteName: "site", Version: 2}
	if err := d.WriteFiles(ctx, v.UserID, v.SiteName, v.Version, map[string][]byte{"index.html": []byte("page")}); err != nil {
		t.Fatal(err)
	}
	if err := d.UpdateCurrent(v.UserID, v.SiteName, v.Version); err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{2, 0} {
		v.Version = n
		got, err := d.ReadFile(ctx, v, "index.html")
		if err != nil || string(got) != "page" {
			t.Fatalf("version %d: read = %q, %v", n, got, err)
		}
	}
}

func TestDiskOpenFileSearchOnlyDirectories(t *testing.T) {
	for _, restrict := range []string{"version", "nested", "both"} {
		t.Run(restrict, func(t *testing.T) {
			d, err := NewDiskStorage(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			v := FileVersion{UserID: "owner", SiteName: "site", Version: 2}
			if err := d.WriteFiles(ctx, v.UserID, v.SiteName, v.Version, map[string][]byte{"assets/page.html": []byte("page")}); err != nil {
				t.Fatal(err)
			}
			dir := d.VersionDir(v.UserID, v.SiteName, v.Version)
			nested := filepath.Join(dir, "assets")
			for _, target := range []string{dir, nested} {
				if restrict == "version" && target != dir || restrict == "nested" && target != nested {
					continue
				}
				if err := os.Chmod(target, 0111); err != nil {
					t.Fatal(err)
				}
				defer os.Chmod(target, 0755)
				if _, err := os.ReadDir(target); err == nil {
					t.Skip("directory permissions do not restrict this user")
				} else if !os.IsPermission(err) {
					t.Fatal(err)
				}
			}
			// Establish that ordinary file reads still succeed with these modes.
			if got, err := os.ReadFile(filepath.Join(nested, "page.html")); err != nil || string(got) != "page" {
				t.Fatalf("direct read = %q, %v", got, err)
			}
			if got, err := d.ReadFile(ctx, v, "assets/page.html"); err != nil || string(got) != "page" {
				t.Fatalf("backend read = %q, %v", got, err)
			}
		})
	}
}
