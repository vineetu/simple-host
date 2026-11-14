package handler

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/internal/edition"
	"github.com/vsriram/simple-host/internal/model"
)

type stubGroupHosts struct {
	group  Group
	err    error
	page   func(http.ResponseWriter, *http.Request, model.User) bool
	pinned int
}

func (g *stubGroupHosts) ByAccount(context.Context, string) (Group, error)      { return g.group, g.err }
func (g *stubGroupHosts) BySlug(context.Context, string) (Group, error)         { return g.group, g.err }
func (g *stubGroupHosts) HasSite(context.Context, string, string) (bool, error) { return true, g.err }
func (g *stubGroupHosts) PinnedVersion(context.Context, string, string) (int, bool, error) {
	return g.pinned, g.pinned > 0, g.err
}
func (g *stubGroupHosts) ServePage(w http.ResponseWriter, r *http.Request, u model.User) bool {
	if g.page != nil {
		return g.page(w, r, u)
	}
	return false
}
func (*stubGroupHosts) Texts() GroupTexts {
	return GroupTexts{"Group not found", "No group here.", "Choose a group."}
}

func TestGroupHostDispatch(t *testing.T) {
	for _, tc := range []struct {
		name, method, path string
		err                error
		status             int
		page, api          bool
	}{
		{"page", "GET", "/", nil, 200, true, false},
		{"head", "HEAD", "/", nil, 200, true, false},
		{"method before lookup", "POST", "/", errors.New("lookup failed"), 405, false, false},
		{"missing", "GET", "/", sql.ErrNoRows, 404, false, false},
		{"lookup failure", "GET", "/", errors.New("lookup failed"), 503, false, false},
		{"api refuses builtin", "GET", "/v1/sites/group/me", nil, 404, false, false},
		{"api lookup failure", "GET", "/v1/sites/group/me", errors.New("lookup failed"), 404, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &SiteHandler{ed: edition.ForKind(edition.Host), siteDomain: "example.test"}
			page, api := false, false
			h.SetGroupHosts(&stubGroupHosts{err: tc.err, page: func(w http.ResponseWriter, r *http.Request, u model.User) bool {
				page = true
				w.WriteHeader(200)
				return true
			}})
			w := httptest.NewRecorder()
			h.serveGroupHost(w, httptest.NewRequest(tc.method, "https://group.example.test"+tc.path, nil), model.User{}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { api = true }))
			if w.Code != tc.status || page != tc.page || api != tc.api {
				t.Fatalf("status=%d page=%v api=%v body=%s", w.Code, page, api, w.Body.String())
			}
		})
	}
}

func TestGroupHostsNilAndErrors(t *testing.T) {
	h := &SiteHandler{ed: edition.ForKind(edition.Host), siteDomain: "example.test"}
	if _, err := h.groupByAccount(context.Background(), "a"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
	if _, err := h.groupBySlug(context.Background(), "a"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
	if got := h.groupPinnedFloor(context.Background(), "a", "site", 10); got != 10 {
		t.Fatal(got)
	}
	h.ed = edition.ForKind(edition.Hack)
	if got := h.groupPinnedFloor(context.Background(), "a", "site", 10); got != 10 {
		t.Fatal(got)
	}
	h.SetGroupHosts(&stubGroupHosts{err: errors.New("unavailable")})
	if got := h.groupPinnedFloor(context.Background(), "a", "site", 10); got != 0 {
		t.Fatalf("error floor=%d", got)
	}
	if _, _, err := h.notificationSiteInfo(context.Background(), model.Site{}); err == nil {
		t.Fatal("notification guessed on lookup failure")
	}
	h.SetGroupHosts(&stubGroupHosts{pinned: 3})
	if got := h.groupPinnedFloor(context.Background(), "a", "site", 10); got != 3 {
		t.Fatal(got)
	}
}

func TestGroupNotificationAddresses(t *testing.T) {
	h := hackFlatApp(t, "")
	h.SetGroupHosts(&stubGroupHosts{group: Group{GroupRef: GroupRef{ID: "event-id", Slug: "weekend", AccountID: "holder"}}})
	for _, tc := range []struct{ site, name, url string }{
		{"event-id", "weekend", "https://weekend.simple-hack.test/"},
		{"alpha", "alpha", "https://alpha.weekend.simple-hack.test/"},
	} {
		name, url, err := h.notificationSiteInfo(context.Background(), model.Site{Name: tc.site, UserID: "holder"})
		if err != nil || name != tc.name || url != tc.url {
			t.Fatalf("%s: %s %s %v", tc.site, name, url, err)
		}
	}
}

func TestGroupSweepOrder(t *testing.T) {
	for _, n := range []int{0, 8, 9, 10, 19} {
		var calls []string
		step := func(name string) func(context.Context) { return func(context.Context) { calls = append(calls, name) } }
		groupSweepPass(context.Background(), n, GroupSweep{Sweep: step("sweep"), After: step("after")}, step("markers"))
		want := []string{"sweep", "after"}
		if n%10 == 9 {
			want = []string{"sweep", "markers", "after"}
		}
		if !reflect.DeepEqual(calls, want) {
			t.Fatalf("pass %d: %v want %v", n, calls, want)
		}
	}
	var calls []string
	groupSweepPass(context.Background(), 9, GroupSweep{Sweep: func(context.Context) { calls = append(calls, "sweep") }}, func(context.Context) { calls = append(calls, "markers") })
	if strings.Join(calls, ",") != "sweep,markers" {
		t.Fatal(calls)
	}
}

func TestGroupHostCustomWebsiteAndAPIAllowlist(t *testing.T) {
	a, _ := newSiteApp(t, "canonical")
	owner := a.newPerson(t, "group-holder")
	a.deploy(t, owner, "custom")
	id, _ := a.userID(t, owner)
	hosts := &stubGroupHosts{group: Group{GroupRef: GroupRef{ID: "custom", Slug: "weekend", AccountID: id}, CustomWebsite: true}, page: func(w http.ResponseWriter, r *http.Request, u model.User) bool {
		w.Write([]byte("product page"))
		return true
	}}
	a.sites.SetGroupHosts(hosts)
	user := model.User{ID: id}
	for _, p := range []string{
		"/v1/sites/weekend/me", "/v1/sites/weekend/visitor/auth", "/v1/sites/weekend/visitor/auth/verify", "/v1/sites/weekend/storage/kv/data",
		"/v1/sso/start", "/v1/visitor/establish", "/v1/visitor/logout", "/v1/visitor/oauth/start",
	} {
		w := httptest.NewRecorder()
		a.sites.serveGroupHost(w, httptest.NewRequest("POST", p, nil), user, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(202) }))
		if w.Code != 202 {
			t.Fatalf("allowed %s: %d %s", p, w.Code, w.Body.String())
		}
	}
	for _, p := range []string{"/v1/hack/events/weekend", "/v1/sites/other/me", "/v1/sites/weekend/files", "/v1/sites/weekend/storage", "/v1/sites/weekend/me/extra", "/v1/visitor/oauth"} {
		w := httptest.NewRecorder()
		a.sites.serveGroupHost(w, httptest.NewRequest("GET", p, nil), user, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Errorf("API leaked %s", p) }))
		if w.Code != 404 {
			t.Fatalf("refused %s: %d", p, w.Code)
		}
	}
	w := httptest.NewRecorder()
	a.sites.serveGroupHost(w, httptest.NewRequest("GET", "/", nil), user, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "<h1>custom</h1>") {
		t.Fatalf("custom before page: %d %s", w.Code, w.Body.String())
	}
	hosts.group.TakenDown = true
	w = httptest.NewRecorder()
	a.sites.serveGroupHost(w, httptest.NewRequest("GET", "/", nil), user, nil)
	if w.Code != 200 || w.Body.String() != "product page" {
		t.Fatalf("taken-down custom leaked: %d %s", w.Code, w.Body.String())
	}
	if a.sites.groupAPIAllowed(httptest.NewRequest("GET", "/v1/sites/weekend/me", nil), user) {
		t.Fatal("taken-down custom API allowed")
	}
	hosts.group.TakenDown = false
	hosts.group.ID = "missing"
	if a.sites.groupAPIAllowed(httptest.NewRequest("GET", "/v1/sites/weekend/me", nil), user) {
		t.Fatal("missing custom site API allowed")
	}
}
