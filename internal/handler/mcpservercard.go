package handler

import (
	"encoding/json"
	"net/http"

	"github.com/vsriram/simple-host/internal/buildinfo"
)

type mcpServerCard struct {
	Schema      string                `json:"$schema"`
	Name        string                `json:"name"`
	Title       string                `json:"title"`
	Description string                `json:"description"`
	Version     string                `json:"version"`
	Icon        string                `json:"icon"`
	Icons       []mcpServerCardIcon   `json:"icons"`
	Remotes     []mcpServerCardRemote `json:"remotes"`
}

type mcpServerCardIcon struct {
	Src string `json:"src"`
}

type mcpServerCardRemote struct {
	Type string `json:"type"`
	URL  string `json:"url"`
}

type aiCatalog struct {
	AgentCard     string `json:"agent_card"`
	MCPServerCard string `json:"mcp_server_card"`
	OpenAPI       string `json:"openapi"`
}

// RegisterMCPServerCard serves public MCP discovery metadata and its catalog.
func RegisterMCPServerCard(mux *http.ServeMux, issuer string) {

	serveCard := func(w http.ResponseWriter, r *http.Request) {
		endpoint, aliases := mcpDiscoveryEndpoints(issuer)
		card := mcpServerCard{
			Schema:      "https://static.modelcontextprotocol.io/schemas/v1/server-card.schema.json",
			Name:        "app.simple-host/mcp",
			Title:       "Simple Host",
			Description: "Put websites online: publish pages made in chat at their own address, with forms and saved data.",
			Version:     buildinfo.Version,
			Icon:        issuer + "/favicon.svg",
			Icons:       []mcpServerCardIcon{{Src: issuer + "/favicon.svg"}},
			Remotes:     []mcpServerCardRemote{{Type: "streamable-http", URL: endpoint}},
		}
		if hackMode {
			card.Name = "app.simple-hack/mcp"
			card.Title = "Simple Hack"
			card.Description = "Run events, join teams, publish team websites, and judge entries through the signed-in connector."
		}
		for _, alias := range aliases {
			card.Remotes = append(card.Remotes, mcpServerCardRemote{Type: "streamable-http", URL: alias})
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=60")
		_ = json.NewEncoder(w).Encode(card)
	}
	mux.HandleFunc("GET /.well-known/mcp/server-card.json", serveCard)
	mux.HandleFunc("GET /mcp/server-card", serveCard)
	mux.HandleFunc("GET /.well-known/ai-catalog.json", func(w http.ResponseWriter, r *http.Request) {
		catalog := aiCatalog{
			AgentCard:     issuer + "/.well-known/agent-card.json",
			MCPServerCard: issuer + "/.well-known/mcp/server-card.json",
			OpenAPI:       issuer + "/openapi.json",
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=60")
		_ = json.NewEncoder(w).Encode(catalog)
	})
}
