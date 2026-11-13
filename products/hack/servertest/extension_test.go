package servertest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/internal/extension"
	"github.com/vsriram/simple-host/internal/server"
	platformtest "github.com/vsriram/simple-host/internal/server/servertest"
	"github.com/vsriram/simple-host/internal/testdb"
	"github.com/vsriram/simple-host/products/hack"
)

func TestExtensionGateExactlyOnce(t *testing.T) {
	for _, product := range []string{"host", "hack"} {
		t.Run(product, func(t *testing.T) {
			cfg := platformtest.Config(t, testdb.Fresh(t))
			ext := hack.Extension()
			if product == "host" {
				// A counting gate on the default edition also proves that the
				// Host legacy path passes through to its ordinary handler.
				ext = &extension.Extension{Gate: ext.Gate}
			}
			factoryCalls, wraps, calls := 0, 0, 0
			gate := ext.Gate
			ext.Gate = func(d extension.GateDeps) func(http.Handler) http.Handler {
				factoryCalls++
				middleware := gate(d)
				return func(next http.Handler) http.Handler {
					wraps++
					gated := middleware(next)
					return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						calls++
						gated.ServeHTTP(w, r)
					})
				}
			}
			app, err := server.Build(cfg, []*extension.Extension{ext})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(app.Close)
			if factoryCalls != 1 || wraps != 1 {
				t.Fatalf("gate factory/wrapping calls = %d/%d, want 1/1", factoryCalls, wraps)
			}
			for i := 1; i <= 2; i++ {
				req := httptest.NewRequest(http.MethodGet, cfg.PublicBaseURL+"/v1/sites/missing/state", nil)
				req.Header.Set("X-API-Key", cfg.AdminAPIKey)
				w := httptest.NewRecorder()
				app.Handler.ServeHTTP(w, req)
				if calls != i {
					t.Fatalf("gate calls after %d REST requests = %d", i, calls)
				}
				if product == "hack" {
					if w.Code != http.StatusGone || !strings.Contains(w.Body.String(), `"legacy_storage_removed"`) {
						t.Fatalf("Hack legacy storage = %d %s", w.Code, w.Body.String())
					}
				} else {
					ordinary := httptest.NewRecorder()
					app.Mux.ServeHTTP(ordinary, req)
					if w.Code == http.StatusGone || w.Code != ordinary.Code || w.Body.String() != ordinary.Body.String() {
						t.Fatalf("Host legacy storage = %d %s; ordinary handler = %d %s", w.Code, w.Body.String(), ordinary.Code, ordinary.Body.String())
					}
				}
			}

			// Enter the registered connector directly to isolate its in-process
			// upstream calls from the outer HTTP request's middleware traversal.
			calls = 0
			req := httptest.NewRequest(http.MethodPost, cfg.PublicBaseURL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_collections","arguments":{"site":"missing"}}}`))
			req.Header.Set("X-API-Key", cfg.AdminAPIKey)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("MCP-Protocol-Version", "2025-06-18")
			w := httptest.NewRecorder()
			app.Mux.ServeHTTP(w, req)
			var reply struct {
				Result struct {
					IsError bool `json:"isError"`
					Content []struct{ Type, Text string }
				}
			}
			if err := json.Unmarshal(w.Body.Bytes(), &reply); err != nil {
				t.Fatalf("MCP response: %v: %s", err, w.Body.String())
			}
			want := "list_collections is no longer offered by this connector. Existing sites keep that saved data, readable in the person's dashboard. For anything new use the storage_* tools (get_page_recipe shows the page code)."
			if w.Code != http.StatusOK || !reply.Result.IsError || len(reply.Result.Content) != 1 || reply.Result.Content[0].Text != want || calls != 0 {
				t.Fatalf("retired MCP call = %d %s; gate calls = %d", w.Code, w.Body.String(), calls)
			}
		})
	}
}
