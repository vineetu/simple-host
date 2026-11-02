package db

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	_ "github.com/lib/pq"
)

// An owner row lands in Split.Owner, not in People.
func TestSplitAddOwner(t *testing.T) {
	var s Split
	s.add(ClassOwner, 4, 2)
	s.add(ClassPerson, 1, 1)
	if s.Owner != (Counts{Views: 4, Visitors: 2}) || s.Person != (Counts{Views: 1, Visitors: 1}) {
		t.Fatalf("Split after owner+person: %+v", s)
	}
}

// PersonViewsBySite: people only, rolling 7 and 30 day windows; owner and
// bot rows and views older than 30 days are left out. Needs DB_DSN.
func TestPersonViewsBySite(t *testing.T) {
	dsn := os.Getenv("DB_DSN")
	if dsn == "" {
		t.Skip("DB_DSN unset")
	}
	database, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	if err := database.Ping(); err != nil {
		t.Skipf("postgres not reachable: %v", err)
	}
	ctx := context.Background()
	stamp := time.Now().Format("20060102150405.000000000")
	u, err := CreateUser(ctx, database, "views-"+stamp+"@example.com", "shk_views_"+stamp, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = database.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, u.ID) })
	other, err := CreateUser(ctx, database, "views-other-"+stamp+"@example.com", "shk_views_other_"+stamp, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = database.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, other.ID) })
	site, err := CreateSite(ctx, database, u.ID, "shop", "https://example.test/shop/", nil)
	if err != nil {
		t.Fatal(err)
	}
	quiet, err := CreateSite(ctx, database, u.ID, "quiet", "https://example.test/quiet/", nil)
	if err != nil {
		t.Fatal(err)
	}
	theirs, err := CreateSite(ctx, database, other.ID, "theirs", "https://example.test/theirs/", nil)
	if err != nil {
		t.Fatal(err)
	}

	add := func(siteID, class string, agoHours int, views int64) {
		t.Helper()
		if _, err := database.ExecContext(ctx, `INSERT INTO site_view_hourly (site_id, hour, class, views)
			VALUES ($1, date_trunc('hour', now()) - make_interval(hours => $2), $3, $4)
			ON CONFLICT (site_id, hour, class) DO UPDATE SET views = site_view_hourly.views + EXCLUDED.views`,
			siteID, agoHours, class, views); err != nil {
			t.Fatal(err)
		}
	}
	add(site.ID, ClassPerson, 2, 5)        // inside 7 days
	add(site.ID, ClassPerson, 10*24, 7)    // between 7 and 30 days
	add(site.ID, ClassPerson, 40*24, 100)  // older than 30 days
	add(site.ID, ClassOwner, 3, 50)        // the owner's own, inside 7 days
	add(site.ID, ClassBot, 4, 60)          // a bot, inside 7 days
	add(theirs.ID, ClassPerson, 5, 9)      // another account's site
	add(theirs.ID, ClassPerson, 20*24, 11) // another account's site, 7-30 days

	got, err := PersonViewsBySite(ctx, database, u.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if want := (SiteViews{People7Days: 5, People30Days: 12}); got[site.ID] != want {
		t.Errorf("shop: %+v, want %+v", got[site.ID], want)
	}
	if _, ok := got[quiet.ID]; ok {
		t.Errorf("quiet site has a row: %+v", got[quiet.ID])
	}
	if _, ok := got[theirs.ID]; ok {
		t.Errorf("another account's site in the owner's map: %+v", got[theirs.ID])
	}

	all, err := PersonViewsBySite(ctx, database, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if want := (SiteViews{People7Days: 9, People30Days: 20}); all[theirs.ID] != want {
		t.Errorf("all sites, theirs: %+v, want %+v", all[theirs.ID], want)
	}
	if want := (SiteViews{People7Days: 5, People30Days: 12}); all[site.ID] != want {
		t.Errorf("all sites, shop: %+v, want %+v", all[site.ID], want)
	}
}
