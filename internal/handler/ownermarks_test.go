package handler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/internal/app/operations"
	model "github.com/vsriram/simple-host/internal/model"
	repo "github.com/vsriram/simple-host/internal/repository/operations"
	sitesrepo "github.com/vsriram/simple-host/internal/repository/sites"
)

// Owner visit marks and the site list's people-only view counts (SIM-151).
// Needs DB_DSN with owner_visitor_marks applied.

// Every item of GET /v1/sites carries views: people over the rolling 7 and
// 30 days, zeros when nothing, the owner's own visits and bots left out.
func TestListSitesViews(t *testing.T) {
	a := newPrivateApp(t)
	olive := a.newPerson(t, "olive")
	a.deploy(t, olive, "shop")
	a.deploy(t, olive, "quiet")
	shop := a.siteID(t, olive, "shop")
	add := func(class string, agoHours int, views int64) {
		t.Helper()
		if _, err := a.database.Exec(`INSERT INTO site_view_hourly (site_id, hour, class, views)
			VALUES ($1, date_trunc('hour', now()) - make_interval(hours => $2), $3, $4)
			ON CONFLICT (site_id, hour, class) DO UPDATE SET views = site_view_hourly.views + EXCLUDED.views`,
			shop, agoHours, class, views); err != nil {
			t.Fatal(err)
		}
	}
	add(repo.ClassPerson, 1, 3)
	add(repo.ClassPerson, 12*24, 4)
	add(repo.ClassPerson, 45*24, 500)
	add(repo.ClassOwner, 2, 40)
	add(repo.ClassBot, 2, 60)

	// Without the analytics log nothing was measured, so views is left out.
	if r := a.at(t, "GET", "simple-host.test", "/v1/sites", nil, map[string]string{"X-API-Key": olive.key}); r.status != 200 || strings.Contains(string(r.body), `"views"`) {
		t.Fatalf("views without the analytics log: %d %s", r.status, r.body)
	}
	a.sites.SetTrafficLog(true)
	r := a.at(t, "GET", "simple-host.test", "/v1/sites", nil, map[string]string{"X-API-Key": olive.key})
	if r.status != http.StatusOK {
		t.Fatalf("list: %d %s", r.status, r.body)
	}
	var items []struct {
		Name  string                `json:"name"`
		Views *operations.SiteViews `json:"views"`
	}
	if err := json.Unmarshal(r.body, &items); err != nil {
		t.Fatal(err)
	}
	seen := map[string]operations.SiteViews{}
	for _, it := range items {
		if it.Views == nil {
			t.Fatalf("%s: no views field in %s", it.Name, r.body)
		}
		seen[it.Name] = *it.Views
	}
	if want := (operations.SiteViews{People7Days: 3, People30Days: 7}); seen["shop"] != want {
		t.Errorf("shop views %+v, want %+v", seen["shop"], want)
	}
	if want := (operations.SiteViews{}); seen["quiet"] != want {
		t.Errorf("quiet views %+v, want zeros", seen["quiet"])
	}

	// The admin's all-accounts list carries the same numbers.
	r = a.at(t, "GET", "simple-host.test", "/v1/sites?all=1", nil, map[string]string{"X-API-Key": a.admin})
	if r.status != http.StatusOK {
		t.Fatalf("admin list: %d %s", r.status, r.body)
	}
	var all []struct {
		ID    string                `json:"id"`
		Views *operations.SiteViews `json:"views"`
	}
	if err := json.Unmarshal(r.body, &all); err != nil {
		t.Fatal(err)
	}
	var got *operations.SiteViews
	for _, it := range all {
		if it.ID == shop {
			got = it.Views
		}
	}
	if got == nil || *got != (operations.SiteViews{People7Days: 3, People30Days: 7}) {
		t.Errorf("admin all=1 shop views %+v", got)
	}
}

// The owner's own browser (Sec-Fetch-Site: same-origin) on the site list
// writes one mark with the ingester's hash of the client address; a plain API
// call without the header writes none.
func TestOwnerBrowserMark(t *testing.T) {
	a := newPrivateApp(t)
	a.sites.SetOwnerMarks(operations.NewOwnerMarks(a.database, "k", "t"))
	olive := a.newPerson(t, "olive")
	a.deploy(t, olive, "shop")
	uid, _ := a.userID(t, olive)

	count := func() int {
		t.Helper()
		var n int
		if err := a.database.QueryRow(`SELECT count(*) FROM owner_visitor_marks WHERE user_id = $1`, uid).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	// No Sec-Fetch-Site: an agent or SDK call, never a mark.
	r := a.at(t, "GET", "simple-host.test", "/v1/sites", nil, map[string]string{"X-API-Key": olive.key, "X-Forwarded-For": "198.51.100.7"})
	if r.status != http.StatusOK {
		t.Fatalf("list: %d %s", r.status, r.body)
	}
	if n := count(); n != 0 {
		t.Fatalf("plain API call wrote %d marks", n)
	}

	r = a.at(t, "GET", "simple-host.test", "/v1/sites", nil, map[string]string{"X-API-Key": olive.key, "Sec-Fetch-Site": "same-origin", "X-Forwarded-For": "198.51.100.7"})
	if r.status != http.StatusOK {
		t.Fatalf("list from the browser: %d %s", r.status, r.body)
	}
	if n := count(); n != 1 {
		t.Fatalf("browser list wrote %d marks, want 1", n)
	}
	var hash []byte
	if err := a.database.QueryRow(`SELECT ip_hash FROM owner_visitor_marks WHERE user_id = $1`, uid).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(hash, ownerTestHashIP("t", "198.51.100.7")) {
		t.Fatalf("mark hash is not HashIP(salt, client address)")
	}

	// The analytics reads mark too, and the throttle keeps it to one row.
	for _, p := range []string{"/v1/sites/shop/analytics?days=7", "/v1/sites/shop/analytics/geo?days=7", "/v1/sites/shop/analytics/top?days=7", "/v1/analytics/sites?days=7"} {
		if r := a.at(t, "GET", "simple-host.test", p, nil, map[string]string{"X-API-Key": olive.key, "Sec-Fetch-Site": "same-origin", "X-Forwarded-For": "198.51.100.7"}); r.status != http.StatusOK {
			t.Fatalf("%s: %d %s", p, r.status, r.body)
		}
	}
	if n := count(); n != 1 {
		t.Fatalf("analytics reads left %d marks, want 1", n)
	}

	// A cross-site or absent header on an analytics read: nothing new.
	if r := a.at(t, "GET", "simple-host.test", "/v1/sites/shop/analytics?days=7", nil, map[string]string{"X-API-Key": olive.key, "Sec-Fetch-Site": "cross-site", "X-Forwarded-For": "198.51.100.8"}); r.status != http.StatusOK {
		t.Fatalf("cross-site analytics: %d %s", r.status, r.body)
	}
	if n := count(); n != 1 {
		t.Fatalf("cross-site read wrote a mark: %d rows", n)
	}
}

// A visitor sign-in by the owner on their own site writes a mark; by anyone
// else, or by the owner on someone else's site, none. Exercises the hook the
// three sign-in paths call after their session row is written.
func TestOwnerVisitorSignInMark(t *testing.T) {
	a := newPrivateApp(t)
	a.sites.SetOwnerMarks(operations.NewOwnerMarks(a.database, "k", "t"))
	olive, vic := a.newPerson(t, "olive"), a.newPerson(t, "vic")
	a.deploy(t, olive, "shop")
	a.deploy(t, vic, "theirs")
	shop, theirs := a.siteID(t, olive, "shop"), a.siteID(t, vic, "theirs")
	oliveID, _ := a.userID(t, olive)
	vicID, _ := a.userID(t, vic)

	req := func(ip string) *http.Request {
		r := httptest.NewRequest("POST", "https://shop.olive.simple-host.test/v1/sites/shop/visitor/auth/verify", nil)
		r.Header.Set("X-Forwarded-For", ip)
		return r.WithContext(context.Background())
	}
	marks := func(uid string) int {
		t.Helper()
		var n int
		if err := a.database.QueryRow(`SELECT count(*) FROM owner_visitor_marks WHERE user_id = $1`, uid).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	a.sites.noteOwnerVisitorSignIn(req("203.0.113.20"), shop, vicID)     // a visitor, not the owner
	a.sites.noteOwnerVisitorSignIn(req("203.0.113.21"), theirs, oliveID) // the owner, on someone else's site
	if n := marks(oliveID) + marks(vicID); n != 0 {
		t.Fatalf("non-owner sign-ins wrote %d marks", n)
	}

	a.sites.noteOwnerVisitorSignIn(req("203.0.113.22"), shop, oliveID)
	if n := marks(oliveID); n != 1 {
		t.Fatalf("owner sign-in wrote %d marks, want 1", n)
	}
	var hash []byte
	if err := a.database.QueryRow(`SELECT ip_hash FROM owner_visitor_marks WHERE user_id = $1`, oliveID).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(hash, ownerTestHashIP("t", "203.0.113.22")) {
		t.Fatal("mark hash is not HashIP(salt, client address)")
	}

	// Without marks set up (analytics off) the hook is a no-op.
	a.sites.SetOwnerMarks(nil)
	a.sites.noteOwnerVisitorSignIn(req("203.0.113.23"), shop, oliveID)
	if n := marks(oliveID); n != 1 {
		t.Fatalf("marks off still wrote: %d rows", n)
	}
}

// Idle cleanup: a site whose only recent view is the owner's own still counts
// as in use (contract: owner views are activity for idle purposes).
func TestIdleOwnerViewCountsAsActivity(t *testing.T) {
	a := newPersonApp(t, "canonical")
	ctx := context.Background()
	olive := a.newPerson(t, "olive")
	a.deploy(t, olive, "quiet")
	a.deploy(t, olive, "mine")
	quiet, mine := a.siteID(t, olive, "quiet"), a.siteID(t, olive, "mine")
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := a.database.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	for _, s := range []string{quiet, mine} {
		exec(`UPDATE sites SET created_at = now() - interval '100 days', updated_at = now() - interval '100 days' WHERE id = $1`, s)
		exec(`UPDATE versions SET created_at = now() - interval '100 days' WHERE site_id = $1`, s)
	}
	// mine: old, but its owner opened it last week (class owner, no person).
	exec(`INSERT INTO site_view_hourly (site_id, hour, class, views) VALUES ($1, date_trunc('hour', now() - interval '7 days'), 'owner', 2) ON CONFLICT DO NOTHING`, mine)

	idle, err := sitesrepo.ListIdleSitesToWarn(ctx, a.database, model.IdleExempt{}, time.Now().AddDate(0, 0, -90), 0)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, s := range idle {
		found[s.SiteID] = true
	}
	if !found[quiet] {
		t.Errorf("quiet (no views at all) not listed as idle")
	}
	if found[mine] {
		t.Errorf("a site its owner opened last week was listed as idle")
	}
}

func ownerTestHashIP(salt, ip string) []byte {
	sum := sha256.Sum256([]byte(salt + ip))
	return sum[:16]
}
