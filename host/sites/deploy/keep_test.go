package deploy_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/config"
	model "github.com/vsriram/simple-host/host/model"
	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/sites/deploy"
	sitesrepo "github.com/vsriram/simple-host/host/sites/repo"
	"github.com/vsriram/simple-host/host/sites/sitesroute/fixture"
)

// keepSite is a person whose site blog has two versions, each with
// index.html and about.html ("version one", "version two").
func keepSite(t *testing.T) (*fixture.App, rt.Person, *model.User) {
	t.Helper()
	a := fixture.New(t)
	p := a.NewPerson(t, "keep-review")
	for i, text := range []string{"version one", "version two"} {
		r := a.Files(t, p, []string{"POST", "PUT"}[i], "blog", map[string]string{"index.html": text, "about.html": text})
		if r.Status/100 != 2 {
			t.Fatal(r.Status, string(r.Body))
		}
	}
	u, err := accountsrepo.GetUserByUsername(context.Background(), a.Database, p.Email)
	if err != nil {
		t.Fatal(err)
	}
	return a, p, &u
}

// keepRequest is a PUT ?keep=1 of blog with key, for serving on the mux directly.
func keepRequest(key string, body io.Reader) *http.Request {
	r := httptest.NewRequest("PUT", "/v1/sites/blog/files?keep=1", body)
	r.Header.Set("X-API-Key", key)
	return r
}

type keepReadSignal struct {
	io.Reader
	done chan struct{}
	sent bool
}

func (r *keepReadSignal) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if !r.sent {
		close(r.done)
		r.sent = true
	}
	return n, err
}

// A keep patch reads the live version under the site lock: a rollback that
// holds the lock first is what the patch builds on, not undone by it.
func TestKeepSnapshotUnderSiteLock(t *testing.T) {
	a, p, user := keepSite(t)
	l := config.DefaultLimits()
	l.MaxSiteTotalMB, l.MaxAccountMB = 0, 0 // no quota lock: keep=1 still has to wait
	a.H.Limits = l
	unlock := a.H.LockSite(user.ID, "blog")
	read := make(chan struct{})
	result := make(chan *httptest.ResponseRecorder)
	body := &keepReadSignal{Reader: strings.NewReader(`{"files":{"about.html":"patched"}}`), done: read}
	go func() {
		w := httptest.NewRecorder()
		a.Mux.ServeHTTP(w, keepRequest(p.Key, body))
		result <- w
	}()
	<-read
	select {
	case <-result:
		unlock()
		t.Fatal("commit bypassed site lock")
	case <-time.After(100 * time.Millisecond):
	}
	if _, err := a.Database.Exec(`UPDATE sites SET active_version=1 WHERE user_id=$1 AND name='blog'`, user.ID); err != nil {
		t.Fatal(err)
	}
	unlock()
	select {
	case w := <-result:
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("patch never finished")
	}
	site, err := sitesrepo.GetSiteByUser(context.Background(), a.Database, user.ID, "blog")
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(a.Disk.VersionDir(user.ID, "blog", 3), "index.html"))
	if site.ActiveVersion != 3 || err != nil || string(content) != "version one" {
		t.Fatal("patch undid rollback", site.ActiveVersion, err, string(content))
	}
}

// The request cap applies to the patch, not to the retained bytes, unless
// the budget says the merged version counts.
func TestKeepMergedUnpackedCaps(t *testing.T) {
	a, _, user := keepSite(t)
	dir := a.Disk.VersionDir(user.ID, "blog", 2)
	body := []byte(strings.Repeat("x", 2<<20))
	if err := os.WriteFile(filepath.Join(dir, "large.txt"), body, 0o600); err != nil {
		t.Fatal(err)
	}
	input := deploy.UpdateInput{User: user, Name: "blog", Files: map[string][]byte{"about.html": []byte("patch")}, Keep: true, Publish: true, Budget: sites.ExtractBudget(1<<20, false), Limit: 1 << 20}
	result, err := deploy.Service{Service: a.H.SitesSvc()}.Update(context.Background(), input)
	if err != nil {
		t.Fatal("request cap applied to retained bytes", err)
	}
	if retained, err := os.ReadFile(filepath.Join(a.Disk.VersionDir(user.ID, "blog", result.Version), "large.txt")); err != nil || len(retained) != len(body) {
		t.Fatal("retained file missing", err)
	}
	input.Files = map[string][]byte{"about.html": []byte("patch")}
	input.Budget = sites.ExtractBudget(1<<20, true)
	var siteErr *sites.Error
	if _, err = (deploy.Service{Service: a.H.SitesSvc()}).Update(context.Background(), input); !errors.As(err, &siteErr) || siteErr.Code != "patch_too_large" {
		t.Fatal("override ignored", err)
	}
}

// An unreadable live file: removing it still works, keeping it fails
// cleanly rather than publishing a version without it.
func TestKeepUnreadableRegularFile(t *testing.T) {
	for _, action := range []string{"remove", "retain"} {
		t.Run(action, func(t *testing.T) {
			a, p, user := keepSite(t)
			path := filepath.Join(a.Disk.VersionDir(user.ID, "blog", 2), "about.html")
			if err := os.Chmod(path, 0); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(path, 0o644) })
			if _, err := os.ReadFile(path); err == nil {
				t.Skip("file permissions do not restrict this user")
			}
			body := map[string]any{"files": map[string]string{"index.html": "patched"}}
			if action == "remove" {
				body["remove_files"] = []string{"about.html"}
			}
			r := a.API(t, p, "PUT", "/v1/sites/blog/files?keep=1", body)
			if action == "retain" {
				if r.Status != 400 {
					t.Fatalf("unreadable retained bytes: %d %s", r.Status, r.Body)
				}
				return
			}
			if r.Status != 200 {
				t.Fatalf("remove unreadable file: %d %s", r.Status, r.Body)
			}
			dir := a.Disk.VersionDir(user.ID, "blog", 3)
			if index, err := os.ReadFile(filepath.Join(dir, "index.html")); err != nil || string(index) != "patched" {
				t.Fatalf("patch contents: %q %v", index, err)
			}
			if _, err := os.Stat(filepath.Join(dir, "about.html")); !os.IsNotExist(err) {
				t.Fatalf("removed file: %v", err)
			}
		})
	}
}

// Ignored entries (credentials, metadata) never reach the merged version,
// and a patch of nothing else leaves the files as they were.
func TestKeepSanitizedPatch(t *testing.T) {
	for _, tc := range []struct {
		name, wantIndex string
		patch           map[string]string
	}{
		{"ignored credentials", "patched", map[string]string{"./index.html": "patched", ".git/hooks/post-checkout.sh": "ignored", ".env": "ignored", "._legacy.php": "ignored"}},
		{"metadata only", "version two", map[string]string{".DS_Store": "ignored"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, p, user := keepSite(t)
			if r := a.API(t, p, "PUT", "/v1/sites/blog/files?keep=1", map[string]any{"files": tc.patch}); r.Status != 200 {
				t.Fatal(r.Status, string(r.Body))
			}
			site, err := sitesrepo.GetSiteByUser(context.Background(), a.Database, user.ID, "blog")
			if err != nil {
				t.Fatal(err)
			}
			dir := a.Disk.VersionDir(user.ID, "blog", site.ActiveVersion)
			for name, want := range map[string]string{"index.html": tc.wantIndex, "about.html": "version two"} {
				if body, err := os.ReadFile(filepath.Join(dir, name)); err != nil || string(body) != want {
					t.Fatal(name, string(body), err)
				}
			}
			if entries, err := os.ReadDir(dir); err != nil || len(entries) != 2 {
				t.Fatal("discarded files reached merged version:", entries, err)
			}
		})
	}
}
