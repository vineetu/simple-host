package sitestest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/host/edition"
	model "github.com/vsriram/simple-host/host/model"
	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/sites/eventdns"
	"github.com/vsriram/simple-host/host/sites/siteshttp"
	"github.com/vsriram/simple-host/host/web"
)

// The admin site list spans accounts: it sorts by size (kept versions count)
// and by recent human views, searches, and is refused to everyone else.
func TestAdminSitesAPI(t *testing.T) {
	a := NewApp(t)
	owner := a.NewPerson(t, "sort-sites")
	for i, name := range []string{"old", "middle", "new"} {
		r := a.files(t, owner, "POST", name, map[string]string{"index.html": strings.Repeat("x", []int{1000, 200, 100}[i])})
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
	if r := a.files(t, owner, "PUT", "old", map[string]string{"index.html": "x"}); r.Status != 200 {
		t.Fatalf("update: %d", r.Status)
	}
	names := func(q string) ([]string, []any) {
		t.Helper()
		r := a.admin(t, "GET", "/v1/admin/sites?"+q, nil)
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
	if r := a.admin(t, "GET", "/v1/admin/sites?sort=invalid", nil); r.Status != 400 {
		t.Fatal("invalid query accepted")
	}
	if r := a.api(t, owner, "GET", "/v1/admin/sites", nil); r.Status != 404 {
		t.Fatal("non-admin access")
	}
}

// ?owner=<handle> on the per-site analytics endpoints: the platform admin may
// read any account's site; everyone else gets exactly the 404 of a site that
// does not exist.
func TestSiteAnalyticsOwnerParam(t *testing.T) {
	a := NewApp(t)
	olive, oscar, boss := a.NewPerson(t, "olive"), a.NewPerson(t, "oscar"), a.NewPerson(t, "boss")
	a.Deploy(t, olive, "shop")
	a.Deploy(t, oscar, "oscar-only")
	if _, err := a.Database.Exec(`UPDATE users SET is_admin = true WHERE id = $1`, boss.ID); err != nil {
		t.Fatal(err)
	}
	want := func(label, key string, code int, site, owner string) []rt.Resp {
		t.Helper()
		var out []rt.Resp
		q := "?days=7"
		if owner != "" {
			q += "&owner=" + url.QueryEscape(owner)
		}
		for _, p := range []string{"/analytics", "/analytics/geo", "/analytics/top"} {
			h := map[string]string{}
			if key != "" {
				h["X-API-Key"] = key
			}
			r := a.At(t, "GET", rt.SiteDomain, "/v1/sites/"+site+p+q, nil, h)
			if r.Status != code {
				t.Errorf("%s: GET %s = %d, want %d (%s)", label, p, r.Status, code, r.Body)
			}
			out = append(out, r)
		}
		return out
	}
	want("no key", "", http.StatusUnauthorized, "shop", olive.Handle)
	want("own site + own handle, other case", olive.Key, http.StatusOK, "shop", strings.ToUpper(olive.Handle))
	missing := want("missing site", olive.Key, http.StatusNotFound, "no-such-site", "")
	got := want("non-admin naming another owner", olive.Key, http.StatusNotFound, "oscar-only", oscar.Handle)
	for i := range got {
		if !bytes.Equal(got[i].Body, missing[i].Body) {
			t.Errorf("another owner's site is distinguishable from a missing one: %s vs %s", got[i].Body, missing[i].Body)
		}
	}
	for _, adm := range []struct{ label, key string }{{"admin key", a.Admin}, {"is_admin account", boss.Key}} {
		want(adm.label+" reads other owner", adm.key, http.StatusOK, "oscar-only", oscar.Handle)
		want(adm.label+" wrong owner for site", adm.key, http.StatusNotFound, "oscar-only", olive.Handle)
		want(adm.label+" no owner", adm.key, http.StatusNotFound, "oscar-only", "")
	}
}

type stubGroupHosts struct {
	group web.Group
	page  func(http.ResponseWriter, *http.Request, model.User) bool
}

func (g *stubGroupHosts) ByAccount(context.Context, string) (web.Group, error)  { return g.group, nil }
func (g *stubGroupHosts) BySlug(context.Context, string) (web.Group, error)     { return g.group, nil }
func (g *stubGroupHosts) HasSite(context.Context, string, string) (bool, error) { return true, nil }
func (g *stubGroupHosts) PinnedVersion(context.Context, string, string) (int, bool, error) {
	return 0, false, nil
}
func (g *stubGroupHosts) ServePage(w http.ResponseWriter, r *http.Request, u model.User) bool {
	return g.page != nil && g.page(w, r, u)
}
func (*stubGroupHosts) Texts() web.GroupTexts { return web.GroupTexts{} }

// A group host with a custom website lets through only the visitor API of its
// own site, and a taken-down custom site falls back to the product page.
func TestGroupHostCustomWebsiteAndAPIAllowlist(t *testing.T) {
	a := NewApp(t)
	owner := a.NewPerson(t, "group-holder")
	a.Deploy(t, owner, "custom")
	hosts := &stubGroupHosts{group: web.Group{GroupRef: web.GroupRef{ID: "custom", Slug: "weekend", AccountID: owner.ID}, CustomWebsite: true}, page: func(w http.ResponseWriter, r *http.Request, u model.User) bool {
		_, _ = w.Write([]byte("product page"))
		return true
	}}
	a.Hosts.SetGroupHosts(hosts)
	user := model.User{ID: owner.ID}
	for _, p := range []string{"/v1/sites/weekend/me", "/v1/sites/weekend/storage/kv/data", "/v1/visitor/establish"} {
		w := httptest.NewRecorder()
		a.Sites.ServeGroupHost(w, httptest.NewRequest("POST", p, nil), user, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(202) }))
		if w.Code != 202 {
			t.Fatalf("allowed %s: %d %s", p, w.Code, w.Body.String())
		}
	}
	for _, p := range []string{"/v1/sites/other/me", "/v1/sites/weekend/files", "/v1/sites/weekend/me/extra"} {
		w := httptest.NewRecorder()
		a.Sites.ServeGroupHost(w, httptest.NewRequest("GET", p, nil), user, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Errorf("API leaked %s", p) }))
		if w.Code != 404 {
			t.Fatalf("refused %s: %d", p, w.Code)
		}
	}
	w := httptest.NewRecorder()
	a.Sites.ServeGroupHost(w, httptest.NewRequest("GET", "/", nil), user, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "<h1>custom</h1>") {
		t.Fatalf("custom page: %d %s", w.Code, w.Body.String())
	}
	hosts.group.TakenDown = true
	w = httptest.NewRecorder()
	a.Sites.ServeGroupHost(w, httptest.NewRequest("GET", "/", nil), user, nil)
	if w.Code != 200 || w.Body.String() != "product page" {
		t.Fatalf("taken-down custom leaked: %d %s", w.Code, w.Body.String())
	}
	if a.Sites.GroupAPIAllowed(httptest.NewRequest("GET", "/v1/sites/weekend/me", nil), user) {
		t.Fatal("taken-down custom API allowed")
	}
}

type eventTestDNS struct {
	created, deleted []string
	failCreate       int
}

func (d *eventTestDNS) TakenNames(context.Context, string) (map[string]bool, error) {
	return map[string]bool{}, nil
}
func (d *eventTestDNS) CreateA(_ context.Context, _, label, _ string, ttl int) (string, error) {
	d.created = append(d.created, label)
	if d.failCreate == len(d.created) {
		return "", errors.New("create failed")
	}
	return fmt.Sprintf("record-%d", len(d.created)), nil
}
func (d *eventTestDNS) DeleteRecord(_ context.Context, _, id string) error {
	d.deleted = append(d.deleted, id)
	return nil
}

type eventProbeTransport struct{}

func (eventProbeTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}}, nil
}

// An event name claims two DNS records, only its owner releases it, a
// half-created claim cleans up after itself, and expiry removes the records.
func TestEventDomainWorkflow(t *testing.T) {
	database := rt.DB(t)
	p := rt.NewPerson(t, database, "event-owner")
	dns := &eventTestDNS{}
	const domain = "event-workflow.test"
	lim := edition.Default(config.DefaultLimits()).Limits()
	svc := sites.New(database, nil, nil, 0, sites.Deps{EventDNS: dns, EventProbe: &eventdns.LiveProbe{HTTP: &http.Client{Transport: eventProbeTransport{}}}, EventDomains: []string{domain}, EventTTL: lim.EventTTL, EventMaxClaims: lim.EventMaxClaims})
	h := siteshttp.NewEventDomainHandler(svc, []string{domain})
	request := func(method string, user *model.User) int {
		t.Helper()
		r := httptest.NewRequest(method, "/v1/events", strings.NewReader(`{"name":"meetup","ip":"8.8.8.8"}`))
		r = r.WithContext(auth.WithUser(r.Context(), user))
		w := httptest.NewRecorder()
		if method == http.MethodPost {
			h.Claim(w, r)
		} else {
			r.SetPathValue("name", "meetup")
			h.Release(w, r)
		}
		return w.Code
	}
	claimed := func() bool {
		t.Helper()
		ok, err := h.NameClaimed(context.Background(), domain, "meetup")
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	owner := &model.User{ID: p.ID}
	if c := request("POST", owner); c != 201 || !claimed() || !reflect.DeepEqual(dns.created, []string{"meetup", "sites.meetup"}) {
		t.Fatalf("claim: %d %v", c, dns.created)
	}
	if c := request("POST", owner); c != 201 || !reflect.DeepEqual(dns.deleted, []string{"record-1", "record-2"}) {
		t.Fatalf("renew: %d %v", c, dns.deleted)
	}
	if c := request("DELETE", &model.User{ID: "another-user"}); c != 404 || !claimed() {
		t.Fatalf("non-owner release: %d", c)
	}
	if c := request("DELETE", owner); c != 204 || claimed() {
		t.Fatalf("release: %d", c)
	}
	dns.failCreate = len(dns.created) + 2
	if c := request("POST", owner); c != 502 || claimed() || dns.deleted[len(dns.deleted)-1] != "record-5" {
		t.Fatalf("partial claim: %d %v", c, dns.deleted)
	}
	dns.failCreate = 0
	if c := request("POST", owner); c != 201 {
		t.Fatalf("claim before sweep: %d", c)
	}
	if _, err := database.Exec(`UPDATE event_domains SET expires_at=now()-interval '1 second' WHERE name='meetup' AND domain=$1`, domain); err != nil {
		t.Fatal(err)
	}
	h.SweepOnce(context.Background())
	if claimed() || !reflect.DeepEqual(dns.deleted[len(dns.deleted)-2:], []string{"record-7", "record-8"}) {
		t.Fatalf("sweep: %v", dns.deleted)
	}
}
