package handler

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMCPAppsAuthenticatedHandle(t *testing.T) {
	a := newConnectorApp(t)
	owner := a.newPerson(t, "app-owner")
	other := a.newPerson(t, "app-other")
	handle := strings.Split(owner.email, "@")[0]
	t.Setenv("MCP_APP_PHOTO_PICKER", "accounts:"+handle)
	client := a.registerClient(t, testRedirect)
	token := a.connect(t, owner, client, testRedirect)["access_token"].(string)
	// OAuth and API-key requests get the handle from their existing authenticated lookup.
	for _, headers := range []map[string]string{
		{"Authorization": "Bearer " + token}, {"X-API-Key": owner.key}, {"X-API-Key": other.key},
	} {
		headers["Content-Type"] = "application/json"
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"})
		response := a.do(t, "POST", "/mcp", strings.NewReader(string(body)), headers)
		expected := headers["X-API-Key"] != other.key
		if got := strings.Contains(string(response.body), `"resourceUri":"ui://simple-host/photo-picker"`); got != expected {
			t.Fatalf("authenticated rollout: status %d, UI %v, want %v", response.status, got, expected)
		}
	}
}
