package servertest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/internal/auth"
	"github.com/vsriram/simple-host/internal/extension"
	"github.com/vsriram/simple-host/internal/papermetrics"
	accountrepo "github.com/vsriram/simple-host/internal/repository/accounts"
	platformtest "github.com/vsriram/simple-host/internal/server/servertest"
	"github.com/vsriram/simple-host/internal/testdb"
	"github.com/vsriram/simple-host/products/hack"
)

// The platform's handlers reach Hack's counters only through the recorder the
// product installs. The REST request names no event in its path or host, so
// it is counted only when the site handler's recorder is the product's; the
// MCP tool call's write is counted only when the connector's result hook is.
func TestPaperRecorderLiveAfterBuild(t *testing.T) {
	for _, product := range []string{"hack", "host"} {
		t.Run(product, func(t *testing.T) {
			cfg := platformtest.Config(t, testdb.Fresh(t))
			var paper *papermetrics.Collector
			ext := hack.Extension()
			wire := ext.Wire
			ext.Wire = func(d extension.Deps) (extension.Parts, error) {
				parts, err := wire(d)
				install := parts.PaperHack
				parts.PaperHack = func(c *papermetrics.Collector) {
					paper = c
					if product == "hack" {
						install(c)
					}
				}
				if product == "host" {
					// The platform alone: no product collector, no product recorder.
					parts.PaperRecorder = nil
				}
				return parts, err
			}
			app := platformtest.Build(t, cfg, []*extension.Extension{ext})
			if paper == nil {
				t.Fatal("paper collector was not handed to the product")
			}
			if (paper.Product() != nil) != (product == "hack") {
				t.Fatalf("product metrics installed = %v", paper.Product() != nil)
			}
			ctx := context.Background()
			db := paper.DB()
			key, _ := auth.GenerateAPIKey()
			holder, err := accountrepo.CreateUser(ctx, db, "paper-holder", key, false)
			if err != nil {
				t.Fatal(err)
			}
			starts := time.Now().UTC().Add(-time.Hour)
			if _, err := db.Exec(`INSERT INTO events(slug,account_id,title,organiser_name,contact_email,join_code,judge_code,stage,starts_at,ends_at)
 VALUES('paper-live',$1,'Paper','Test','test@example.com','paper-join','paper-judge','building',$2,$3)`, holder.ID, starts, starts.Add(24*time.Hour)); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`INSERT INTO event_members(event_id,user_id,role) SELECT id,account_id,'organiser' FROM events WHERE slug='paper-live'`); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`INSERT INTO sites(user_id,name) VALUES($1,'paper-team')`, holder.ID); err != nil {
				t.Fatal(err)
			}
			if paper.Product() != nil {
				paper.Product().Refresh(ctx)
			}
			send := func(method, path, body string) *httptest.ResponseRecorder {
				t.Helper()
				req := httptest.NewRequest(method, cfg.PublicBaseURL+path, strings.NewReader(body))
				req.Header.Set("X-API-Key", key)
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("MCP-Protocol-Version", "2025-06-18")
				w := httptest.NewRecorder()
				app.Handler.ServeHTTP(w, req)
				return w
			}
			if w := send(http.MethodGet, "/v1/sites/paper-team/storage/resources", ""); w.Code != http.StatusOK {
				t.Fatalf("REST storage list: %d %s", w.Code, w.Body.String())
			}
			// The tool call's own request is POST /mcp. Its event write counts
			// only when the result hook reaches the product's recorder.
			call := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"hack_update_event","arguments":{"slug":"paper-live","title":"Paper renamed"}}}`
			if w := send(http.MethodPost, "/mcp", call); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"isError":false`) {
				t.Fatalf("MCP event update: %d %s", w.Code, w.Body.String())
			}
			// One flush: a minute is written once, so both requests must be in it.
			if paper.Product() != nil {
				paper.Product().Flush(ctx, time.Now().UTC().Add(2*time.Minute))
			}
			var requests, writes int
			if err := db.QueryRow(`SELECT COALESCE(sum(requests),0),COALESCE(sum(writes),0) FROM paper_hack_minute`).Scan(&requests, &writes); err != nil {
				t.Fatal(err)
			}
			want := map[string]int{"hack": 1, "host": 0}[product]
			if requests != 2*want || writes != want {
				t.Fatalf("counted %d requests, %d writes; want %d, %d", requests, writes, 2*want, want)
			}
		})
	}
}
