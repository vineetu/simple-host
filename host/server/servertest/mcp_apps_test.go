package servertest

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMCPAppsAuthenticatedHandle(t *testing.T) {
	a := NewConnectorApp(t)
	owner := a.NewPerson(t, "app-owner")
	other := a.NewPerson(t, "app-other")
	handle := strings.Split(owner.Email, "@")[0]
	t.Setenv("MCP_APP_PHOTO_PICKER", "accounts:"+handle)
	client := a.RegisterClient(t, TestRedirect)
	token := a.Connect(t, owner, client, TestRedirect)["access_token"].(string)
	// OAuth and API-key requests get the handle from their existing authenticated lookup.
	for _, headers := range []map[string]string{
		{"Authorization": "Bearer " + token}, {"X-API-Key": owner.Key}, {"X-API-Key": other.Key},
	} {
		headers["Content-Type"] = "application/json"
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"})
		response := a.Do(t, "POST", "/mcp", strings.NewReader(string(body)), headers)
		expected := headers["X-API-Key"] != other.Key
		if got := strings.Contains(string(response.Body), `"resourceUri":"ui://simple-host/photo-picker"`); got != expected {
			t.Fatalf("authenticated rollout: status %d, UI %v, want %v", response.Status, got, expected)
		}
	}
}
