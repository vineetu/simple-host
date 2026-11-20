package servertest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/extension"
	"github.com/vsriram/simple-host/host/server"
	platformtest "github.com/vsriram/simple-host/host/server/servertest"
	"github.com/vsriram/simple-host/products/hack"
	"github.com/vsriram/simple-host/products/hack/testdb"
)

// The product's tools reach the connector through Parts.Tools after its
// routes are registered. The public agent card reads the tool server the
// routes use, so it shows whether the contribution is live after Build, and
// that a build without a product has none of Hack's tools.
func TestProductToolsAfterBuild(t *testing.T) {
	for _, product := range []string{"host", "hack"} {
		t.Run(product, func(t *testing.T) {
			cfg := platformtest.Config(t, testdb.Fresh(t))
			var exts []*extension.Extension
			if product == "hack" {
				exts = []*extension.Extension{hack.Extension()}
			}
			platformtest.RecordProduct(t, cfg, exts)
			app, err := server.Build(cfg, exts)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(app.Close)
			w := httptest.NewRecorder()
			app.Handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, cfg.PublicBaseURL+"/.well-known/agent-card.json", nil))
			var card struct {
				Tools []struct {
					Name string `json:"name"`
				} `json:"tools"`
			}
			if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &card) != nil || len(card.Tools) == 0 {
				t.Fatalf("agent card: %d %s", w.Code, w.Body.String())
			}
			names := map[string]bool{}
			hackTools := 0
			for _, tool := range card.Tools {
				names[tool.Name] = true
				if strings.HasPrefix(tool.Name, "hack_") {
					hackTools++
				}
			}
			if product == "hack" {
				for _, name := range []string{"hack_list_events", "hack_get_entries", "hack_set_event_website_mode", "hack_get_page_recipe"} {
					if !names[name] {
						t.Errorf("agent card after Build lacks %s", name)
					}
				}
				return
			}
			if hackTools != 0 || !names["create_site"] {
				t.Fatalf("Host build lists %d Hack tools; create_site listed: %v", hackTools, names["create_site"])
			}
			req := httptest.NewRequest(http.MethodPost, cfg.PublicBaseURL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"hack_list_events","arguments":{}}}`))
			req.Header.Set("X-API-Key", cfg.AdminAPIKey)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("MCP-Protocol-Version", "2025-06-18")
			w = httptest.NewRecorder()
			app.Handler.ServeHTTP(w, req)
			if !strings.Contains(w.Body.String(), `"error"`) || strings.Contains(w.Body.String(), `"structuredContent"`) {
				t.Fatalf("Host build ran a Hack tool: %d %s", w.Code, w.Body.String())
			}
		})
	}
}
