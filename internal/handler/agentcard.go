package handler

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/vsriram/simple-host/internal/buildinfo"
	"github.com/vsriram/simple-host/internal/mcp"
)

type agentCardTool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type agentCard struct {
	Name            string          `json:"name"`
	Description     string          `json:"description"`
	Version         string          `json:"version"`
	Protocol        string          `json:"protocol"`
	ProtocolVersion string          `json:"protocolVersion"`
	Endpoint        string          `json:"endpoint"`
	Auth            agentCardAuth   `json:"auth"`
	Tools           []agentCardTool `json:"tools"`
}

type agentCardAuth struct {
	Type                string `json:"type"`
	AuthorizationServer string `json:"authorization_server"`
	ProtectedResource   string `json:"protected_resource"`
}

// RegisterAgentCard serves public DNS-AID discovery metadata for this instance.
func RegisterAgentCard(mux *http.ServeMux, issuer string) {
	mux.HandleFunc("GET /.well-known/agent-card.json", func(w http.ResponseWriter, r *http.Request) {
		tools := mcp.Tools()
		card := agentCard{
			Name:        "Simple Host",
			Description: "Simple Host publishes websites with a small backend (KV, SQLite and files), each at its own address. This MCP server lets an agent build and manage sites on behalf of a signed-in person.",
			Version:     buildinfo.Version, Protocol: "mcp", ProtocolVersion: mcp.ProtocolVersion,
			Endpoint: issuer + "/mcp",
			Auth: agentCardAuth{
				Type:                "oauth2",
				AuthorizationServer: issuer + "/.well-known/oauth-authorization-server",
				ProtectedResource:   issuer + "/.well-known/oauth-protected-resource/mcp",
			},
			Tools: make([]agentCardTool, 0, len(tools)),
		}
		for _, tool := range tools {
			card.Tools = append(card.Tools, agentCardTool{tool.Name, agentCardSummary(tool.Description)})
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=60")
		_ = json.NewEncoder(w).Encode(card)
	})
}

func agentCardSummary(description string) string {
	description = strings.Join(strings.Fields(description), " ")
	if end := strings.Index(description, ". "); end >= 0 {
		description = description[:end+1]
	}
	chars := []rune(description)
	if len(chars) > 120 {
		return string(chars[:117]) + "..."
	}
	return description
}
