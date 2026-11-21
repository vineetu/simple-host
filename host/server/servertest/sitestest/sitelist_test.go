package sitestest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	model "github.com/vsriram/simple-host/host/model"
	operations "github.com/vsriram/simple-host/host/ops"
	"github.com/vsriram/simple-host/host/ops/repo"
	"github.com/vsriram/simple-host/host/sites"
	sitesrepo "github.com/vsriram/simple-host/host/sites/repo"
	"github.com/vsriram/simple-host/host/sites/siteshttp"
)

// GET /v1/sites searches the deployed pages' summaries and dates, pages
// within bounds, and refuses bad parameters with a code.
func TestListSitesSearch(t *testing.T) {
	a := NewApp(t)
	owner := a.NewPerson(t, "search-owner")
	for i, fixture := range []struct{ name, page string }{
		{"homes", `<title>Real estate</title><meta name=description content="Coastal homes"><h1>For sale</h1><p>Sunny apartments</p>`},
		{"survey", `<title>Tuesday survey</title><h1>Real feedback</h1><p>Survey answers</p>`},
		{"empty", `<html><body></body></html>`},
	} {
		if r := a.files(t, owner, "POST", fixture.name, map[string]string{"index.html": fixture.page}); r.Status != 201 {
			t.Fatalf("deploy: %d %s", r.Status, r.Body)
		}
		id := a.SiteID(t, owner, fixture.name)
		stamp := fmt.Sprintf("2026-10-%02dT23:59:59.999999Z", 20+i)
		if _, err := a.Database.Exec(`UPDATE sites SET created_at=$2, updated_at=$2 WHERE id=$1`, id, stamp); err != nil {
			t.Fatal(err)
		}
	}
	list := func(query string) ([]siteshttp.SiteResponse, string) {
		t.Helper()
		r := a.api(t, owner, "GET", "/v1/sites"+query, nil)
		if r.Status != 200 {
			return nil, r.Code()
		}
		var out []siteshttp.SiteResponse
		if err := json.Unmarshal(r.Body, &out); err != nil {
			t.Fatal(err)
		}
		return out, ""
	}
	for _, tt := range []struct {
		name, query string
		want        []string
		code        string
	}{
		{"default", "", []string{"empty", "survey", "homes"}, ""},
		{"case insensitive all words", "?query=REAL+coastal", []string{"homes"}, ""},
		{"all required", "?query=real+Tuesday+apartments", []string{}, ""},
		{"text substring", "?query=apart", []string{"homes"}, ""},
		{"created day inclusive", "?created_after=2026-10-21&created_before=2026-10-21", []string{"survey"}, ""},
		{"timezone", "?updated_after=2026-10-22T01:00:00%2B02:00&updated_before=2026-10-22T02:00:00%2B02:00", []string{"survey"}, ""},
		{"combined", "?query=real&created_before=2026-10-21&updated_after=2026-10-20&limit=1&offset=1", []string{"homes"}, ""},
		{"order name", "?order=name", []string{"empty", "homes", "survey"}, ""},
		{"zero limit", "?limit=0", nil, "bad_pagination"},
		{"fractional offset", "?offset=1.5", nil, "bad_pagination"},
		{"bad date", "?created_after=Tuesday", nil, "bad_date"},
		{"bad order", "?order=bogus", nil, "bad_order"},
	} {
		sites, code := list(tt.query)
		if code != tt.code {
			t.Fatalf("%s: code %q, want %q", tt.name, code, tt.code)
		}
		if tt.code != "" {
			continue
		}
		names := []string{}
		for _, site := range sites {
			names = append(names, site.Name)
			if site.Name == "homes" && (site.Summary == nil || site.Summary.Title != "Real estate" || site.Summary.Description != "Coastal homes" || site.Summary.Headings != "For sale") {
				t.Errorf("summary: %+v", site.Summary)
			}
		}
		if !reflect.DeepEqual(names, tt.want) {
			t.Errorf("%s: names=%v want=%v", tt.name, names, tt.want)
		}
	}
	// A bound domain is searchable even before its certificate is live.
	if _, err := a.Database.Exec(`UPDATE sites SET custom_domain='survey-search.example.test' WHERE id=$1`, a.SiteID(t, owner, "empty")); err != nil {
		t.Fatal(err)
	}
	if sites, _ := list("?query=survey-search"); len(sites) != 1 || sites[0].Name != "empty" {
		t.Fatalf("domain search: %+v", sites)
	}
	// Page bounds: a search returns 200 by default and at most 500.
	if _, err := a.Database.Exec(`INSERT INTO sites(user_id,name) SELECT $1, 'page-' || n FROM generate_series(1, 505) AS n`, owner.ID); err != nil {
		t.Fatal(err)
	}
	for query, count := range map[string]int{"": 508, "?query=page-": 200, "?query=page-&limit=500&offset=500": 5} {
		if sites, _ := list(query); len(sites) != count {
			t.Errorf("%s: got %d want %d", query, len(sites), count)
		}
	}
}

// Each published deploy refreshes the summary from the live index.html (a
// keep patch without one leaves it); a backfill neither touches updated_at
// nor overwrites a summary a deploy wrote.
func TestSiteSummaryDeployUpdates(t *testing.T) {
	a := NewApp(t)
	owner := a.NewPerson(t, "summary-deploy")
	summary := func() string {
		t.Helper()
		var got string
		if err := a.Database.QueryRow(`SELECT coalesce(summary_title, '<null>') FROM sites WHERE id=$1`, a.SiteID(t, owner, "content")).Scan(&got); err != nil {
			t.Fatal(err)
		}
		return got
	}
	for _, tt := range []struct {
		method, path, page, want string
	}{
		{"POST", "/v1/sites/content/files", "<title>First</title>", "First"},
		{"PUT", "/v1/sites/content/files?keep=1", "", "First"},
		{"PUT", "/v1/sites/content/files?publish=false", "<title>Preview</title>", "Preview"},
		{"PUT", "/v1/sites/content/files", "<html><body></body></html>", ""},
	} {
		files := map[string]string{"index.html": tt.page}
		if tt.page == "" {
			files = map[string]string{"style.css": "body { color: red; }"}
		}
		if r := a.api(t, owner, tt.method, tt.path, map[string]any{"files": files}); r.Status/100 != 2 {
			t.Fatalf("%s: %d %s", tt.path, r.Status, r.Body)
		}
		if got := summary(); got != tt.want {
			t.Errorf("%s: summary=%q want=%q", tt.path, got, tt.want)
		}
	}
	id := a.SiteID(t, owner, "content")
	ctx := context.Background()
	var before, after time.Time
	_ = a.Database.QueryRow(`SELECT updated_at FROM sites WHERE id=$1`, id).Scan(&before)
	if err := sitesrepo.UpdateSiteSummary(ctx, a.Database, id, model.SiteSummary{Title: "Deployed"}); err != nil {
		t.Fatal(err)
	}
	_ = a.Database.QueryRow(`SELECT updated_at FROM sites WHERE id=$1`, id).Scan(&after)
	if !before.Equal(after) {
		t.Fatal("summary changed updated_at")
	}
	// The one-time backfill writes only a never-summarised (NULL) row.
	if err := sitesrepo.UpdateSiteSummaryIfUnset(ctx, a.Database, id, model.SiteSummary{Title: "Stale backfill read"}); err != nil || summary() != "Deployed" {
		t.Fatalf("backfill overwrote a live summary: %q %v", summary(), err)
	}
	if _, err := a.Database.Exec(`UPDATE sites SET summary_title=NULL WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if err := sitesrepo.UpdateSiteSummaryIfUnset(ctx, a.Database, id, model.SiteSummary{Title: "Backfilled"}); err != nil || summary() != "Backfilled" {
		t.Fatalf("backfill did not write a never-summarised row: %q %v", summary(), err)
	}
}

// SIM-329: GET /v1/sites returns only the caller's own sites, even for the
// admin; ?all=1 is an admin-only switch to every account's.
func TestListSitesAdminScope(t *testing.T) {
	a := NewApp(t)
	owner := a.NewPerson(t, "scope-owner")
	a.Deploy(t, owner, "ownsite")
	if r := a.admin(t, "GET", "/v1/sites", nil); r.Status != 200 || strings.Contains(string(r.Body), `"name":"ownsite"`) {
		t.Fatalf("admin default: %d %s", r.Status, r.Body)
	}
	if r := a.admin(t, "GET", "/v1/sites?all=1", nil); r.Status != 200 || !strings.Contains(string(r.Body), `"name":"ownsite"`) {
		t.Fatalf("admin all=1: %d %s", r.Status, r.Body)
	}
	if r := a.api(t, owner, "GET", "/v1/sites?all=1", nil); r.Status != 403 {
		t.Fatalf("non-admin all=1: %d %s", r.Status, r.Body)
	}
}

// Every item of GET /v1/sites carries people-only views over the rolling 7
// and 30 days (owner and bot visits left out), once the traffic log is on.
func TestListSitesViews(t *testing.T) {
	a := NewApp(t)
	olive := a.NewPerson(t, "olive")
	shop := a.Deploy(t, olive, "shop")
	a.Deploy(t, olive, "quiet")
	for _, v := range []struct {
		class string
		ago   int
		n     int64
	}{{repo.ClassPerson, 1, 3}, {repo.ClassPerson, 12 * 24, 4}, {repo.ClassPerson, 45 * 24, 500}, {repo.ClassOwner, 2, 40}, {repo.ClassBot, 2, 60}} {
		if _, err := a.Database.Exec(`INSERT INTO site_view_hourly (site_id, hour, class, views)
			VALUES ($1, date_trunc('hour', now()) - make_interval(hours => $2), $3, $4)
			ON CONFLICT (site_id, hour, class) DO UPDATE SET views = site_view_hourly.views + EXCLUDED.views`, shop, v.ago, v.class, v.n); err != nil {
			t.Fatal(err)
		}
	}
	if r := a.api(t, olive, "GET", "/v1/sites", nil); r.Status != 200 || strings.Contains(string(r.Body), `"views"`) {
		t.Fatalf("views without the analytics log: %d %s", r.Status, r.Body)
	}
	a.Sites.SetTrafficLog(true)
	views := func(r []byte) map[string]operations.SiteViews {
		var items []struct {
			Name  string                `json:"name"`
			ID    string                `json:"id"`
			Views *operations.SiteViews `json:"views"`
		}
		if err := json.Unmarshal(r, &items); err != nil {
			t.Fatal(err)
		}
		out := map[string]operations.SiteViews{}
		for _, it := range items {
			if it.Views != nil {
				out[it.Name] = *it.Views
				out[it.ID] = *it.Views
			}
		}
		return out
	}
	seen := views(a.api(t, olive, "GET", "/v1/sites", nil).Body)
	if want := (operations.SiteViews{People7Days: 3, People30Days: 7}); seen["shop"] != want {
		t.Errorf("shop views %+v, want %+v", seen["shop"], want)
	}
	if v, ok := seen["quiet"]; !ok || v != (operations.SiteViews{}) {
		t.Errorf("quiet views %+v %v, want zeros", v, ok)
	}
	if all := views(a.admin(t, "GET", "/v1/sites?all=1", nil).Body); all[shop] != (operations.SiteViews{People7Days: 3, People30Days: 7}) {
		t.Errorf("admin all=1 shop views %+v", all[shop])
	}
}

// The owner's own browser (Sec-Fetch-Site: same-origin) on the site list or
// analytics writes one throttled mark with the ingester's hash of the client
// address; a plain API call or a cross-site read writes none.
func TestOwnerBrowserMark(t *testing.T) {
	a := NewApp(t)
	a.Sites.SetOwnerMarks(operations.NewOwnerMarks(a.Database, "k", "t"))
	olive := a.NewPerson(t, "olive")
	a.Deploy(t, olive, "shop")
	count := func() int {
		t.Helper()
		var n int
		if err := a.Database.QueryRow(`SELECT count(*) FROM owner_visitor_marks WHERE user_id = $1`, olive.ID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	get := func(path, fetchSite, ip string) {
		t.Helper()
		h := map[string]string{"X-API-Key": olive.Key, "X-Forwarded-For": ip}
		if fetchSite != "" {
			h["Sec-Fetch-Site"] = fetchSite
		}
		if r := a.At(t, "GET", "simple-host.test", path, nil, h); r.Status != http.StatusOK {
			t.Fatalf("%s: %d %s", path, r.Status, r.Body)
		}
	}
	get("/v1/sites", "", "198.51.100.7")
	if n := count(); n != 0 {
		t.Fatalf("plain API call wrote %d marks", n)
	}
	get("/v1/sites", "same-origin", "198.51.100.7")
	get("/v1/sites/shop/analytics?days=7", "same-origin", "198.51.100.7")
	get("/v1/sites/shop/analytics?days=7", "cross-site", "198.51.100.8")
	if n := count(); n != 1 {
		t.Fatalf("browser reads left %d marks, want 1", n)
	}
	var hash []byte
	if err := a.Database.QueryRow(`SELECT ip_hash FROM owner_visitor_marks WHERE user_id = $1`, olive.ID).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if sum := sha256.Sum256([]byte("t" + "198.51.100.7")); !bytes.Equal(hash, sum[:16]) {
		t.Fatal("mark hash is not HashIP(salt, client address)")
	}
}

// While the person's site-host certificate waits in the issuer's queue, the
// site list says so with the interim address; once ready it says nothing.
func TestAddressStateFromIssuerFiles(t *testing.T) {
	a := NewApp(t)
	olive := a.NewPerson(t, "olive")
	a.Deploy(t, olive, "shop")
	ready := filepath.Join(a.CertDir, "ready", olive.Handle)
	if err := os.Remove(ready); err != nil {
		t.Fatal(err)
	}
	state := func() map[string]any {
		t.Helper()
		sites.ForgetAddressState(olive.Handle)
		var listed []struct {
			AddressState map[string]any `json:"address_state"`
		}
		r := a.api(t, olive, "GET", "/v1/sites", nil)
		if err := json.Unmarshal(r.Body, &listed); err != nil || len(listed) != 1 {
			t.Fatalf("site list: %s", r.Body)
		}
		return listed[0].AddressState
	}
	if s := state(); s["state"] != "waiting" || s["address"] != "https://"+olive.SiteHost("shop")+"/" || s["interim_address"] != "https://"+olive.Handle+".simple-host.test/shop/" {
		t.Fatalf("waiting: %v", s)
	}
	if err := os.MkdirAll(filepath.Join(a.CertDir, "failed"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(a.CertDir, "failed", olive.Handle), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if s := state(); s["state"] != "failing" {
		t.Fatalf("failing: %v", s)
	}
	a.MarkReady(t, olive.Handle)
	if s := state(); s != nil {
		t.Fatalf("ready site still carries address_state: %v", s)
	}
}
