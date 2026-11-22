package versions

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"

	accountapp "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/host/edition"
	model "github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/sites/sitesroute"
	"github.com/vsriram/simple-host/host/storage"
)

func TestVersionFiles(t *testing.T) {
	disk, err := storage.NewDiskStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := disk.VersionDir("owner", "site", 2)
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0755); err != nil {
		t.Fatal(err)
	}
	for path, body := range map[string]string{"a..b": "dots", "index.html": "<h1>old</h1>", "assets/a.css": "body{}", "z.unknownext": "raw", "data.json": `{"old":true}`} {
		if err := os.WriteFile(filepath.Join(dir, path), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	created := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	versions := []model.Version{{VersionNumber: 2, CreatedAt: created}}
	// All fixture roots are logical sites under this one backend.
	refFor := func(root string) storage.FileVersion {
		if root == dir {
			return storage.FileVersion{UserID: "owner", SiteName: "site", Version: 2}
		}
		if root == filepath.Join(dir, "missing") {
			return storage.FileVersion{UserID: "owner", SiteName: "missing", Version: 2}
		}
		name := filepath.Base(filepath.Dir(root))
		return storage.FileVersion{UserID: "owner", SiteName: name, Version: 2}
	}
	request := func(path string, listing bool, rows []model.Version, root string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/", nil)
		r.SetPathValue("path", path)
		w := httptest.NewRecorder()
		serveVersionFiles(w, r, disk, refFor(root), rows, 2, 2, listing)
		return w
	}
	t.Run("listing", func(t *testing.T) {
		w := request("", true, versions, dir)
		var got versionFilesResponse
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		want := versionFilesResponse{2, true, created, []sitesroute.VersionFile{{Path: "a..b", Size: 4}, {Path: "assets/a.css", Size: 6}, {Path: "data.json", Size: 12}, {Path: "index.html", Size: 12}, {Path: "z.unknownext", Size: 3}}}
		if w.Code != 200 || !reflect.DeepEqual(got, want) {
			t.Fatalf("status %d: %+v, want %+v", w.Code, got, want)
		}
	})
	for _, path := range []string{"../x", "/etc/passwd", "a/../../x", `a\b`, "a\x00b", ""} {
		t.Run(path, func(t *testing.T) {
			w := request(path, false, versions, dir)
			if w.Code != 400 || w.Body.String() != "{\"error\":\"invalid path\"}\n" {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
		})
	}
	for _, listing := range []bool{false, true} {
		for _, c := range []struct {
			name, root string
			rows       []model.Version
		}{{"pruned row", dir, nil}, {"missing dir", filepath.Join(dir, "missing"), versions}} {
			t.Run(c.name, func(t *testing.T) {
				w := request("index.html", listing, c.rows, c.root)
				if w.Code != 404 || w.Body.String() != "{\"error\":\"version not found\"}\n" {
					t.Fatalf("%d %s", w.Code, w.Body.String())
				}
			})
		}
	}
	for _, path := range []string{"missing", "assets", "index.html/child"} { // last: ENOTDIR must read as not found
		w := request(path, false, versions, dir)
		if w.Code != 404 || w.Body.String() != "{\"error\":\"file not found\"}\n" {
			t.Fatalf("%q: %d %s", path, w.Code, w.Body.String())
		}
	}
	for _, c := range []struct{ path, contentType, body string }{{"index.html", "text/html; charset=utf-8", "<h1>old</h1>"}, {"z.unknownext", "application/octet-stream", "raw"}} {
		w := request(c.path, false, versions, dir)
		if w.Code != 200 || w.Body.String() != c.body {
			t.Fatalf("%d %s", w.Code, w.Body.String())
		}
		for key, want := range map[string]string{"Content-Type": c.contentType, "Content-Length": strconv.Itoa(len(c.body)), "Content-Security-Policy": "sandbox", "Cache-Control": "private, no-store", "X-Content-Type-Options": "nosniff"} {
			if got := w.Header().Get(key); got != want {
				t.Errorf("%s = %q, want %q", key, got, want)
			}
		}
	}
	t.Run("symlinks", func(t *testing.T) {
		if err := os.Symlink(filepath.Join(dir, "z.unknownext"), filepath.Join(dir, "alias.html")); err != nil {
			t.Fatal(err)
		}
		if w := request("alias.html", false, versions, dir); w.Code != 200 || w.Header().Get("Content-Type") != "text/html; charset=utf-8" || w.Body.String() != "raw" {
			t.Fatal(w.Code, w.Body.String())
		}
		outside := filepath.Join(t.TempDir(), "outside")
		if err := os.WriteFile(outside, []byte("secret"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(dir, "escape")); err != nil {
			t.Fatal(err)
		}
		if w := request("escape", false, versions, dir); w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
		if err := os.WriteFile(filepath.Join(dir, `bad\name`), []byte("hidden"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(dir, "missing"), filepath.Join(dir, "broken")); err != nil {
			t.Fatal(err)
		}
		w := request("", true, versions, dir)
		var got versionFilesResponse
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		want := []sitesroute.VersionFile{{Path: "a..b", Size: 4}, {Path: "alias.html", Size: 3}, {Path: "assets/a.css", Size: 6}, {Path: "data.json", Size: 12}, {Path: "index.html", Size: 12}, {Path: "z.unknownext", Size: 3}}
		if w.Code != 200 || !reflect.DeepEqual(got.Files, want) {
			t.Fatalf("status %d: %+v, want %+v", w.Code, got.Files, want)
		}

	})
	t.Run("symlink version directory", func(t *testing.T) {
		alias := disk.VersionDir("owner", "alias", 2)
		if err := os.MkdirAll(filepath.Dir(alias), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(dir, alias); err != nil {
			t.Fatal(err)
		}
		for _, listing := range []bool{false, true} {
			w := request("index.html", listing, versions, alias)
			if w.Code != 404 || w.Body.String() != "{\"error\":\"version not found\"}\n" {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
		}
	})
}

func TestVersionFilesRequestValidation(t *testing.T) {
	h := routes{&sitesroute.Handler{}}
	h.Hosts = sites.NewHosts(nil, nil, edition.Default(config.DefaultLimits()), "", "", "", "")
	for _, handler := range []http.HandlerFunc{h.listVersionFiles, h.getVersionFile} {
		w := httptest.NewRecorder()
		handler(w, httptest.NewRequest("GET", "/", nil))
		if w.Code != 401 {
			t.Fatal(w.Code)
		}
		for _, c := range []struct{ site, version string }{{"", "1"}, {"site", "abc"}, {"site", "0"}, {"site", "-1"}} {
			r := httptest.NewRequest("GET", "/", nil)
			r.SetPathValue("sitename", c.site)
			r.SetPathValue("version", c.version)
			r.Header.Set("X-API-Key", "test-key")
			w := httptest.NewRecorder()
			auth.Middleware("test-key", "owner", accountapp.New(nil), 180*24*time.Hour)(handler).ServeHTTP(w, r)
			if w.Code != 400 {
				t.Fatalf("%+v: %d", c, w.Code)
			}
		}
	}
}
