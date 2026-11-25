package manage_test

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"simplehost/core/testkit"
	"simplehost/core/web"
	"simplehost/sites/manage"
	"simplehost/sites/serve"
	"simplehost/sites/shortnames"
)

// SPEC test 12 (F8): roll back to an older version. Every address serves it.
// Old versions beyond the count are gone.
func TestRollback(t *testing.T) {
	k := testkit.Start(t, serve.Mount)
	b := k.Browser()
	bob := k.Person(t, "bob")
	for i := 1; i <= 6; i++ {
		k.Publish(t, bob, "blog", map[string]string{"index.html": fmt.Sprintf("v%d", i)})
	}
	r := web.WithCaller(httptest.NewRequest("PATCH", "/", nil), web.Caller{ID: bob.ID})
	site, _ := k.App.Sites.Get(context.Background(), bob.ID, "blog")
	if _, err := shortnames.Claim(r, k.App, site, "bobsblog"); err != nil {
		t.Fatal(err)
	}
	if err := manage.Rollback(r, k.App, site, 3); err != nil {
		t.Fatal(err)
	}
	// Over HTTPS the main address sends pages to the short name; plain HTTP shows it serving too.
	for _, url := range []string{"http://blog--bob.simple-host.site/", "https://bobsblog.simple-host.app/"} {
		if res := testkit.Call(t, b, "GET", url, nil); res.Status != 200 || res.Body != "v3" {
			t.Fatalf("%s: %d %q, want v3", url, res.Status, res.Body)
		}
	}
	err := manage.Rollback(r, k.App, site, 2)
	if err == nil || !strings.Contains(err.Error(), "no_version") {
		t.Fatalf("rollback to a removed version: %v", err)
	}
	var kept int
	k.App.DB.QueryRow(`SELECT count(*) FROM versions WHERE site_id = $1`, site.ID).Scan(&kept)
	if _, err := os.Stat(k.App.Sites.VersionDir(site.ID, 2)); kept != 4 || !os.IsNotExist(err) {
		t.Fatalf("kept %d versions, v2 on disk: %v", kept, err)
	}
	olga := k.Person(t, "olga")
	if err := manage.Rollback(web.WithCaller(httptest.NewRequest("PATCH", "/", nil), web.Caller{ID: olga.ID}), k.App, site, 4); err == nil {
		t.Fatal("someone else rolled the site back")
	}
}

// SPEC test 13 (F9): delete to the bin and restore with data. Offline shows the offline page.
func TestBinAndOffline(t *testing.T) {
	k := testkit.Start(t, serve.Mount, manage.Mount)
	b := k.Browser()
	bob := k.Person(t, "bob")
	k.Publish(t, bob, "shop", map[string]string{"index.html": "open"})
	ctx := context.Background()
	site, _ := k.App.Sites.Get(ctx, bob.ID, "shop")
	r := web.WithCaller(httptest.NewRequest("PATCH", "/", nil), web.Caller{ID: bob.ID})
	if err := k.App.Data.Create(ctx, site, "orders"); err != nil {
		t.Fatal(err)
	}
	ns, _ := k.App.Data.Open(r, site, "orders")
	ns.Exec(ctx, `INSERT INTO _values (key, value) VALUES ('total', '42')`)
	ns.Close()

	key := []string{"X-API-Key", bob.Key}
	if res := testkit.Call(t, b, "DELETE", "https://simple-host.app/v1/sites/shop", nil, key...); res.Status != 200 {
		t.Fatalf("delete: %d %s", res.Status, res.Body)
	}
	if res := testkit.Call(t, b, "GET", "https://shop--bob.simple-host.site/", nil); res.Status != 404 {
		t.Fatalf("deleted site answers: %d", res.Status)
	}
	if res := testkit.Call(t, b, "GET", "https://simple-host.app/v1/sites?bin=true", nil, key...); !strings.Contains(res.Body, `"name":"shop"`) {
		t.Fatalf("bin: %s", res.Body)
	}
	site, err := manage.Restore(r, k.App, bob.ID, "shop")
	if err != nil {
		t.Fatal(err)
	}
	if res := testkit.Call(t, b, "GET", "https://shop--bob.simple-host.site/", nil); res.Status != 200 || res.Body != "open" {
		t.Fatalf("restored: %d %q", res.Status, res.Body)
	}
	ns, err = k.App.Data.Open(r, site, "orders")
	if err != nil {
		t.Fatal(err)
	}
	rows, err := ns.Query(ctx, `SELECT value FROM _values WHERE key = 'total'`)
	ns.Close()
	if err != nil || len(rows.Rows) != 1 || rows.Rows[0][0] != "42" {
		t.Fatalf("saved data after restore: %v %v", rows, err)
	}
	if res := testkit.Call(t, b, "GET", "https://simple-host.app/v1/sites?q=sho", nil, key...); !strings.Contains(res.Body, `"url":"https://shop--bob.simple-host.site/"`) {
		t.Fatalf("list: %s", res.Body)
	}

	if err := manage.SetOffline(r, k.App, site, true); err != nil {
		t.Fatal(err)
	}
	if res := testkit.Call(t, b, "GET", "https://shop--bob.simple-host.site/", nil); res.Status != 503 || !strings.Contains(res.Body, "This site is offline") {
		t.Fatalf("offline: %d %q", res.Status, res.Body)
	}
	manage.SetOffline(r, k.App, site, false)
	if res := testkit.Call(t, b, "GET", "https://shop--bob.simple-host.site/", nil); res.Status != 200 {
		t.Fatalf("back online: %d", res.Status)
	}
}
