package siteshttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/host/sites/sitesroute"
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
	h := withHosts(&SiteHandler{Handler: &sitesroute.Handler{}})
	for _, tt := range []struct {
		url, body, location string
		status              int
	}{
		{"/faq", "plain faq", "", 200},
		{"/faq/", "directory faq", "", 200},
		{"/only", "", "/only/", 301},
		{"/only-file/?from=a%2Fb", "", "/only-file?from=a%2Fb", 301},
		{"/exact", "exact file", "", 200},
		{"/_redirects", "custom missing", "", 404},
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
	h := withHosts(&SiteHandler{Handler: &sitesroute.Handler{Disk: disk, Limits: config.DefaultLimits()}})
	for _, tt := range []struct {
		method, rel, public, query, location, body string
		status                                     int
		applied                                    bool
	}{
		{"GET", "old", "/old", "track=1", "/new?track=1", "", 301, true},
		{"GET", "old", "/ann/site/old", "", "/ann/site/new", "", 301, true},
		{"GET", "faq", "/faq", "", "", "", 200, false},
		{"HEAD", "force", "/force", "", "/new", "", 302, true},
		{"GET", "gone", "/gone", "", "", "gone", 410, true},
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
}

const publicPageCache = "public, max-age=0, must-revalidate"

// applyRedirect runs applyRootRedirect against the site's folder on disk.
func (h *SiteHandler) applyRedirect(w http.ResponseWriter, r *http.Request, userID, siteName, rel, publicPath string) bool {
	root, err := os.OpenRoot(h.Disk.SiteDir(userID, siteName))
	if err != nil {
		return false
	}
	defer root.Close()
	return h.applyRootRedirect(w, r, root, rel, publicPath)
}
