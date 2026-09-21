package handler

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

	"github.com/vsriram/simple-host/internal/auth"
	"github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/storage"
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
	versions := []db.Version{{VersionNumber: 2, CreatedAt: created}}
	request := func(path string, listing bool, rows []db.Version, root string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/", nil)
		r.SetPathValue("path", path)
		w := httptest.NewRecorder()
		serveVersionFiles(w, r, root, rows, 2, 2, listing)
		return w
	}
	t.Run("listing", func(t *testing.T) {
		w := request("", true, versions, dir)
		var got versionFilesResponse
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		want := versionFilesResponse{2, true, created, []versionFile{{"a..b", 4}, {"assets/a.css", 6}, {"data.json", 12}, {"index.html", 12}, {"z.unknownext", 3}}}
		if w.Code != 200 || !reflect.DeepEqual(got, want) {
			t.Fatalf("status %d: %+v, want %+v", w.Code, got, want)
		}
	})
	for _, path := range []string{"../x", "/etc/passwd", "a/../../x", `a\b`, "a\x00b", "a/../b", "..", "", ".", "a/./b", "a//b", "a/"} {
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
			rows       []db.Version
		}{{"pruned row", dir, nil}, {"missing dir", filepath.Join(dir, "missing"), versions}} {
			t.Run(c.name, func(t *testing.T) {
				w := request("index.html", listing, c.rows, c.root)
				if w.Code != 404 || w.Body.String() != "{\"error\":\"version not found\"}\n" {
					t.Fatalf("%d %s", w.Code, w.Body.String())
				}
			})
		}
	}
	for _, path := range []string{"missing", "assets", "index.html/child", "assets/a.css/deeper"} { // last two: ENOTDIR must read as not found
		w := request(path, false, versions, dir)
		if w.Code != 404 || w.Body.String() != "{\"error\":\"file not found\"}\n" {
			t.Fatalf("%q: %d %s", path, w.Code, w.Body.String())
		}
	}
	for _, c := range []struct{ path, contentType, body string }{{"a..b", "application/octet-stream", "dots"}, {"index.html", "text/html; charset=utf-8", "<h1>old</h1>"}, {"z.unknownext", "application/octet-stream", "raw"}} {
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
		want := []versionFile{{"a..b", 4}, {"alias.html", 3}, {"assets/a.css", 6}, {"data.json", 12}, {"index.html", 12}, {"z.unknownext", 3}}
		if w.Code != 200 || !reflect.DeepEqual(got.Files, want) {
			t.Fatalf("status %d: %+v, want %+v", w.Code, got.Files, want)
		}
		resolved, err := versionFilePath(dir, "alias.html")
		if err != nil || resolved != filepath.Join(dir, "z.unknownext") {
			t.Fatalf("resolved path = %q, error = %v", resolved, err)
		}
	})
	t.Run("symlink version directory", func(t *testing.T) {
		alias := filepath.Join(t.TempDir(), "v2")
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
	t.Run("HEAD", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodHead, "/", nil)
		r.SetPathValue("path", "index.html")
		w := httptest.NewRecorder()
		serveVersionFiles(w, r, dir, versions, 2, 2, false)
		get := request("index.html", false, versions, dir)
		if w.Code != 200 || w.Body.Len() != 0 || !reflect.DeepEqual(w.Header(), get.Header()) {
			t.Fatalf("HEAD status %d, headers %v, body %q; GET headers %v", w.Code, w.Header(), w.Body.String(), get.Header())
		}
	})
	t.Run("empty listing", func(t *testing.T) {
		w := request("", true, versions, t.TempDir())
		var got map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if files, ok := got["files"].([]any); !ok || len(files) != 0 {
			t.Fatalf("files = %#v", got["files"])
		}
	})
	t.Run("raw JSON through notice middleware", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.Handle("GET /v1/sites/{sitename}/versions/{version}/files/{path...}", NoticeMiddleware("0.16.0")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, buffered := w.(*bufferingResponseWriter); buffered {
				t.Error("file response buffered")
			}
			serveVersionFiles(w, r, dir, versions, 2, 2, false)
		})))
		for _, skillVersion := range []string{"", "0.1.0", "0.16.0"} {
			w := httptest.NewRecorder()
			r := httptest.NewRequest("GET", "/v1/sites/site/versions/2/files/data.json", nil)
			r.Header.Set("X-Skill-Version", skillVersion)
			mux.ServeHTTP(w, r)
			if w.Code != 200 || w.Body.String() != `{"old":true}` {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
		}
	})
}

func TestVersionFilesRequestValidation(t *testing.T) {
	h := &SiteHandler{}
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
			auth.Middleware("test-key", "owner", nil)(handler).ServeHTTP(w, r)
			if w.Code != 400 {
				t.Fatalf("%+v: %d", c, w.Code)
			}
		}
	}
}
