package sitemap_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"

	"github.com/vsriram/simple-host/host/sites/repo"
	"github.com/vsriram/simple-host/host/sites/sitemap"
	"github.com/vsriram/simple-host/host/sites/sitesroute/fixture"
	"github.com/vsriram/simple-host/host/sqlkit"
	"github.com/vsriram/simple-host/host/testdb"
	"github.com/vsriram/simple-host/host/web"
)

// A failing product sitemap filter answers 503 and is not cached.
func TestSitemapFilterErrorIsNotCached(t *testing.T) {
	a := fixture.New(t)
	var cache sitemap.SitemapCache
	hostedService(t)
	a.Deploy(t, a.NewPerson(t, "sitemap-filter"), "open")
	calls, fail := 0, true
	a.H.SetSitemapFilter(func(ctx context.Context, q sqlkit.Querier, sites []repo.Site) ([]repo.Site, error) {
		calls++
		if fail {
			return sites, errors.New("test sitemap filter failure")
		}
		return sites, nil
	})
	if w := serve(a, &cache, "GET"); w.Code != http.StatusServiceUnavailable || calls != 1 || len(cache.ByBrand) != 0 {
		t.Fatalf("filter error: status=%d calls=%d cached=%d", w.Code, calls, len(cache.ByBrand))
	}
	fail = false
	for range 2 {
		if w := serve(a, &cache, "GET"); w.Code != http.StatusOK || calls != 2 || len(cache.ByBrand) != 1 {
			t.Fatalf("recovered filter: status=%d calls=%d cached=%d", w.Code, calls, len(cache.ByBrand))
		}
	}
}

// The platform's sitemap query never touches a product's tables.
func TestPlatformSitemapNoProductQueries(t *testing.T) {
	a := fixture.New(t)
	a.Deploy(t, a.NewPerson(t, "sitemap-platform"), "open")
	db, recorder := testdb.OpenRecording(os.Getenv("DB_DSN"))
	t.Cleanup(func() { db.Close() })
	a.H.Database = db
	sites, err := a.H.SitesSvc().ListSitemapSites(t.Context())
	if err != nil || len(sites) == 0 {
		t.Fatalf("platform sitemap: %d sites, %v", len(sites), err)
	}
	if len(recorder.Statements()) != 1 || !recorder.Names("sites") {
		t.Fatalf("expected one platform query, got %v", recorder.Statements())
	}
	objects, err := os.ReadFile("../../../scripts/split/hack-objects.txt")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(objects), "\n") {
		fields := strings.Split(line, "|")
		if len(fields) < 2 || strings.TrimSpace(fields[0]) != "table" {
			continue
		}
		if name := strings.TrimSpace(fields[1]); recorder.Names(name) {
			t.Errorf("platform sitemap queried product table %s", name)
		}
	}
}

func hostedService(t *testing.T) {
	old := web.InstanceIsHostedService
	web.InstanceIsHostedService = true
	t.Cleanup(func() { web.InstanceIsHostedService = old })
}

// serve answers one /sitemap.xml request from cache.
func serve(a *fixture.App, cache *sitemap.SitemapCache, method string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	sitemap.Serve(a.H, cache)(w, httptest.NewRequest(method, "https://"+rt.SiteDomain+"/sitemap.xml", nil))
	return w
}
