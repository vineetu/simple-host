package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/vsriram/simple-host/internal/buildinfo"
	"github.com/vsriram/simple-host/internal/mcp"
)

func TestMCPServerCard(t *testing.T) {
	for _, issuer := range []string{"https://simple-host.app", "https://SIMPLE-HOST.APP", "https://sites.example.org"} {
		t.Run(issuer, func(t *testing.T) {
			mux := http.NewServeMux()
			h := &ConnectorHandler{issuer: issuer, mcp: mcp.NewServer(mcp.Config{})}
			h.Register(mux, func(next http.Handler) http.Handler { return next })
			readJSON := func(path string, dst any) string {
				t.Helper()
				rec := httptest.NewRecorder()
				mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
				if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/json; charset=utf-8" || rec.Header().Get("Cache-Control") != "public, max-age=60" {
					t.Fatalf("%s: unexpected response: %d %v", path, rec.Code, rec.Header())
				}
				if err := json.Unmarshal(rec.Body.Bytes(), dst); err != nil {
					t.Fatalf("%s: %v", path, err)
				}
				return rec.Body.String()
			}

			var agent agentCard
			readJSON("/.well-known/agent-card.json", &agent)
			wantURLs := []string{issuer + "/mcp"}
			if strings.EqualFold(issuer, "https://simple-host.app") {
				wantURLs = append(wantURLs, "https://mcp.simple-host.app/mcp")
			}
			var firstBody string
			for _, path := range []string{"/.well-known/mcp/server-card.json", "/mcp/server-card"} {
				var card mcpServerCard
				body := readJSON(path, &card)
				if firstBody != "" && body != firstBody {
					t.Fatal("server card routes returned different bodies")
				}
				firstBody = body
				if card.Schema != "https://static.modelcontextprotocol.io/schemas/v1/server-card.schema.json" || card.Name != "app.simple-host/mcp" || card.Title != "Simple Host" {
					t.Fatalf("unexpected identity: %+v", card)
				}
				if card.Version != buildinfo.Version || card.Version != agent.Version {
					t.Fatalf("inconsistent version: server=%q agent=%q build=%q", card.Version, agent.Version, buildinfo.Version)
				}
				if n := utf8.RuneCountInString(card.Description); n == 0 || n > 100 || strings.Contains(card.Description, "\u2014") {
					t.Fatalf("invalid description (%d characters): %q", n, card.Description)
				}
				if card.Icon != issuer+"/favicon.svg" || !reflect.DeepEqual(card.Icons, []mcpServerCardIcon{{Src: card.Icon}}) {
					t.Fatalf("unexpected icon: %+v", card)
				}
				var urls []string
				for _, remote := range card.Remotes {
					if remote.Type != "streamable-http" {
						t.Fatalf("unexpected transport: %+v", remote)
					}
					urls = append(urls, remote.URL)
				}
				if !reflect.DeepEqual(urls, wantURLs) || !reflect.DeepEqual(urls, append([]string{agent.Endpoint}, agent.EndpointAliases...)) {
					t.Fatalf("inconsistent remotes: got %v, want %v; agent=%s %v", urls, wantURLs, agent.Endpoint, agent.EndpointAliases)
				}
				var fields map[string]json.RawMessage
				if err := json.Unmarshal([]byte(body), &fields); err != nil {
					t.Fatal(err)
				}
				if _, ok := fields["tools"]; ok {
					t.Fatal("server card must not list tools")
				}
			}

			var catalog map[string]string
			readJSON("/.well-known/ai-catalog.json", &catalog)
			wantCatalog := map[string]string{
				"agent_card":      issuer + "/.well-known/agent-card.json",
				"mcp_server_card": issuer + "/.well-known/mcp/server-card.json",
				"openapi":         issuer + "/openapi.json",
			}
			if !reflect.DeepEqual(catalog, wantCatalog) {
				t.Fatalf("unexpected catalog: %v", catalog)
			}
		})
	}
}
