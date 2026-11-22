package ops

import (
	"database/sql"
	"testing"
	"time"

	model "github.com/vsriram/simple-host/host/model"
)

func TestAdminSiteSortsBeforePagination(t *testing.T) {
	now := time.Now()
	rows := []AdminSiteRow{
		{Site: model.Site{ID: "a", CreatedAt: now, LastDeployedAt: sql.NullTime{Time: now.Add(-time.Hour), Valid: true}}, Usage: SiteUsage{Bytes: 100, LiveBytes: 90}, Views: 0},
		{Site: model.Site{ID: "b", CreatedAt: now.Add(-time.Hour), LastDeployedAt: sql.NullTime{Time: now, Valid: true}}, Usage: SiteUsage{Bytes: 200, LiveBytes: 10}, Views: 10},
		{Site: model.Site{ID: "c", CreatedAt: now.Add(-2 * time.Hour)}, Usage: SiteUsage{Bytes: 300, LiveBytes: 1}, Views: 20},
	}
	for _, tt := range []struct{ key, want string }{{"created", "abc"}, {"size", "cba"}, {"live", "abc"}} {
		t.Run(tt.key, func(t *testing.T) {
			copyRows := append([]AdminSiteRow(nil), rows...)
			sortAdminSites(copyRows, tt.key, false)
			for page := 0; page < 3; page++ {
				if copyRows[page].Site.ID != string(tt.want[page]) {
					t.Fatalf("page %d: got %s", page, copyRows[page].Site.ID)
				}
			}
			sortAdminSites(copyRows, tt.key, true)
			if copyRows[0].Site.ID != string(tt.want[2]) {
				t.Fatal("ascending order")
			}
		})
	}
}

func TestAdminSiteSortTiesHaveStableIDs(t *testing.T) {
	rows := []AdminSiteRow{{Site: model.Site{ID: "b"}}, {Site: model.Site{ID: "a"}}}
	for _, key := range []string{"created", "updated", "size", "views"} {
		sortAdminSites(rows, key, false)
		if rows[0].Site.ID != "a" {
			t.Fatal(key)
		}
	}
}

func TestAdminSearchMatches(t *testing.T) {
	text := "salon-demo vineetu vineetu@example.com salon.example.com"
	for _, c := range []struct {
		q    string
		want bool
	}{
		{"", true},
		{"salon", true},
		{"bakery", false},
		{"salon vineetu", true},
		{"salon bakery", false},
		{"-vineetu", false},
		{"-bakery", true},
		{"-bakery -test", true},
		{"-test -vineetu", false},
		{"salon -demo", false},
		{"salon -bakery", true},
		{"bakery -demo", false},
		{"- salon", true},
		{"--bakery", true},
	} {
		if got := adminSearchMatches(c.q, text); got != c.want {
			t.Errorf("adminSearchMatches(%q) = %v, want %v", c.q, got, c.want)
		}
	}
}
