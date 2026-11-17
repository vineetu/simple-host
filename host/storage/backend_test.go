package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

// The same content-only consumer runs against disk and a fake with no directory,
// VersionDir or DataDir. Promotion is fixture setup, since it stays local.
func TestSiteFilesContract(t *testing.T) {
	factories := map[string]func(*testing.T) (SiteFiles, func(string, string, int)){
		"disk": func(t *testing.T) (SiteFiles, func(string, string, int)) {
			d, err := NewDiskStorage(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			return d, func(user, site string, n int) {
				t.Helper()
				if err := d.UpdateCurrent(user, site, n); err != nil {
					t.Fatal(err)
				}
			}
		},
		"memory": func(t *testing.T) (SiteFiles, func(string, string, int)) {
			d := &memorySiteFiles{trees: map[FileVersion]map[string][]byte{}}
			return d, func(user, site string, n int) {
				d.trees[FileVersion{UserID: user, SiteName: site}] = cloneFiles(d.trees[FileVersion{UserID: user, SiteName: site, Version: n}])
			}
		},
	}
	for name, factory := range factories {
		t.Run(name, func(t *testing.T) {
			files, promote := factory(t)
			ctx := context.Background()
			one := FileVersion{UserID: "owner", SiteName: "site", Version: 1}
			must := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			write := func(site string, n int, body map[string][]byte) {
				t.Helper()
				must(files.WriteFiles(ctx, "owner", site, n, body))
			}
			read := func(ref FileVersion, path, want string) {
				t.Helper()
				got, err := files.ReadFile(ctx, ref, path)
				must(err)
				if string(got) != want {
					t.Fatalf("%+v %s = %q, want %q", ref, path, got, want)
				}
			}
			missing := func(ref FileVersion) {
				t.Helper()
				_, err := files.ReadFile(ctx, ref, "index.html")
				if !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("missing %+v: %v", ref, err)
				}
			}
			// Linux uploads accept a literal backslash as part of a filename.
			backslash := FileVersion{UserID: "owner", SiteName: "backslash", Version: 1}
			write("backslash", 1, map[string][]byte{`photo\one.png`: []byte("image")})
			read(backslash, `photo\one.png`, "image")
			info, err := files.StatFile(ctx, backslash, `photo\one.png`)
			must(err)
			if info.Size() != 5 {
				t.Fatal(info)
			}
			must(files.DeleteSite("owner", "backslash"))
			original := map[string][]byte{"index.html": []byte("one"), "assets/a.txt": []byte("asset"), "empty": {}}
			write("site", 1, original)
			if _, err := files.StatVersion(ctx, FileVersion{UserID: "owner", SiteName: "missing", Version: 1}); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("stat missing version: %v", err)
			}
			info, err = files.StatVersion(ctx, one)
			must(err)
			if !info.IsDir() {
				t.Fatalf("version metadata = %v", info)
			}
			original["index.html"][0] = 'X' // caller buffers do not own stored bytes
			read(one, "index.html", "one")
			var names []string
			must(files.WalkFiles(ctx, one, func(file SiteFile, err error) error {
				must(err)
				names = append(names, file.Name)
				if !file.Mode.IsRegular() {
					t.Fatalf("unexpected mode %v", file.Mode)
				}
				raw, err := io.ReadAll(file.Reader)
				must(err)
				if int64(len(raw)) != file.Size {
					t.Fatalf("%s: size %d != %d", file.Name, len(raw), file.Size)
				}
				return nil
			}))
			if !reflect.DeepEqual(names, []string{"assets/a.txt", "empty", "index.html"}) {
				t.Fatal(names)
			}
			stop := errors.New("consumer stopped")
			calls := 0
			err = files.WalkFiles(ctx, one, func(_ SiteFile, err error) error {
				if err != nil {
					return err
				}
				calls++
				return stop
			})
			if !errors.Is(err, stop) || calls != 1 {
				t.Fatalf("walk cancellation: %d %v", calls, err)
			}
			cancelled, cancel := context.WithCancel(ctx)
			cancel()
			if err := files.WalkFiles(cancelled, one, func(_ SiteFile, err error) error {
				if err != nil {
					return err
				}
				t.Fatal("callback after cancellation")
				return nil
			}); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if _, err := files.ReadFile(cancelled, one, "index.html"); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if _, err := files.StatFile(cancelled, one, "index.html"); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if _, err := files.StatVersion(cancelled, one); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			for _, bad := range []string{"../escape", "/absolute", "a/../index.html", "a\x00b", ""} {
				if _, err := files.ReadFile(ctx, one, bad); !errors.Is(err, ErrInvalidFilePath) {
					t.Fatalf("%q: %v", bad, err)
				}
				if _, err := files.StatFile(ctx, one, bad); !errors.Is(err, ErrInvalidFilePath) {
					t.Fatalf("stat %q: %v", bad, err)
				}
			}
			f, info, err := files.OpenFile(ctx, one, "assets/a.txt")
			must(err)
			raw, err := io.ReadAll(f)
			must(err)
			must(f.Close())
			if string(raw) != "asset" || info.Size() != 5 {
				t.Fatal(string(raw), info)
			}
			write("site", 2, map[string][]byte{"index.html": []byte("two!")})
			versions, err := files.ListVersions("owner", "site")
			must(err)
			if !reflect.DeepEqual(versions, []int{1, 2}) {
				t.Fatal(versions)
			}
			promote("owner", "site", 1)
			live := FileVersion{UserID: "owner", SiteName: "site"}
			read(live, "assets/../index.html", "one")
			if _, err := files.ReadFile(ctx, live, "missing/../index.html"); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("missing live page parent: %v", err)
			}
			fp, err := files.SiteFootprint("owner", "site")
			must(err)
			if fp.Live != 8 || !reflect.DeepEqual(fp.Versions, map[int]int64{1: 8, 2: 4}) {
				t.Fatalf("footprint %+v", fp)
			}
			n, err := files.AccountFileBytes("owner")
			must(err)
			if n != 20 {
				t.Fatal(n)
			}
			// Replacement removes paths omitted by the new complete version.
			write("site", 2, map[string][]byte{"index.html": []byte("replacement"), "old": []byte("gone")})
			write("site", 2, map[string][]byte{"index.html": []byte("two!")})
			if _, err := files.ReadFile(ctx, FileVersion{UserID: "owner", SiteName: "site", Version: 2}, "old"); !errors.Is(err, os.ErrNotExist) {
				t.Fatal(err)
			}
			// Hash removal spans retained versions and live bytes, and preserves
			// different bytes stored at the same name in another version.
			sum := sha256.Sum256([]byte("one"))
			must(files.RemoveFileEverywhere("owner", "site", "index.html", hex.EncodeToString(sum[:])))
			missing(one)
			missing(FileVersion{UserID: "owner", SiteName: "site"})
			read(FileVersion{UserID: "owner", SiteName: "site", Version: 2}, "index.html", "two!")
			must(files.TrashSite("owner", "site", "site-id"))
			missing(one)
			trashed := FileVersion{UserID: "owner", SiteName: "site", TrashID: "site-id", Version: 2}
			_, err = files.StatVersion(ctx, trashed)
			must(err)
			read(trashed, "index.html", "two!")
			n, err = files.AccountFileBytes("owner")
			must(err)
			if n != 14 {
				t.Fatal(n)
			}
			must(files.RestoreSite("owner", "restored", "site-id"))
			read(FileVersion{UserID: "owner", SiteName: "restored", Version: 2}, "index.html", "two!")
			must(files.RenameSite("owner", "restored", "renamed"))
			read(FileVersion{UserID: "owner", SiteName: "renamed", Version: 2}, "index.html", "two!")
			must(files.DeleteVersion("owner", "renamed", 2))
			must(files.DeleteVersion("owner", "renamed", 2))
			must(files.TrashSite("owner", "renamed", "site-id"))
			must(files.PurgeTrashedSite("owner", "site-id"))
			must(files.PurgeTrashedSite("owner", "site-id"))
			missing(trashed)
			write("delete", 1, map[string][]byte{"index.html": []byte("delete")})
			must(files.DeleteSite("owner", "delete"))
			must(files.DeleteSite("owner", "delete"))
			missing(FileVersion{UserID: "owner", SiteName: "delete", Version: 1})
			write("active", 1, map[string][]byte{"index.html": []byte("active")})
			write("trash", 1, map[string][]byte{"index.html": []byte("trash")})
			must(files.TrashSite("owner", "trash", "trash-id"))
			must(files.DeleteUser("owner", "handle"))
			n, err = files.AccountFileBytes("owner")
			must(err)
			if n != 0 {
				t.Fatal(n)
			}
			versions, err = files.ListVersions("owner", "missing")
			must(err)
			if len(versions) != 0 {
				t.Fatal(versions)
			}
		})
	}
}

// memorySiteFiles deliberately has no filesystem path or LocalServing methods.
// Only fixture setup supplies its live tree.
type memorySiteFiles struct {
	trees map[FileVersion]map[string][]byte
}

var _ SiteFiles = (*memorySiteFiles)(nil)

func cloneFiles(in map[string][]byte) map[string][]byte {
	out := map[string][]byte{}
	for k, v := range in {
		out[k] = bytes.Clone(v)
	}
	return out
}
func (m *memorySiteFiles) WriteFiles(ctx context.Context, u, s string, n int, files map[string][]byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.trees[FileVersion{UserID: u, SiteName: s, Version: n}] = cloneFiles(files)
	return nil
}
func (m *memorySiteFiles) ReadFile(ctx context.Context, v FileVersion, name string) ([]byte, error) {
	f, _, err := m.OpenFile(ctx, v, name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}
func (m *memorySiteFiles) OpenFile(ctx context.Context, v FileVersion, name string) (io.ReadCloser, fs.FileInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if v.TrashID != "" {
		v.SiteName = ""
	}
	if v.Version == 0 {
		var err error
		name, err = m.liveFileName(v, name)
		if err != nil {
			return nil, nil, err
		}
	}
	info, err := m.StatFile(ctx, v, name)
	if err != nil {
		return nil, nil, err
	}
	return io.NopCloser(bytes.NewReader(m.trees[v][name])), info, nil
}

// Resolve the fake's implicit directories in order, so a missing parent cannot
// disappear through path cleaning. Symlinks are a disk-only fixture concern.
func (m *memorySiteFiles) liveFileName(v FileVersion, name string) (string, error) {
	if name == "" || strings.HasPrefix(name, "/") || strings.ContainsRune(name, '\x00') {
		return "", ErrInvalidFilePath
	}
	var parts []string
	segments := strings.Split(name, "/")
	for i, part := range segments {
		switch part {
		case "", ".":
			continue
		case "..":
			if len(parts) == 0 {
				return "", ErrInvalidFilePath
			}
			parts = parts[:len(parts)-1]
			continue
		default:
			parts = append(parts, part)
		}
		if i < len(segments)-1 {
			prefix := strings.Join(parts, "/") + "/"
			found := false
			for key := range m.trees[v] {
				if strings.HasPrefix(key, prefix) {
					found = true
					break
				}
			}
			if !found {
				return "", os.ErrNotExist
			}
		}
	}
	return strings.Join(parts, "/"), nil
}
func (m *memorySiteFiles) StatVersion(ctx context.Context, v FileVersion) (fs.FileInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if v.TrashID != "" {
		v.SiteName = ""
	}
	if _, ok := m.trees[v]; !ok {
		return nil, os.ErrNotExist
	}
	return memoryInfo{directory: true}, nil
}
func (m *memorySiteFiles) StatFile(ctx context.Context, v FileVersion, name string) (fs.FileInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." || strings.ContainsRune(part, '\x00') {
			return nil, ErrInvalidFilePath
		}
	}
	if v.TrashID != "" {
		v.SiteName = ""
	}
	b, ok := m.trees[v][name]
	if !ok {
		return nil, os.ErrNotExist
	}
	return memoryInfo{name: name, size: int64(len(b))}, nil
}
func (m *memorySiteFiles) WalkFiles(ctx context.Context, v FileVersion, visit func(SiteFile, error) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if v.TrashID != "" {
		v.SiteName = ""
	}
	tree, ok := m.trees[v]
	if !ok {
		return visit(SiteFile{}, os.ErrNotExist)
	}
	names := make([]string, 0, len(tree))
	for name := range tree {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return err
		}
		b := tree[name]
		if err := visit(SiteFile{Name: name, Size: int64(len(b)), Mode: 0644, Reader: bytes.NewReader(b)}, nil); err != nil {
			return err
		}
	}
	return nil
}
func (m *memorySiteFiles) ListVersions(u, s string) ([]int, error) {
	out := []int{}
	for ref := range m.trees {
		if ref.UserID == u && ref.SiteName == s && ref.TrashID == "" && ref.Version > 0 {
			out = append(out, ref.Version)
		}
	}
	sort.Ints(out)
	return out, nil
}
func (m *memorySiteFiles) DeleteVersion(u, s string, n int) error {
	delete(m.trees, FileVersion{UserID: u, SiteName: s, Version: n})
	return nil
}
func memoryBytes(tree map[string][]byte) int64 {
	var n int64
	for _, b := range tree {
		n += int64(len(b))
	}
	return n
}
func (m *memorySiteFiles) SiteFootprint(u, s string) (FileFootprint, error) {
	f := FileFootprint{Versions: map[int]int64{}}
	for ref, tree := range m.trees {
		if ref.UserID != u || ref.SiteName != s || ref.TrashID != "" {
			continue
		}
		if ref.Version == 0 {
			f.Live = memoryBytes(tree)
		} else {
			f.Versions[ref.Version] = memoryBytes(tree)
		}
	}
	return f, nil
}
func (m *memorySiteFiles) AccountFileBytes(u string) (int64, error) {
	var n int64
	for ref, tree := range m.trees {
		if ref.UserID == u {
			n += memoryBytes(tree)
		}
	}
	return n, nil
}
func (m *memorySiteFiles) DeleteSite(u, s string) error {
	for ref := range m.trees {
		if ref.UserID == u && ref.SiteName == s && ref.TrashID == "" {
			delete(m.trees, ref)
		}
	}
	return nil
}
func (m *memorySiteFiles) DeleteUser(u, _ string) error {
	for ref := range m.trees {
		if ref.UserID == u {
			delete(m.trees, ref)
		}
	}
	return nil
}
func (m *memorySiteFiles) RenameSite(u, old, new string, _ ...string) error {
	return m.move(func(ref FileVersion) (FileVersion, bool) {
		if ref.UserID != u || ref.SiteName != old || ref.TrashID != "" {
			return ref, false
		}
		ref.SiteName = new
		return ref, true
	})
}
func (m *memorySiteFiles) move(change func(FileVersion) (FileVersion, bool)) error {
	next := map[FileVersion]map[string][]byte{}
	for ref, tree := range m.trees {
		if dest, ok := change(ref); ok {
			next[dest] = tree
			delete(m.trees, ref)
		}
	}
	for ref, tree := range next {
		m.trees[ref] = tree
	}
	return nil
}
func (m *memorySiteFiles) TrashSite(u, s, id string) error {
	return m.move(func(ref FileVersion) (FileVersion, bool) {
		if ref.UserID != u || ref.SiteName != s || ref.TrashID != "" {
			return ref, false
		}
		ref.SiteName = ""
		ref.TrashID = id
		return ref, true
	})
}
func (m *memorySiteFiles) RestoreSite(u, s, id string) error {
	for ref := range m.trees {
		if ref.UserID == u && ref.SiteName == s && ref.TrashID == "" {
			return os.ErrExist
		}
	}
	return m.move(func(ref FileVersion) (FileVersion, bool) {
		if ref.UserID != u || ref.TrashID != id {
			return ref, false
		}
		ref.SiteName = s
		ref.TrashID = ""
		return ref, true
	})
}
func (m *memorySiteFiles) PurgeTrashedSite(u, id string) error {
	for ref := range m.trees {
		if ref.UserID == u && ref.TrashID == id {
			delete(m.trees, ref)
		}
	}
	return nil
}
func (m *memorySiteFiles) RemoveFileEverywhere(u, s, name, sha string) error {
	for ref, tree := range m.trees {
		if ref.UserID != u || ref.SiteName != s || ref.TrashID != "" {
			continue
		}
		sum := sha256.Sum256(tree[name])
		if hex.EncodeToString(sum[:]) == sha {
			delete(tree, name)
		}
	}
	return nil
}

type memoryInfo struct {
	name      string
	size      int64
	directory bool
}

func (i memoryInfo) Name() string { return i.name }
func (i memoryInfo) Size() int64  { return i.size }
func (i memoryInfo) Mode() fs.FileMode {
	if i.directory {
		return fs.ModeDir | 0755
	}
	return 0644
}
func (i memoryInfo) ModTime() time.Time { return time.Time{} }
func (i memoryInfo) IsDir() bool        { return i.directory }
func (i memoryInfo) Sys() any           { return nil }

// The product's existing byte counter counts more than deploy quotas. Keep the
// local runtime/marker contribution separate without changing its physical total.
func TestAccountAuxiliaryBytes(t *testing.T) {
	d, err := NewDiskStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, site := range []string{"active", "trashed"} {
		if err := d.WriteFiles(ctx, "owner", site, 1, map[string][]byte{"index.html": []byte("hello")}); err != nil {
			t.Fatal(err)
		}
		if err := d.UpdateCurrent("owner", site, 1); err != nil {
			t.Fatal(err)
		}
		dir := d.SiteDir("owner", site)
		for name, body := range map[string]string{"runtime/files/photos/a": "photo", "offline": "offline", ".v2.tmp/pending": "pending"} {
			p := filepath.Join(dir, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(body), 0644); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := d.TrashSite("owner", "trashed", "id"); err != nil {
		t.Fatal(err)
	}
	deployed, err := d.AccountFileBytes("owner")
	if err != nil {
		t.Fatal(err)
	}
	local, err := d.AccountAuxiliaryBytes(ctx, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if deployed != 20 || local != 38 {
		t.Fatalf("deploy=%d local=%d", deployed, local)
	}
	var physical int64
	for _, area := range []string{"by-id", "deleted"} {
		n, err := treeBytes(filepath.Join(d.DataDir(), area, "owner"))
		if err != nil {
			t.Fatal(err)
		}
		physical += n
	}
	if deployed+local != physical {
		t.Fatalf("split=%d physical=%d", deployed+local, physical)
	}
}
