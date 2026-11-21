package accesshttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/vsriram/simple-host/host/buildinfo"
	"github.com/vsriram/simple-host/host/mcp"
	"github.com/vsriram/simple-host/host/web"
)

func TestAgentCard(t *testing.T) {
	for _, issuer := range []string{"https://simple-host.app", "https://SIMPLE-HOST.APP", "https://sites.example.org"} {
		t.Run(issuer, func(t *testing.T) {
			mux := http.NewServeMux()
			server := mcp.NewServer(mcp.Config{
				APIHost:    strings.TrimPrefix(issuer, "https://"),
				SelfHosted: !strings.EqualFold(issuer, "https://simple-host.app"),
				Rewrite: func(text string) string {
					return strings.ReplaceAll(text, "simple-host.app", strings.TrimPrefix(issuer, "https://"))
				},
			})
			h := &ConnectorHandler{Issuer: issuer, mcp: server}
			h.Register(mux, func(next http.Handler) http.Handler { return next })
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/.well-known/agent-card.json", nil))
			if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/json; charset=utf-8" || rec.Header().Get("Cache-Control") != "public, max-age=60" {
				t.Fatalf("unexpected response: %d %v", rec.Code, rec.Header())
			}
			var card web.AgentCard
			if err := json.Unmarshal(rec.Body.Bytes(), &card); err != nil {
				t.Fatal(err)
			}
			if card.Name != "Simple Host" || card.Description == "" || card.Version != buildinfo.Version || card.Protocol != "mcp" || card.ProtocolVersion != mcp.ProtocolVersion || card.Endpoint != issuer+"/mcp" {
				t.Fatalf("unexpected card: %+v", card)
			}
			if card.Auth != (web.AgentCardAuth{Type: "oauth2", AuthorizationServer: issuer + "/.well-known/oauth-authorization-server", ProtectedResource: issuer + "/.well-known/oauth-protected-resource/mcp"}) {
				t.Fatalf("unexpected auth: %+v", card.Auth)
			}
			if strings.EqualFold(issuer, "https://simple-host.app") {
				if len(card.EndpointAliases) != 1 || card.EndpointAliases[0] != "https://mcp.simple-host.app/mcp" {
					t.Fatalf("unexpected endpoint aliases: %v", card.EndpointAliases)
				}
			} else if strings.Contains(rec.Body.String(), `"endpoint_aliases"`) {
				t.Fatal("non-canonical issuer must omit endpoint_aliases")
			}
			tools := server.ListedTools()
			if len(card.Tools) != len(tools) {
				t.Fatalf("got %d tools, want %d", len(card.Tools), len(tools))
			}
			for i, tool := range tools {
				if card.Tools[i].Name != tool.Name || card.Tools[i].Description != web.AgentCardSummary(tool.Description) || card.Tools[i].Description == "" || utf8.RuneCountInString(card.Tools[i].Description) > 120 {
					t.Fatalf("unexpected tool: %+v", card.Tools[i])
				}
			}
		})
	}
}
