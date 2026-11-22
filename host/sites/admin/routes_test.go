package admin_test

import (
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/sites/admin"
	"github.com/vsriram/simple-host/host/sites/sitesroute/fixture"
)

// The admin site list spans accounts: it sorts by size (kept versions count)
// and by recent human views, searches, and is refused to everyone else.
func TestAdminSitesAPI(t *testing.T) {
	a := fixture.New(t)
	admin.Register(a.Mux, a.H, a.Auth, a.SiteOps)
	owner := a.NewPerson(t, "sort-sites")
	for i, name := range []string{"old", "middle", "new"} {
		r := a.Files(t, owner, "POST", name, map[string]string{"index.html": strings.Repeat("x", []int{1000, 200, 100}[i])})
		if r.Status != 201 {
			t.Fatalf("deploy: %d", r.Status)
		}
		id := r.JSON(t)["id"].(string)
		if _, err := a.Database.Exec(`UPDATE sites SET created_at=now()-($1 * interval '1 day') WHERE id=$2`, 3-i, id); err != nil {
			t.Fatal(err)
		}
		for _, v := range []struct {
			class   string
			days, n int
		}{{"person", 1, i * 10}, {"bot", 1, 9999}, {"person", 31, 9999}} {
			if _, err := a.Database.Exec(`INSERT INTO site_view_hourly(site_id,hour,class,views) VALUES($1,date_trunc('hour',now()-($2 * interval '1 day')),$3,$4)`, id, v.days, v.class, v.n); err != nil {
				t.Fatal(err)
			}
		}
	}
	// An extra kept upload changes the footprint, while the live copy gets smaller.
	if r := a.Files(t, owner, "PUT", "old", map[string]string{"index.html": "x"}); r.Status != 200 {
		t.Fatalf("update: %d", r.Status)
	}
	names := func(q string) ([]string, []any) {
		t.Helper()
		r := a.AsAdmin(t, "GET", "/v1/admin/sites?"+q, nil)
		if r.Status != 200 {
			t.Fatalf("list %s: %d", q, r.Status)
		}
		rows := r.JSON(t)["sites"].([]any)
		var got []string
		for _, row := range rows {
			got = append(got, row.(map[string]any)["name"].(string))
		}
		return got, rows
	}
	// A reused test database keeps other runs' owners: search this run's handle.
	if got, rows := names("sort=views&q=" + owner.Handle); !slices.Equal(got, []string{"new", "middle", "old"}) || rows[0].(map[string]any)["views"] != float64(20) {
		t.Fatalf("by views (bots and old views excluded): %v %v", got, rows)
	}
	if got, rows := names("sort=size&q=" + owner.Handle); !slices.Equal(got, []string{"old", "middle", "new"}) || rows[0].(map[string]any)["disk"].(float64) <= rows[0].(map[string]any)["size"].(float64) {
		t.Fatalf("by size (kept version in disk): %v %v", got, rows)
	}
	if got, _ := names("sort=created&q=" + url.QueryEscape(owner.Handle+" -old")); !slices.Equal(got, []string{"new", "middle"}) {
		t.Fatalf("search: %v", got)
	}
	if r := a.AsAdmin(t, "GET", "/v1/admin/sites?sort=invalid", nil); r.Status != 400 {
		t.Fatal("invalid query accepted")
	}
	if r := a.API(t, owner, "GET", "/v1/admin/sites", nil); r.Status != 404 {
		t.Fatal("non-admin access")
	}
}
