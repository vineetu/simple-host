package siteshttp

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/host/web"

	"github.com/vsriram/simple-host/host/edition"
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

func TestGroupHostDispatch(t *testing.T) {
	for _, tc := range []struct {
		name, method, path string
		err                error
		status             int
		page, api          bool
	}{
		{"page", "GET", "/", nil, 200, true, false},
		{"missing", "GET", "/", sql.ErrNoRows, 404, false, false},
		{"lookup failure", "GET", "/", errors.New("lookup failed"), 503, false, false},
		{"api refuses builtin", "GET", "/v1/sites/group/me", nil, 404, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := withHosts(&SiteHandler{Ed: edition.Default(config.DefaultLimits()), SiteDomain: "example.test"})
			page, api := false, false
			h.Hosts.SetGroupHosts(&stubGroupHosts{err: tc.err, page: func(w http.ResponseWriter, r *http.Request, u model.User) bool {
				page = true
				w.WriteHeader(200)
				return true
			}})

			w := httptest.NewRecorder()
			h.ServeGroupHost(w, httptest.NewRequest(tc.method, "https://group.example.test"+tc.path, nil), model.User{}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { api = true }))
			if w.Code != tc.status || page != tc.page || api != tc.api {
				t.Fatalf("status=%d page=%v api=%v body=%s", w.Code, page, api, w.Body.String())
			}
		})
	}
}
