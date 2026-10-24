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
	EndpointAliases []string        `json:"endpoint_aliases,omitempty"`
	Auth            agentCardAuth   `json:"auth"`
	Tools           []agentCardTool `json:"tools"`
}

type agentCardAuth struct {
	Type                string `json:"type"`
	AuthorizationServer string `json:"authorization_server"`
	ProtectedResource   string `json:"protected_resource"`
}

// RegisterAgentCard serves public DNS-AID discovery metadata for this instance.
func RegisterAgentCard(mux *http.ServeMux, issuer string, mcpServer *mcp.Server) {

	mux.HandleFunc("GET /.well-known/agent-card.json", func(w http.ResponseWriter, r *http.Request) {
		var tools []mcp.Tool
		if hackMode {
			tools = mcpServer.HackListedTools()
		} else {
			tools = mcpServer.ListedTools()
		}
		endpoint, aliases := mcpDiscoveryEndpoints(issuer)
		card := agentCard{
			Name:        "Simple Host",
			Description: "Simple Host publishes websites with a small backend (KV, SQLite and files), each at its own address. This MCP server lets an agent build and manage sites on behalf of a signed-in person.",
			Version:     buildinfo.Version, Protocol: "mcp", ProtocolVersion: mcp.ProtocolVersion,
			Endpoint: endpoint, EndpointAliases: aliases,
			Auth: agentCardAuth{
				Type:                "oauth2",
				AuthorizationServer: issuer + "/.well-known/oauth-authorization-server",
				ProtectedResource:   issuer + "/.well-known/oauth-protected-resource/mcp",
			},
			Tools: make([]agentCardTool, 0, len(tools)),
		}
		if hackMode {
			card.Name = "Simple Hack"
			card.Description = "Organisers manage events, teams, judging, and results. Participants join teams, submit entries, and publish to a selected team website. Judges read rubrics and score assigned entries. Each call checks the signed-in person's role, membership, stage, and deadline. This public tool overview contains no account or event data."
		}
		for _, tool := range tools {
			description := tool.Description
			if hackMode {
				// Titles describe generic actions without inherited Host address examples.
				description = tool.Title
			}
			card.Tools = append(card.Tools, agentCardTool{tool.Name, agentCardSummary(description)})
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=60")
		_ = json.NewEncoder(w).Encode(card)
	})
}

// mcpDiscoveryEndpoints keeps both public discovery cards on the same URLs.
func mcpDiscoveryEndpoints(issuer string) (string, []string) {
	var aliases []string
	if !hackMode && strings.EqualFold(issuer, "https://simple-host.app") {
		aliases = []string{"https://mcp.simple-host.app/mcp"}
	}
	return issuer + "/mcp", aliases
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
