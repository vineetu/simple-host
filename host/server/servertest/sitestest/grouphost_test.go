package sitestest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/server/servertest"
	"github.com/vsriram/simple-host/host/web"

	"github.com/vsriram/simple-host/host/model"
)

type stubGroupHosts struct {
	group  web.Group
	err    error
	page   func(http.ResponseWriter, *http.Request, model.User) bool
	pinned int
}

func (g *stubGroupHosts) ByAccount(context.Context, string) (web.Group, error)  { return g.group, g.err }
func (g *stubGroupHosts) BySlug(context.Context, string) (web.Group, error)     { return g.group, g.err }
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
func (*stubGroupHosts) Texts() web.GroupTexts {
	return web.GroupTexts{NotFoundTitle: "Group not found", NotFoundBody: "No group here.", NoPersonalSites: "Choose a group."}
}

func TestGroupHostCustomWebsiteAndAPIAllowlist(t *testing.T) {
	a, _ := servertest.NewSiteApp(t, "canonical")
	owner := a.NewPerson(t, "group-holder")
	a.Deploy(t, owner, "custom")
	id, _ := a.UserID(t, owner)
	hosts := &stubGroupHosts{group: web.Group{GroupRef: web.GroupRef{ID: "custom", Slug: "weekend", AccountID: id}, CustomWebsite: true}, page: func(w http.ResponseWriter, r *http.Request, u model.User) bool {
		w.Write([]byte("product page"))
		return true
	}}
	a.Sites.Hosts.SetGroupHosts(hosts)
	user := model.User{ID: id}
	for _, p := range []string{
		"/v1/sites/weekend/me", "/v1/sites/weekend/visitor/auth", "/v1/sites/weekend/visitor/auth/verify", "/v1/sites/weekend/storage/kv/data",
		"/v1/sso/start", "/v1/visitor/establish", "/v1/visitor/logout", "/v1/visitor/oauth/start",
	} {
		w := httptest.NewRecorder()
		a.Sites.ServeGroupHost(w, httptest.NewRequest("POST", p, nil), user, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(202) }))
		if w.Code != 202 {
			t.Fatalf("allowed %s: %d %s", p, w.Code, w.Body.String())
		}
	}
	for _, p := range []string{"/v1/hack/events/weekend", "/v1/sites/other/me", "/v1/sites/weekend/files", "/v1/sites/weekend/storage", "/v1/sites/weekend/me/extra", "/v1/visitor/oauth"} {
		w := httptest.NewRecorder()
		a.Sites.ServeGroupHost(w, httptest.NewRequest("GET", p, nil), user, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Errorf("API leaked %s", p) }))
		if w.Code != 404 {
			t.Fatalf("refused %s: %d", p, w.Code)
		}
	}
	w := httptest.NewRecorder()
	a.Sites.ServeGroupHost(w, httptest.NewRequest("GET", "/", nil), user, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "<h1>custom</h1>") {
		t.Fatalf("custom before page: %d %s", w.Code, w.Body.String())
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
	hosts.group.TakenDown = false
	hosts.group.ID = "missing"
	if a.Sites.GroupAPIAllowed(httptest.NewRequest("GET", "/v1/sites/weekend/me", nil), user) {
		t.Fatal("missing custom site API allowed")
	}
}
