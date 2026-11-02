package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/internal/db"
)

func TestListSitesSearch(t *testing.T) {
	a := newPersonApp(t, "serve")
	owner := a.newPerson(t, "search-owner")
	headers := map[string]string{"X-API-Key": owner.key}
	userID, _ := a.userID(t, owner)
	for i, fixture := range []struct{ name, page string }{
		{"homes", `<title>Real estate</title><meta name=description content="Coastal homes"><h1>For sale</h1><p>Sunny apartments</p>`},
		{"survey", `<title>Tuesday survey</title><h1>Real feedback</h1><p>Survey answers</p>`},
		{"empty", `<html><body></body></html>`},
	} {
		r := a.at(t, "POST", "simple-host.test", "/v1/sites/"+fixture.name+"/files", map[string]any{"files": map[string]string{"index.html": fixture.page}}, headers)
		if r.status != 201 {
			t.Fatalf("deploy: %d %s", r.status, r.body)
		}
		id := a.siteID(t, owner, fixture.name)
		stamp := fmt.Sprintf("2026-10-%02dT23:59:59.999999Z", 20+i)
		if _, err := a.database.Exec(`UPDATE sites SET created_at=$2, updated_at=$2 WHERE id=$1`, id, stamp); err != nil {
			t.Fatal(err)
		}
		if _, err := a.database.Exec(`UPDATE versions SET created_at=$2 WHERE site_id=$1`, id, stamp); err != nil {
			t.Fatal(err)
		}
	}
	for _, tt := range []struct {
		name, query string
		want        []string
		code        string
	}{
		{"default", "", []string{"empty", "survey", "homes"}, ""},
		{"unrelated parameter", "?ignored=1", []string{"empty", "survey", "homes"}, ""},
		{"empty query sorts", "?query=", []string{"empty", "survey", "homes"}, ""},
		{"case insensitive all words", "?query=REAL+coastal", []string{"homes"}, ""},
		{"all required", "?query=real+Tuesday+apartments", []string{}, ""},
		{"text substring", "?query=apart", []string{"homes"}, ""},
		{"headings", "?query=sale", []string{"homes"}, ""},
		{"nothing", "?query=unmatched", []string{}, ""},
		{"created day inclusive", "?created_after=2026-10-21&created_before=2026-10-21", []string{"survey"}, ""},
		{"created exact inclusive", "?created_after=2026-10-21T23:59:59.999999Z&created_before=2026-10-21T23:59:59.999999Z", []string{"survey"}, ""},
		{"before exact", "?created_before=2026-10-21T00:00:00Z", []string{"homes"}, ""},
		{"updated day inclusive", "?updated_after=2026-10-21&updated_before=2026-10-21", []string{"survey"}, ""},
		{"timezone", "?updated_after=2026-10-22T01:00:00%2B02:00&updated_before=2026-10-22T02:00:00%2B02:00", []string{"survey"}, ""},
		{"combined", "?query=real&created_before=2026-10-21&updated_after=2026-10-20&limit=1&offset=1", []string{"homes"}, ""},
		{"offset only", "?offset=1", []string{"survey", "homes"}, ""},
		{"limit only", "?limit=1", []string{"empty"}, ""},
		{"zero limit", "?limit=0", []string{}, ""},
		{"offset beyond", "?offset=999", []string{}, ""},
		{"negative limit", "?limit=-1", nil, "bad_pagination"},
		{"negative offset", "?offset=-1", nil, "bad_pagination"},
		{"invalid int", "?limit=no", nil, "bad_pagination"},
		{"fractional offset", "?offset=1.5", nil, "bad_pagination"},
		{"bad date", "?created_after=Tuesday", nil, "bad_date"},
		{"empty date", "?updated_before=", nil, "bad_date"},
		{"order name", "?order=name", []string{"empty", "homes", "survey"}, ""},
		{"order created", "?order=created", []string{"empty", "survey", "homes"}, ""},
		{"bad order", "?order=bogus", nil, "bad_order"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := a.at(t, "GET", "simple-host.test", "/v1/sites"+tt.query, nil, headers)
			if tt.code != "" {
				if r.status != 400 || r.json(t)["code"] != tt.code {
					t.Fatalf("%d %s", r.status, r.body)
				}
				return
			}
			var sites []siteResponse
			if r.status != 200 {
				t.Fatalf("%d %s", r.status, r.body)
			}
			if err := json.Unmarshal(r.body, &sites); err != nil {
				t.Fatal(err)
			}
			names := make([]string, 0, len(sites))
			for _, site := range sites {
				names = append(names, site.Name)
				if site.Name == "homes" && (site.Summary == nil || site.Summary.Title != "Real estate" || site.Summary.Description != "Coastal homes" || site.Summary.Headings != "For sale" || site.Summary.Text != "For sale Sunny apartments") {
					t.Errorf("summary: %+v", site.Summary)
				}
				if site.Name == "empty" && site.Summary != nil {
					t.Errorf("empty summary present: %+v", site.Summary)
				}
			}
			if !reflect.DeepEqual(names, tt.want) {
				t.Errorf("names=%v want=%v", names, tt.want)
			}
			if len(tt.want) == 0 && strings.TrimSpace(string(r.body)) != "[]" {
				t.Errorf("empty response=%s", r.body)
			}
			if tt.query == "" {
				// Rebuild the old response projection, allowing only the new summary.
				loaded, err := db.ListSitesByUser(context.Background(), a.database, userID)
				if err != nil {
					t.Fatal(err)
				}
				want := make([]siteResponse, 0, len(loaded))
				for _, site := range loaded {
					site.SummaryTitle = ""
					site.SummaryDescription = ""
					site.SummaryHeadings = ""
					site.SummaryText = ""
					resp := a.sites.toSiteResponse(site, "")
					footprint, err := a.sites.disk.SiteFootprint(site.UserID, site.Name)
					if err != nil {
						t.Fatal(err)
					}
					resp.FileBytes = footprint.Total()
					want = append(want, resp)
				}
				for i := range sites {
					sites[i].Summary = nil
				}
				gotJSON, err := json.Marshal(sites)
				if err != nil {
					t.Fatal(err)
				}
				wantJSON, err := json.Marshal(want)
				if err != nil {
					t.Fatal(err)
				}
				if string(gotJSON) != string(wantJSON) {
					t.Errorf("no-argument projection changed: got %s want %s", gotJSON, wantJSON)
				}
			}
		})
	}
	// Domain participates even when its certificate is not live.
	if _, err := a.database.Exec(`UPDATE sites SET custom_domain='survey-search.example.test' WHERE id=$1`, a.siteID(t, owner, "empty")); err != nil {
		t.Fatal(err)
	}
	r := a.at(t, "GET", "simple-host.test", "/v1/sites?query=survey-search", nil, headers)
	if r.status != 200 || !strings.Contains(string(r.body), `"name":"empty"`) {
		t.Fatalf("domain search: %d %s", r.status, r.body)
	}
	// Exercise the admin SELECT's separate scan order too.
	all, err := db.ListAllSites(context.Background(), a.database)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, site := range all {
		if site.UserID == userID && site.Name == "homes" {
			found = true
			if site.SummaryTitle != "Real estate" {
				t.Errorf("admin summary: %+v", site)
			}
		}
	}
	if !found {
		t.Fatal("admin list omitted site")
	}
	// More than one screenful without disk writes, to check actual REST bounds.
	if _, err := a.database.Exec(`INSERT INTO sites(user_id,name) SELECT $1, 'page-' || n FROM generate_series(1, 505) AS n`, userID); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		query string
		count int
	}{{"", 508}, {"?query=page-", 200}, {"?query=page-&limit=999", 500}, {"?query=page-&limit=999&offset=500", 5}} {
		r := a.at(t, "GET", "simple-host.test", "/v1/sites"+tt.query, nil, headers)
		var sites []siteResponse
		if r.status != 200 {
			t.Fatalf("paging: %d %s", r.status, r.body)
		}
		if err := json.Unmarshal(r.body, &sites); err != nil {
			t.Fatal(err)
		}
		if len(sites) != tt.count {
			t.Errorf("%s: got %d want %d", tt.query, len(sites), tt.count)
		}
	}
}

func TestSiteSummaryDeployUpdates(t *testing.T) {
	a := newPersonApp(t, "serve")
	owner := a.newPerson(t, "summary-deploy")
	headers := map[string]string{"X-API-Key": owner.key}
	for _, tt := range []struct {
		method, path, page, want string
		status                   int
	}{
		{"POST", "/v1/sites/content/files", "<title>First</title><h1>First heading</h1>", "First", 201},
		{"PUT", "/v1/sites/content/files?keep=1", "", "First", 200},
		{"PUT", "/v1/sites/content/files?publish=false", "<title>Preview</title>", "Preview", 200},
		{"PUT", "/v1/sites/content/files", "<title>Live</title>", "Live", 200},
		{"PUT", "/v1/sites/content/files", "<html><body></body></html>", "", 200},
	} {
		files := map[string]string{"index.html": tt.page}
		if tt.page == "" {
			files = map[string]string{"style.css": "body { color: red; }"}
		}
		r := a.at(t, tt.method, "simple-host.test", tt.path, map[string]any{"files": files}, headers)
		if r.status != tt.status {
			t.Fatalf("%s: %d %s", tt.path, r.status, r.body)
		}
		var got string
		if err := a.database.QueryRow(`SELECT summary_title FROM sites WHERE id=$1`, a.siteID(t, owner, "content")).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != tt.want {
			t.Errorf("%s: summary=%q want=%q", tt.path, got, tt.want)
		}
	}
	id := a.siteID(t, owner, "content")
	var before, after time.Time
	if err := a.database.QueryRow(`SELECT updated_at FROM sites WHERE id=$1`, id).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateSiteSummary(context.Background(), a.database, id, db.SiteSummary{Title: "Backfilled"}); err != nil {
		t.Fatal(err)
	}
	if err := a.database.QueryRow(`SELECT updated_at FROM sites WHERE id=$1`, id).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if !before.Equal(after) {
		t.Fatal("summary changed updated_at")
	}
}

// A deploy that races the one-time backfill (writes the current version's
// summary between the backfill's SELECT and its write) must win: the
// backfill's stale read must never clobber it, and must never re-run against
// an already-processed row either.
func TestUpdateSiteSummaryIfUnset(t *testing.T) {
	a := newPersonApp(t, "serve")
	owner := a.newPerson(t, "backfill-race")
	headers := map[string]string{"X-API-Key": owner.key}
	r := a.at(t, "POST", "simple-host.test", "/v1/sites/raced/files", map[string]any{"files": map[string]string{"index.html": "<title>First</title>"}}, headers)
	if r.status != 201 {
		t.Fatalf("deploy: %d %s", r.status, r.body)
	}
	id := a.siteID(t, owner, "raced")

	// A late backfill write for a site the deploy hook already summarised
	// must not overwrite it.
	if err := db.UpdateSiteSummaryIfUnset(context.Background(), a.database, id, db.SiteSummary{Title: "Stale backfill read"}); err != nil {
		t.Fatal(err)
	}
	var got string
	if err := a.database.QueryRow(`SELECT summary_title FROM sites WHERE id=$1`, id).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != "First" {
		t.Fatalf("backfill overwrote a live summary: %q", got)
	}

	// On a row genuinely never summarised (NULL), it writes normally.
	if _, err := a.database.Exec(`UPDATE sites SET summary_title=NULL WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateSiteSummaryIfUnset(context.Background(), a.database, id, db.SiteSummary{Title: "Backfilled"}); err != nil {
		t.Fatal(err)
	}
	if err := a.database.QueryRow(`SELECT summary_title FROM sites WHERE id=$1`, id).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != "Backfilled" {
		t.Fatalf("backfill did not write a never-summarised row: %q", got)
	}
}

// SIM-329: GET /v1/sites returns only the caller's own sites by default,
// even for the admin; ?all=1 is an admin-only switch to every account's.
func TestListSitesAdminScope(t *testing.T) {
	a := newPersonApp(t, "serve")
	owner := a.newPerson(t, "scope-owner")
	ownerHeaders := map[string]string{"X-API-Key": owner.key}
	r := a.at(t, "POST", "simple-host.test", "/v1/sites/ownsite/files", map[string]any{"files": map[string]string{"index.html": "hi"}}, ownerHeaders)
	if r.status != 201 {
		t.Fatalf("deploy: %d %s", r.status, r.body)
	}

	adminHeaders := map[string]string{"X-API-Key": a.admin}
	r = a.at(t, "GET", "simple-host.test", "/v1/sites", nil, adminHeaders)
	if r.status != 200 {
		t.Fatalf("admin default: %d %s", r.status, r.body)
	}
	if strings.Contains(string(r.body), `"name":"ownsite"`) {
		t.Fatalf("admin default leaked another account's site: %s", r.body)
	}

	r = a.at(t, "GET", "simple-host.test", "/v1/sites?all=1", nil, adminHeaders)
	if r.status != 200 {
		t.Fatalf("admin all=1: %d %s", r.status, r.body)
	}
	if !strings.Contains(string(r.body), `"name":"ownsite"`) {
		t.Fatalf("admin all=1 missing another account's site: %s", r.body)
	}

	r = a.at(t, "GET", "simple-host.test", "/v1/sites?all=1", nil, ownerHeaders)
	if r.status != 403 {
		t.Fatalf("non-admin all=1: %d %s", r.status, r.body)
	}
}

// Default order sorts by UpdatedAt alone: a stale LastDeployedAt (set by
// SIM-189's search relevance, unrelated to this sort) must not reorder it.
func TestSiteSearchSortByUpdatedAtOnly(t *testing.T) {
	now := time.Now()
	sites := []db.Site{{Name: "fallback", UpdatedAt: now}, {Name: "active", UpdatedAt: now.Add(time.Hour)}}
	sites[1].LastDeployedAt.Valid = true
	sites[1].LastDeployedAt.Time = now.Add(-time.Hour)
	got, err := filterSiteList(sites, url.Values{"query": {""}})
	if err != nil || got[0].Name != "active" {
		t.Fatalf("updated_at ordering: %+v %v", got, err)
	}
	for _, key := range []string{"created_after", "created_before", "updated_after", "updated_before"} {
		_, err := filterSiteList(sites, url.Values{key: {"bad"}})
		if err == nil || err.Code != "bad_date" || !strings.Contains(err.Error, key) {
			t.Errorf("%s: %+v", key, err)
		}
	}
}
