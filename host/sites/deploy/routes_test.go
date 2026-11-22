package deploy_test

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/sites/deploy"
	"github.com/vsriram/simple-host/host/sites/sitesroute/fixture"
)

// B3: the expiry sweep keeps a taken-down site, as the purge does.
func TestExpirySweepKeepsTakenDownSite(t *testing.T) {
	a := fixture.New(t)
	owner := a.NewPerson(t, "expiry-held")
	a.Deploy(t, owner, "held")
	if _, err := a.Database.Exec(`UPDATE sites SET expires_at = now() - interval '1 hour', suspended_at = now() WHERE name = 'held' AND user_id = $1`, owner.ID); err != nil {
		t.Fatal(err)
	}
	deploy.Service{Service: a.H.SitesSvc()}.SweepExpiredSites(context.Background())
	var n int
	if err := a.Database.QueryRow(`SELECT count(*) FROM sites WHERE name = 'held' AND user_id = $1`, owner.ID).Scan(&n); err != nil || n != 1 {
		t.Fatal("taken-down site swept", n, err)
	}
}

// MAX_SITES_OVERRIDES replaces the per-account cap for the listed handles
// only, and follows an account's earlier handles.
func TestSiteOverridesEnforced(t *testing.T) {
	a := fixture.New(t)
	olive, oscar := a.NewPerson(t, "olive"), a.NewPerson(t, "oscar")
	a.SetLimits(t, map[string]string{"MAX_SITES_PER_ACCOUNT": "2", "MAX_SITES_OVERRIDES": olive.Handle + ":4"})
	fill := func(uid string, n int) {
		t.Helper()
		if _, err := a.Database.Exec(`INSERT INTO sites (user_id, name, deleted_at)
			SELECT $1, 'gone-' || g, now() FROM generate_series(1, $2) g`, uid, n); err != nil {
			t.Fatal(err)
		}
	}
	create := func(p rt.Person, name string) rt.Resp {
		return a.Files(t, p, "POST", name, map[string]string{"index.html": "x"})
	}
	quota := func(r rt.Resp) bool { return r.Status == http.StatusForbidden && r.Code() == "site_quota_reached" }

	fill(oscar.ID, 2)
	if r := create(oscar, "fresh"); !quota(r) {
		t.Fatalf("oscar over the global cap: %d %s", r.Status, r.Body)
	}
	// A handle change keeps the override (the old handle stays an alias).
	if _, err := a.Database.Exec(`INSERT INTO handle_aliases (handle, user_id) VALUES ($1, $2)`, olive.Handle, olive.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Database.Exec(`UPDATE users SET handle = $1 WHERE id = $2`, olive.Handle+"-new", olive.ID); err != nil {
		t.Fatal(err)
	}
	fill(olive.ID, 2)
	if r := create(olive, "third"); r.Status != http.StatusCreated {
		t.Fatalf("olive under her override: %d %s", r.Status, r.Body)
	}
	if _, err := a.Database.Exec(`INSERT INTO sites (user_id, name, deleted_at) VALUES ($1, 'gone-x', now())`, olive.ID); err != nil {
		t.Fatal(err)
	}
	if r := create(olive, "fifth"); !quota(r) || !strings.Contains(string(r.Body), "4") {
		t.Fatalf("olive over her override: %d %s", r.Status, r.Body)
	}
}

// Without an override the unpacked files stay under the instance's own
// extractor caps; only an override narrows them.
func TestArchiveNoOverrideKeepsInstanceUnpackCaps(t *testing.T) {
	a := fixture.New(t)
	olive, oscar := a.NewPerson(t, "olive"), a.NewPerson(t, "oscar")
	restore := sites.Upload.Snapshot()
	sites.Upload.Body = 1 << 20 // the body cap alone, as if Upload.Configure never ran
	t.Cleanup(restore)
	a.SetLimits(t, map[string]string{"MAX_ARCHIVE_MB_OVERRIDES": olive.Handle + ":1"})
	body := fixture.TarGzOf(t, 3<<20/2) // a few KB compressed, 1.5 MB unpacked
	if r := a.At(t, "POST", rt.SiteDomain, "/v1/sites/wide", body, oscar.Header()); r.Status != http.StatusCreated {
		t.Fatalf("no override: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "POST", rt.SiteDomain, "/v1/sites/wide", body, olive.Header()); r.Status != http.StatusRequestEntityTooLarge {
		t.Fatalf("override of 1 MB: %d %s", r.Status, r.Body)
	}
	if sites.StatedSiteMB(900<<20) != 500 {
		t.Fatal("a limit above the extractor's ceiling is reported as it is")
	}
}

// MAX_SITE_TOTAL_MB counts the live copy and kept versions on every deploy
// path, published or not.
func TestStorageCapsDeployPaths(t *testing.T) {
	a := fixture.New(t)
	p := a.NewPerson(t, "capper")
	fixture.WithKeepVersions(t, 4)
	a.SetLimits(t, map[string]string{"MAX_SITE_TOTAL_MB": "2", "SITE_TOTAL_CAP_FROM": "2026-01-01T00:00:00Z", "MAX_ACCOUNT_MB": "10"})
	payload := strings.Repeat("x", 1100<<10) // 2.2 MB with the live copy
	zipped := fixture.ZipOf(t, map[string][]byte{"index.html": []byte(payload)})
	jsonBody := map[string]any{"files": map[string]string{"index.html": payload}}
	totalTooLarge := func(r rt.Resp) bool { return r.Status == 413 && r.Code() == "site_total_too_large" }
	for _, tc := range []struct {
		method, path string
		body         any
	}{{"POST", "/v1/sites/json-post/files", jsonBody}, {"PUT", "/v1/sites/archive-put?create=1", zipped}} {
		if r := a.At(t, tc.method, rt.SiteDomain, tc.path, tc.body, p.Header()); !totalTooLarge(r) {
			t.Fatalf("%s: %d %s", tc.path, r.Status, r.Body)
		}
	}
	a.Deploy(t, p, "small")
	if r := a.API(t, p, "PUT", "/v1/sites/small/files", jsonBody); !totalTooLarge(r) {
		t.Fatalf("update: %d %s", r.Status, r.Body)
	}
	// Unpublished: the tiny live copy plus 1.1 MB fits once, not twice.
	if r := a.API(t, p, "PUT", "/v1/sites/small/files?publish=false", jsonBody); r.Status != 200 {
		t.Fatalf("first unpublished: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "PUT", rt.SiteDomain, "/v1/sites/small?publish=false", zipped, p.Header()); !totalTooLarge(r) {
		t.Fatalf("second unpublished: %d %s", r.Status, r.Body)
	}
}

// The account cap is checked under the account's lock: two different sites
// deployed at once cannot both pass it (site locks are per account).
func TestAccountCapSerializesDifferentSites(t *testing.T) {
	a := fixture.New(t)
	p := a.NewPerson(t, "concurrentcap")
	a.SetLimits(t, map[string]string{"MAX_ACCOUNT_MB": "1"})
	start := make(chan struct{})
	results := make(chan int, 2)
	for _, name := range []string{"one", "two"} {
		go func(name string) {
			<-start
			results <- a.Files(t, p, "POST", name, map[string]string{"index.html": strings.Repeat("x", 300<<10)}).Status
		}(name)
	}
	close(start)
	got := map[int]int{}
	for i := 0; i < 2; i++ {
		got[<-results]++
	}
	if got[201] != 1 || got[413] != 1 {
		t.Fatalf("concurrent deploys: %v", got)
	}
}

// A photo over the account's storage is refused and the live version stays.
func TestPhotosQuotaKeepsLiveVersion(t *testing.T) {
	a := fixture.New(t)
	p := a.NewPerson(t, "photoquota")
	if r := a.Files(t, p, "POST", "home", map[string]string{"index.html": strings.Repeat("x", 350000)}); r.Status != 201 {
		t.Fatalf("setup: %d %s", r.Status, r.Body)
	}
	a.SetLimits(t, map[string]string{"MAX_ACCOUNT_MB": "1"})
	if r := addPhotos(t, a, p.Key, "home", "front.jpg", photoJPEG(t, 8, 6)); r.Status != 413 {
		t.Fatalf("quota: %d %s", r.Status, r.Body)
	}
	var active int
	if err := a.Database.QueryRow(`SELECT active_version FROM sites WHERE id = $1`, a.SiteID(t, p, "home")).Scan(&active); err != nil || active != 1 {
		t.Fatalf("live version after refusal: %d %v", active, err)
	}
}

// A file where images/ or the photo's path would go is a conflict (409),
// and the file is kept as it was.
func TestPhotoAncestorConflictKeepsFile(t *testing.T) {
	for _, path := range []string{"images", "images/front.jpg/child"} {
		t.Run(path, func(t *testing.T) {
			a := fixture.New(t)
			p := a.NewPerson(t, "photoconflict")
			a.Deploy(t, p, "home")
			dest := filepath.Join(a.Disk.VersionDir(p.ID, "home", 1), path)
			if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(dest, []byte("keep"), 0o644); err != nil {
				t.Fatal(err)
			}
			if r := addPhotos(t, a, p.Key, "home", "front.jpg", photoJPEG(t, 8, 6)); r.Status != 409 {
				t.Fatalf("conflict: %d %s", r.Status, r.Body)
			}
			if got, err := os.ReadFile(dest); err != nil || string(got) != "keep" {
				t.Fatal("conflicting file changed")
			}
		})
	}
}
