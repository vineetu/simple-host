package sitestest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/host/edition"
	model "github.com/vsriram/simple-host/host/model"
	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/sites/eventdns"
	"github.com/vsriram/simple-host/host/sites/events"
	"github.com/vsriram/simple-host/host/web"
)

type stubGroupHosts struct {
	group web.Group
	page  func(http.ResponseWriter, *http.Request, model.User) bool
}

func (g *stubGroupHosts) ByAccount(context.Context, string) (web.Group, error) { return g.group, nil }

func (g *stubGroupHosts) BySlug(context.Context, string) (web.Group, error) { return g.group, nil }

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
	h := events.NewHandler(svc, []string{domain})
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
