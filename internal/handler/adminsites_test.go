package handler

import (
	"database/sql"
	"fmt"
	"github.com/vsriram/simple-host/internal/capacity"
	"github.com/vsriram/simple-host/internal/db"
	"strings"
	"testing"
	"time"
)

func TestAdminSiteSortsBeforePagination(t *testing.T) {
	now := time.Now()
	rows := []adminSiteEntry{
		{site: db.Site{ID: "a", CreatedAt: now, LastDeployedAt: sql.NullTime{Time: now.Add(-time.Hour), Valid: true}}, size: capacity.SiteUsage{Bytes: 100, LiveBytes: 90}, views: 0},
		{site: db.Site{ID: "b", CreatedAt: now.Add(-time.Hour), LastDeployedAt: sql.NullTime{Time: now, Valid: true}}, size: capacity.SiteUsage{Bytes: 200, LiveBytes: 10}, views: 10},
		{site: db.Site{ID: "c", CreatedAt: now.Add(-2 * time.Hour)}, size: capacity.SiteUsage{Bytes: 300, LiveBytes: 1}, views: 20},
	}
	for _, tt := range []struct{ key, want string }{{"created", "abc"}, {"updated", "bac"}, {"size", "cba"}, {"views", "cba"}, {"live", "abc"}} {
		t.Run(tt.key, func(t *testing.T) {
			copyRows := append([]adminSiteEntry(nil), rows...)
			sortAdminSites(copyRows, tt.key, false)
			for page := 0; page < 3; page++ {
				if copyRows[page].site.ID != string(tt.want[page]) {
					t.Fatalf("page %d: got %s", page, copyRows[page].site.ID)
				}
			}
			sortAdminSites(copyRows, tt.key, true)
			if copyRows[0].site.ID != string(tt.want[2]) {
				t.Fatal("ascending order")
			}
		})
	}
}

func TestAdminSiteSortTiesHaveStableIDs(t *testing.T) {
	rows := []adminSiteEntry{{site: db.Site{ID: "b"}}, {site: db.Site{ID: "a"}}}
	for _, key := range []string{"created", "updated", "size", "views"} {
		sortAdminSites(rows, key, false)
		if rows[0].site.ID != "a" {
			t.Fatal(key)
		}
	}
}

func TestAdminSitesAPI(t *testing.T) {
	a := newConnectorApp(t)
	owner := a.newPerson(t, "sort-sites")
	headers := map[string]string{"X-API-Key": owner.key}
	ids := map[string]string{}
	for i, name := range []string{"old", "middle", "new"} {
		body := jsonBody(map[string]any{"files": map[string]string{"index.html": strings.Repeat("x", []int{1000, 200, 100}[i])}})
		r := a.do(t, "POST", "/v1/sites/"+name+"/files", body, headers)
		if r.status != 201 {
			t.Fatalf("deploy: %d", r.status)
		}
		id := r.json(t)["id"].(string)
		ids[name] = id
		_, err := a.database.Exec(`UPDATE sites SET created_at=now()-($1 * interval '1 day') WHERE id=$2`, 3-i, id)
		if err != nil {
			t.Fatal(err)
		}
		_, err = a.database.Exec(`UPDATE versions SET created_at=now()-($1 * interval '1 day') WHERE site_id=$2`, i, id)
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range []struct {
			class   string
			days, n int
		}{{"person", 1, i * 10}, {"bot", 1, 9999}, {"person", 31, 9999}} {
			_, err = a.database.Exec(`INSERT INTO site_view_hourly(site_id,hour,class,views) VALUES($1,date_trunc('hour',now()-($2 * interval '1 day')),$3,$4)`, id, v.days, v.class, v.n)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	// An extra kept upload changes the footprint, while the live copy gets smaller.
	r := a.do(t, "PUT", "/v1/sites/old/files", jsonBody(map[string]any{"files": map[string]string{"index.html": "x"}}), headers)
	if r.status != 200 {
		t.Fatalf("update: %d", r.status)
	}
	admin := map[string]string{"X-API-Key": a.admin}
	for _, tt := range []struct {
		sort  string
		names []string
	}{{"created", []string{"new", "middle", "old"}}, {"updated", []string{"old", "middle", "new"}}, {"size", []string{"old", "middle", "new"}}, {"views", []string{"new", "middle", "old"}}} {
		t.Run(tt.sort, func(t *testing.T) {
			for page, name := range tt.names {
				r := a.do(t, "GET", fmt.Sprintf("/v1/admin/sites?sort=%s&limit=1&page=%d&q=sort-sites", tt.sort, page), nil, admin)
				if r.status != 200 {
					t.Fatalf("list: %d", r.status)
				}
				d := r.json(t)
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
	for _, path := range []string{"?sort=invalid", "?page=-1", "?limit=201"} {
		if r := a.do(t, "GET", "/v1/admin/sites"+path, nil, admin); r.status != 400 {
			t.Fatal("invalid query accepted")
		}
	}
	if r := a.do(t, "GET", "/v1/admin/sites", nil, headers); r.status != 404 {
		t.Fatal("non-admin access")
	}
}
