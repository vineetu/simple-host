package servertest

import (
	"net/http"
	"testing"
)

func KeysOf(t *testing.T, a *ConnectorApp, key string) []map[string]any {
	t.Helper()
	r := a.Do(t, http.MethodGet, "/v1/me/keys", nil, map[string]string{"X-API-Key": key})
	if r.Status != http.StatusOK {
		t.Fatalf("list keys: %d %s", r.Status, r.Body)
	}
	raw, _ := r.JSON(t)["keys"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, k := range raw {
		out = append(out, k.(map[string]any))
	}
	return out
}
