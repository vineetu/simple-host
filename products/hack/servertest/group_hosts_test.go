package servertest

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/internal/auth"
	"github.com/vsriram/simple-host/internal/extension"
	"github.com/vsriram/simple-host/internal/handler"
	accountrepo "github.com/vsriram/simple-host/internal/repository/accounts"
	platformtest "github.com/vsriram/simple-host/internal/server/servertest"
	"github.com/vsriram/simple-host/internal/testdb"
	"github.com/vsriram/simple-host/products/hack"
)

type countedGroupHosts struct {
	handler.GroupHosts
	accounts, pins int
}

func (g *countedGroupHosts) ByAccount(ctx context.Context, id string) (handler.Group, error) {
	g.accounts++
	return g.GroupHosts.ByAccount(ctx, id)
}
func (g *countedGroupHosts) PinnedVersion(ctx context.Context, id, name string) (int, bool, error) {
	g.pins++
	return g.GroupHosts.PinnedVersion(ctx, id, name)
}

func TestGroupHostsInstalledAfterBuild(t *testing.T) {
	cfg := platformtest.Config(t, testdb.Fresh(t))
	cfg.PersonHosts = "canonical"
	cfg.SiteHosts = "canonical"
	ext := hack.Extension()
	wire := ext.Wire
	var db *sql.DB
	var hosts *countedGroupHosts
	ext.Wire = func(d extension.Deps) (extension.Parts, error) {
		parts, err := wire(d)
		db = d.DB
		hosts = &countedGroupHosts{GroupHosts: parts.Hooks.GroupHosts}
		parts.Hooks.GroupHosts = hosts
		return parts, err
	}
	app := platformtest.Build(t, cfg, []*extension.Extension{ext})
	key, _ := auth.GenerateAPIKey()
	if _, err := accountrepo.CreateUser(context.Background(), db, "group-wiring@example.com", key, false); err != nil {
		t.Fatal(err)
	}
	request := func(method, host, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "https://"+host+path, strings.NewReader(body))
		r.Header.Set("X-API-Key", key)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("MCP-Protocol-Version", "2025-06-18")
		w := httptest.NewRecorder()
		app.Handler.ServeHTTP(w, r)
		return w
	}
	w := request("POST", cfg.SiteDomain, "/v1/hack/events", `{"slug":"group-wiring","title":"Wiring event","organiser_name":"Organiser","contact_email":"organiser@example.com","purpose":"testing","expected_participants":10,"starts_at":"2026-10-01","time_zone":"UTC"}`)
	if w.Code != 201 {
		t.Fatalf("create event: %d %s", w.Code, w.Body.String())
	}
	w = request("GET", "group-wiring."+cfg.SiteDomain, "/", "")
	if w.Code != 200 || hosts.accounts == 0 {
		t.Fatalf("REST host: %d, hook calls=%d %s", w.Code, hosts.accounts, w.Body.String())
	}
	for n := 1; n <= 3; n++ {
		body := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"hack_publish_event_website","arguments":{"slug":"group-wiring","files":{"index.html":"version %d"}}}}`, n)
		w = request("POST", cfg.SiteDomain, "/mcp", body)
		if w.Code != 200 || strings.Contains(w.Body.String(), `"isError":true`) {
			t.Fatalf("MCP publish: %d %s", w.Code, w.Body.String())
		}
		if n == 1 {
			if _, err := db.Exec(`UPDATE sites SET keep_versions=1 WHERE user_id=(SELECT account_id FROM events WHERE slug='group-wiring')`); err != nil {
				t.Fatal(err)
			}
		}
	}
	if hosts.pins == 0 {
		t.Fatal("MCP publication never reached installed pinned-version hook")
	}
}

func TestGroupHostsNilAfterBuild(t *testing.T) {
	cfg := platformtest.Config(t, testdb.Fresh(t))
	app := platformtest.Build(t, cfg, nil)
	w := httptest.NewRecorder()
	app.Handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, cfg.PublicBaseURL+"/", nil))
	if w.Code != 200 || strings.Contains(w.Body.String(), "Event not found") {
		t.Fatalf("platform page: %d %s", w.Code, w.Body.String())
	}
}
