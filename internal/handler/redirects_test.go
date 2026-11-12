package handler

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/internal/config"
	"github.com/vsriram/simple-host/internal/storage"
)

func TestPlainAddressesAndHiddenRules(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{"faq.html": "plain faq", "faq/index.html": "directory faq", "only/index.html": "only directory", "exact": "exact file", "exact.html": "other file", "_redirects": "SECRET", "sub/_redirects": "ordinary", "404.html": "custom missing"} {
		p := filepath.Join(dir, name)
		if e := os.MkdirAll(filepath.Dir(p), 0755); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(p, []byte(body), 0644); e != nil {
			t.Fatal(e)
		}
	}
	h := &SiteHandler{}
	for _, tt := range []struct {
		url, body, location string
		status              int
	}{{"/faq", "plain faq", "", 200}, {"/faq/", "directory faq", "", 200}, {"/only", "", "/only/", 301}, {"/exact", "exact file", "", 200}, {"/_redirects", "custom missing", "", 404}, {"/_redirects/", "custom missing", "", 404}, {"/./_redirects", "custom missing", "", 404}, {"/%5Fredirects", "custom missing", "", 404}, {"/sub/_redirects", "ordinary", "", 200}} {
		r := httptest.NewRequest("GET", tt.url, nil)
		w := httptest.NewRecorder()
		h.serveDirFile(w, r, dir, r.URL.Path, r.URL.EscapedPath())
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
	files := map[string][]byte{"index.html": []byte("home"), "faq.html": []byte("faq"), "force.html": []byte("shadowed"), "gone.html": []byte("gone"), "404.html": []byte("missing"), "_redirects": []byte("/old /new 301\n/faq /other\n/force /new 302!\n/gone /gone.html 410!\n/missing /absent.html 404!\n/page id=:id /post/:id\n")}
	if e := disk.WriteFiles(context.Background(), "user", "site", 1, files); e != nil {
		t.Fatal(e)
	}
	if e := disk.UpdateCurrent("user", "site", 1); e != nil {
		t.Fatal(e)
	}
	h := &SiteHandler{disk: disk, limitSet: newLimitSet(config.DefaultLimits())}
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
	// Runtime equality guard includes query normalisation and rewritten public paths.
	if e := os.WriteFile(legacy, []byte("/page id=:id /:id?id=:id 301!"), 0644); e != nil {
		t.Fatal(e)
	}
	r = httptest.NewRequest("GET", "/internal/passcode/page?id=page", nil)
	w = httptest.NewRecorder()
	if h.applyRedirect(w, r, "user", "site", "page", "/page") {
		t.Fatal("self redirect")
	}
}
func TestRedirectsAfterGates(t *testing.T) {
	p := newPasscodeApp(t)
	r := p.at(t, "PUT", pcSiteDomain, "/v1/sites/trip/files?keep=1", map[string]any{"files": map[string]string{"_redirects": "/old https://example.com/secret 302!"}}, p.okey)
	if r.status != 200 {
		t.Fatal(r.status, string(r.body))
	}
	if r = p.at(t, "GET", p.host, "/old", nil, nil); r.status != 302 || r.header.Get("Location") != "https://example.com/secret" {
		t.Fatal(r.status, r.header)
	}
	if r = p.lock(t, "123456"); r.status != 200 {
		t.Fatal(r.status, string(r.body))
	}
	if r = p.at(t, "GET", p.host, "/old", nil, nil); r.header.Get("Location") != "" || strings.Contains(string(r.body), "example.com/secret") {
		t.Fatal("target leaked", r.status, r.header)
	}
	if r = p.at(t, "PATCH", pcSiteDomain, "/v1/sites/trip", map[string]bool{"offline": true}, p.okey); r.status != 200 {
		t.Fatal(r.status, string(r.body))
	}
	if r = p.at(t, "GET", p.host, "/old", nil, nil); r.status != 503 || r.header.Get("Location") != "" {
		t.Fatal(r.status, r.header)
	}
	if r = p.at(t, "PATCH", pcSiteDomain, "/v1/sites/trip", map[string]bool{"offline": false}, p.okey); r.status != 200 {
		t.Fatal(r.status, string(r.body))
	}
	uid, _ := p.userID(t, p.olive)
	if e := p.sites.disk.SetSuspended(uid, "trip", true); e != nil {
		t.Fatal(e)
	}
	if r = p.at(t, "GET", p.host, "/old", nil, nil); r.header.Get("Location") != "" || strings.Contains(string(r.body), "example.com/secret") {
		t.Fatal("take-down leaked target", r.status, r.header)
	}
	if e := p.sites.disk.SetSuspended(uid, "trip", false); e != nil {
		t.Fatal(e)
	}
	if r = p.at(t, "DELETE", pcSiteDomain, "/v1/sites/trip/lock", nil, p.okey); r.status != 200 {
		t.Fatal(r.status, string(r.body))
	}
	if r = p.grant(t, "viewer@example.com"); r.status != 200 {
		t.Fatal(r.status, string(r.body))
	}
	if r = p.at(t, "GET", p.host, "/old", nil, nil); strings.Contains(r.header.Get("Location"), "example.com/secret") || strings.Contains(string(r.body), "example.com/secret") {
		t.Fatal("named-viewer gate leaked target", r.status, r.header)
	}

}

func TestRedirectsOnAddresses(t *testing.T) {
	a, dir := newSiteApp(t, "serve")
	a.sites.SetPersonHosts("serve")
	owner := a.newPerson(t, "redirect-owner")
	_, handle := a.userID(t, owner)
	markReady(t, dir, handle)
	key := map[string]string{"X-API-Key": owner.key}
	files := map[string]string{"index.html": "home", "_redirects": "/old /new 302!", "faq.html": "faq"}
	if r := a.at(t, "POST", pcSiteDomain, "/v1/sites/blog/files", map[string]any{"files": files}, key); r.status != 201 {
		t.Fatal(r.status, string(r.body))
	}
	for _, tt := range []struct{ host, path, want string }{{"blog." + handle + "." + pcSiteDomain, "/old", "/new"}, {handle + "." + pcSiteDomain, "/blog/old", "/blog/new"}, {pcContentHost, "/internal/site-redirect/" + handle + "/blog/old", "/" + handle + "/blog/new"}} {
		r := a.at(t, "GET", tt.host, tt.path, nil, nil)
		if r.status != 302 || r.header.Get("Location") != tt.want {
			t.Fatalf("%+v: %d %v", tt, r.status, r.header)
		}
	}
	claimed := uniq("redirect") + "." + pcSiteDomain
	if r := a.at(t, "POST", pcSiteDomain, "/v1/sites/blog/domain", map[string]string{"domain": claimed}, key); r.status != 200 {
		t.Fatal(r.status, string(r.body))
	}
	if r := a.at(t, "GET", claimed, "/old", nil, nil); r.status != 302 || r.header.Get("Location") != "/new" {
		t.Fatal(r.status, r.header)
	}
	custom := uniq("redirect") + ".example.test"
	if r := a.at(t, "POST", pcSiteDomain, "/v1/sites/blog/domain", map[string]string{"domain": custom}, key); r.status != 200 {
		t.Fatal(r.status, string(r.body))
	}
	a.verify(t, a.siteID(t, owner, "blog"), custom)
	if r := a.at(t, "GET", custom, "/internal/passcode/old", nil, nil); r.status != 302 || r.header.Get("Location") != "/new" {
		t.Fatal(r.status, r.header)
	}
	if r := a.at(t, "GET", custom, "/internal/passcode/_redirects", nil, nil); r.status != 404 {
		t.Fatal(r.status, string(r.body))
	}
}
func TestRedirectsOnFamilyAndHackTeam(t *testing.T) {
	a, _, famDir, dns := newFamilyApp(t)
	owner := a.newPerson(t, "family-redirect")
	key := map[string]string{"X-API-Key": owner.key}
	files := map[string]string{"index.html": "home", "_redirects": "/old /new 301!"}
	if r := a.at(t, "POST", pcSiteDomain, "/v1/sites/voucher-shop/files", map[string]any{"files": files}, key); r.status != 201 {
		t.Fatal(r.status, string(r.body))
	}
	a.liveFamily(t, famDir, dns, owner, "redirects.fam.test", "voucher-", false, 0)
	if r := a.at(t, "GET", "shop.redirects.fam.test", "/old", nil, nil); r.status != 301 || r.header.Get("Location") != "/new" {
		t.Fatal(r.status, r.header)
	}
	b, _ := newSiteApp(t, "canonical")
	event := b.newPerson(t, "redirect-event")
	eventHandle := uniqueLabel(t, "weekend")
	setHandle(t, b, event, eventHandle)
	if r := b.at(t, "POST", pcSiteDomain, "/v1/sites/alpha/files", map[string]any{"files": files}, map[string]string{"X-API-Key": event.key}); r.status != 201 {
		t.Fatal(r.status, string(r.body))
	}
	asHackFlat(b)
	if r := b.at(t, "GET", "alpha--"+eventHandle+"."+pcSiteDomain, "/old", nil, nil); r.status != 301 || r.header.Get("Location") != "/new" {
		t.Fatal(r.status, r.header)
	}
}

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
	h := &SiteHandler{}
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
	if !siteContentExists(dir, "/_redirects/") {
		t.Error("folder not counted as content")
	}
}
