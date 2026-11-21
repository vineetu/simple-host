package sitestest

import (
	"fmt"
	"github.com/vsriram/simple-host/host/server/servertest"
	"net/url"
	"slices"
	"strings"
	"testing"
)

func TestAdminSitesAPI(t *testing.T) {
	a := servertest.NewConnectorApp(t)
	owner := a.NewPerson(t, "sort-sites")
	headers := map[string]string{"X-API-Key": owner.Key}
	ids := map[string]string{}
	for i, name := range []string{"old", "middle", "new"} {
		body := servertest.JSONBody(map[string]any{"files": map[string]string{"index.html": strings.Repeat("x", []int{1000, 200, 100}[i])}})
		r := a.Do(t, "POST", "/v1/sites/"+name+"/files", body, headers)
		if r.Status != 201 {
			t.Fatalf("deploy: %d", r.Status)
		}
		id := r.JSON(t)["id"].(string)
		ids[name] = id
		_, err := a.Database.Exec(`UPDATE sites SET created_at=now()-($1 * interval '1 day') WHERE id=$2`, 3-i, id)
		if err != nil {
			t.Fatal(err)
		}
		_, err = a.Database.Exec(`UPDATE versions SET created_at=now()-($1 * interval '1 day') WHERE site_id=$2`, i, id)
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range []struct {
			class   string
			days, n int
		}{{"person", 1, i * 10}, {"bot", 1, 9999}, {"person", 31, 9999}} {
			_, err = a.Database.Exec(`INSERT INTO site_view_hourly(site_id,hour,class,views) VALUES($1,date_trunc('hour',now()-($2 * interval '1 day')),$3,$4)`, id, v.days, v.class, v.n)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	// An extra kept upload changes the footprint, while the live copy gets smaller.
	r := a.Do(t, "PUT", "/v1/sites/old/files", servertest.JSONBody(map[string]any{"files": map[string]string{"index.html": "x"}}), headers)
	if r.Status != 200 {
		t.Fatalf("update: %d", r.Status)
	}
	admin := map[string]string{"X-API-Key": a.Admin}
	// The admin list spans every account, so searches name this run's own
	// handle: a reused test database keeps other runs' sort-sites owners.
	handle := strings.Split(owner.Email, "@")[0]
	for _, tt := range []struct {
		sort  string
		names []string
	}{{"created", []string{"new", "middle", "old"}}, {"updated", []string{"old", "middle", "new"}}, {"size", []string{"old", "middle", "new"}}, {"views", []string{"new", "middle", "old"}}} {
		t.Run(tt.sort, func(t *testing.T) {
			for page, name := range tt.names {
				r := a.Do(t, "GET", fmt.Sprintf("/v1/admin/sites?sort=%s&limit=1&page=%d&q=%s", tt.sort, page, handle), nil, admin)
				if r.Status != 200 {
					t.Fatalf("list: %d", r.Status)
				}
				d := r.JSON(t)
				rows := d["sites"].([]any)
				if d["total"] != float64(3) || len(rows) != 1 {
					t.Fatalf("pagination: %v", d)
				}
				row := rows[0].(map[string]any)
				if row["name"] != name {
					t.Fatalf("page %d got %v want %s", page, row["name"], name)
				}
				if row["views"] != float64(map[string]int{"old": 0, "middle": 10, "new": 20}[name]) {
					t.Fatal("bot or old views included")
				}
				if name == "old" && row["disk"].(float64) <= row["size"].(float64) {
					t.Fatal("kept version missing from size")
				}
			}
		})
	}
	for q, want := range map[string][]string{" -old": {"new", "middle"}, " mid": {"middle"}, " -old -new": {"middle"}, " -": {"new", "middle", "old"}} {
		r := a.Do(t, "GET", "/v1/admin/sites?sort=created&q="+url.QueryEscape(handle+q), nil, admin)
		if r.Status != 200 {
			t.Fatalf("search %q: %d", q, r.Status)
		}
		var got []string
		for _, row := range r.JSON(t)["sites"].([]any) {
			got = append(got, row.(map[string]any)["name"].(string))
		}
		if !slices.Equal(got, want) {
			t.Fatalf("search %q got %v want %v", q, got, want)
		}
	}
	for _, path := range []string{"?sort=invalid", "?page=-1"} {
		if r := a.Do(t, "GET", "/v1/admin/sites"+path, nil, admin); r.Status != 400 {
			t.Fatal("invalid query accepted")
		}
	}
	if r := a.Do(t, "GET", "/v1/admin/sites", nil, headers); r.Status != 404 {
		t.Fatal("non-admin access")
	}
}
