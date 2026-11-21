package siteshttp

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/host/storage"
)

func TestPlainAddressesAndHiddenRules(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"faq.html":        "plain faq",
		"only-file.html":  "file",
		"faq/index.html":  "directory faq",
		"only/index.html": "only directory",
		"exact":           "exact file",
		"exact.html":      "other file",
		"_redirects":      "SECRET",
		"sub/_redirects":  "ordinary",
		"404.html":        "custom missing",
	} {
		p := filepath.Join(dir, name)
		if e := os.MkdirAll(filepath.Dir(p), 0755); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(p, []byte(body), 0644); e != nil {
			t.Fatal(e)
		}
	}
	h := withHosts(&SiteHandler{})
	for _, tt := range []struct {
		url, body, location string
		status              int
	}{
		{"/faq", "plain faq", "", 200},
		{"/faq/", "directory faq", "", 200},
		{"/only", "", "/only/", 301},
		{"/only-file/?from=a%2Fb", "", "/only-file?from=a%2Fb", 301},
		{"/%6Fnly-file%2F", "", "/%6Fnly-file", 301},
		{"//only-file/", "", "/only-file", 301},
		{"/exact", "exact file", "", 200},
		{"/_redirects", "custom missing", "", 404},
		{"/_redirects/", "custom missing", "", 404},
		{"/./_redirects", "custom missing", "", 404},
		{"/%5Fredirects", "custom missing", "", 404},
		{"/sub/_redirects", "ordinary", "", 200},
	} {
		r := httptest.NewRequest("GET", tt.url, nil)
		w := httptest.NewRecorder()
		h.serveDirFile(w, r, dir, r.URL.Path, r.URL.EscapedPath())
		if tt.status == 301 && w.Header().Get("Cache-Control") != publicPageCache {
			t.Errorf("%s: cache %q", tt.url, w.Header().Get("Cache-Control"))
		}
		if w.Code != tt.status || w.Header().Get("Location") != tt.location || tt.body != "" && !strings.Contains(w.Body.String(), tt.body) {
			t.Errorf("%s: %d %s %s", tt.url, w.Code, w.Header(), w.Body)
		}
	}
}
func TestApplyRedirect(t *testing.T) {
	disk, e := storage.NewDiskStorage(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	files := map[string][]byte{"index.html": []byte("home"), "faq.html": []byte("faq"), "force.html": []byte("shadowed"), "gone.html": []byte("gone"), "404.html": []byte("missing"), "_redirects": []byte("/old /new 301\n/faq /other\n/force /new 302!\n/gone /gone.html 410!\n/missing /absent.html 404!\n/page id=:id /post/:id\n/retired /faq 410!\n")}
	if e := disk.WriteFiles(context.Background(), "user", "site", 1, files); e != nil {
		t.Fatal(e)
	}
	if e := disk.UpdateCurrent("user", "site", 1); e != nil {
		t.Fatal(e)
	}
	h := withHosts(&SiteHandler{Disk: disk, Limits: config.DefaultLimits()})
	for _, tt := range []struct {
		method, rel, public, query, location, body string
		status                                     int
		applied                                    bool
	}{
		{"GET", "old", "/old", "track=1", "/new?track=1", "", 301, true},
		{"GET", "old", "/ann/site/old", "", "/ann/site/new", "", 301, true},
		{"GET", "old", "/ann/site/%6Fld", "", "/ann/site/new", "", 301, true},
		{"GET", "", "/ann/site/", "", "", "", 200, false},
		{"GET", "faq", "/faq", "", "", "", 200, false},
		{"HEAD", "force", "/force", "", "/new", "", 302, true},
		{"GET", "gone", "/gone", "", "", "gone", 410, true},
		{"HEAD", "gone", "/gone", "", "", "", 410, true},
		{"GET", "missing", "/missing", "", "", "missing", 404, true},
		{"GET", "page", "/page", "id=123&extra=x", "/post/123", "", 301, true},
		{"GET", "retired", "/retired", "", "", "faq", 410, true}, // pagepath.Resolve: /faq is faq.html
	} {
		r := httptest.NewRequest(tt.method, tt.public+"?"+tt.query, nil)
		w := httptest.NewRecorder()
		ok := h.applyRedirect(w, r, "user", "site", tt.rel, tt.public)
		if ok != tt.applied || w.Code != tt.status || w.Header().Get("Location") != tt.location || w.Body.String() != tt.body {
			t.Errorf("%+v: applied=%v %d %v %q", tt, ok, w.Code, w.Header(), w.Body.String())
		}
	}
	// Legacy files apply without promotion; parse errors are cached until a change.
	marker := disk.RedirectsFile("user", "site")
	if e := os.Remove(marker); e != nil {
		t.Fatal(e)
	}
	legacy := filepath.Join(disk.SiteDir("user", "site"), "current", "_redirects")
	if e := os.WriteFile(legacy, []byte("/old /legacy"), 0644); e != nil {
		t.Fatal(e)
	}
	r := httptest.NewRequest("GET", "/old", nil)
	w := httptest.NewRecorder()
	if !h.applyRedirect(w, r, "user", "site", "old", "/old") || w.Header().Get("Location") != "/legacy" {
		t.Fatal(w)
	}
	// Runtime equality guard includes query normalisation and Rewritten public paths.
	if e := os.WriteFile(legacy, []byte("/page id=:id /:id?id=:id 301!"), 0644); e != nil {
		t.Fatal(e)
	}
	r = httptest.NewRequest("GET", "/internal/passcode/page?id=page", nil)
	w = httptest.NewRecorder()
	if h.applyRedirect(w, r, "user", "site", "page", "/page") {
		t.Fatal("self redirect")
	}
}

const publicPageCache = "public, max-age=0, must-revalidate"

// A folder named _redirects at the root is ordinary content (only the root
// file holds rules).
func TestRedirectsFolderServed(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "_redirects", "index.html")
	if e := os.MkdirAll(filepath.Dir(p), 0755); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(p, []byte("folder page"), 0644); e != nil {
		t.Fatal(e)
	}
	h := withHosts(&SiteHandler{})
	for _, tt := range []struct {
		url      string
		status   int
		location string
	}{{"/_redirects", 301, "/_redirects/"}, {"/_redirects/", 200, ""}} {
		r := httptest.NewRequest("GET", tt.url, nil)
		w := httptest.NewRecorder()
		h.serveDirFile(w, r, dir, r.URL.Path, r.URL.EscapedPath())
		if w.Code != tt.status || w.Header().Get("Location") != tt.location {
			t.Errorf("%s: %d %v", tt.url, w.Code, w.Header())
		}
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if !rootContentExists(root, "/_redirects/") {
		t.Error("folder not counted as content")
	}
}
