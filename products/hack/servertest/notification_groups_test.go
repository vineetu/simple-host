package servertest

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/internal/extension"
	accountrepo "github.com/vsriram/simple-host/internal/repository/accounts"
	"github.com/vsriram/simple-host/internal/repository/sqlkit"
	platformtest "github.com/vsriram/simple-host/internal/server/servertest"
	"github.com/vsriram/simple-host/internal/testdb"
	"github.com/vsriram/simple-host/products/hack"
)

type countedNotificationGroups struct {
	hack.NotificationGroups
	calls int
	err   error
}

func (g *countedNotificationGroups) Labels(ctx context.Context, q sqlkit.Querier, ids []string) (map[string]string, error) {
	g.calls++
	if g.err != nil {
		return nil, g.err
	}
	labels := make(map[string]string)
	for _, id := range ids {
		labels[id] = "wired-label"
	}
	return labels, nil
}

func TestNotificationGroupsInstalledAfterBuild(t *testing.T) {
	cfg := platformtest.Config(t, testdb.Fresh(t))
	ext := hack.Extension()
	wire := ext.Wire
	groups := &countedNotificationGroups{}
	var database *sql.DB
	ext.Wire = func(d extension.Deps) (extension.Parts, error) {
		parts, err := wire(d)
		if err != nil {
			return parts, err
		}
		if parts.Hooks.Notifications == nil {
			t.Fatal("Hack extension has no notification groups")
		}
		database = d.DB
		parts.Hooks.Notifications = groups
		return parts, nil
	}
	app := platformtest.Build(t, cfg, []*extension.Extension{ext})
	const key = "notification-wiring-person"
	person, err := accountrepo.CreateUser(context.Background(), database, "notification-wiring", key, false)
	if err != nil {
		t.Fatal(err)
	}
	owner := person.ID
	var site string
	if err := database.QueryRow(`INSERT INTO sites(user_id,name) VALUES($1,'group-notifications') RETURNING id`, owner).Scan(&site); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO site_notifications(id,site_id,owner_id,source,url,events,secret) VALUES('sub_wired',$1,$2,'owner','https://example.invalid/notify','{record.added}','')`, site, owner); err != nil {
		t.Fatal(err)
	}
	for _, failure := range []bool{false, true} {
		if failure {
			groups.err = errors.New("label lookup failed")
		}
		for _, transport := range []string{"REST", "MCP"} {
			before := groups.calls
			req := httptest.NewRequest(http.MethodGet, cfg.PublicBaseURL+"/v1/notifications", nil)
			if transport == "MCP" {
				req = httptest.NewRequest(http.MethodPost, cfg.PublicBaseURL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_notifications","arguments":{}}}`))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("MCP-Protocol-Version", "2025-06-18")
			}
			req.Header.Set("X-API-Key", key)
			w := httptest.NewRecorder()
			if transport == "MCP" {
				app.Mux.ServeHTTP(w, req)
			} else {
				app.Handler.ServeHTTP(w, req)
			}
			if groups.calls != before+1 {
				t.Fatalf("%s hook calls = %d, response %d %s", transport, groups.calls-before, w.Code, w.Body.String())
			}
			if !failure && (w.Code != 200 || !strings.Contains(w.Body.String(), "wired-label")) {
				t.Fatalf("%s label: %d %s", transport, w.Code, w.Body.String())
			}
			if failure {
				if transport == "REST" && (w.Code != 500 || !strings.Contains(w.Body.String(), "internal_error")) {
					t.Fatalf("REST error: %d %s", w.Code, w.Body.String())
				}
				if transport == "MCP" && !strings.Contains(w.Body.String(), `"isError":true`) {
					t.Fatalf("MCP error: %s", w.Body.String())
				}
			}
		}
	}
}
