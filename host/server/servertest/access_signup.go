package servertest

import (
	"encoding/json"
	"testing"
)

const SignupApex = "simple-host.test"

func (a *PrivateApp) RegisterNamedClient(t *testing.T, name string) string {
	t.Helper()
	r := a.At(t, "POST", SignupApex, "/oauth/register", map[string]any{
		"client_name": name, "redirect_uris": []string{"https://chat.example.com/cb"}, "token_endpoint_auth_method": "none",
	}, nil)
	if r.Status != 201 {
		t.Fatalf("register: %d %s", r.Status, r.Body)
	}
	id := r.JSON(t)["client_id"].(string)
	t.Cleanup(func() { _, _ = a.Database.Exec(`DELETE FROM oauth_clients WHERE client_id = $1`, id) })
	return id
}

func (a *PrivateApp) SitesOf(t *testing.T, p Person) []any {
	t.Helper()
	r := a.At(t, "GET", SignupApex, "/v1/sites", nil, map[string]string{"X-API-Key": p.Key, "X-Skill-Version": "1.0.0"})
	var out []any
	if err := json.Unmarshal(r.Body, &out); err != nil {
		t.Fatalf("sites: %s", r.Body)
	}
	return out
}
