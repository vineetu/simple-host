package datatest

import (
	"encoding/json"
	"github.com/vsriram/simple-host/host/server/servertest"
	"io"
	"net/http"
	"testing"
)

// TestDeployKeyStorageRefusedAtScopeLayer locks in that a deploy-only key
// never reaches the storage handler: host/auth/scope.go's deployRoutes
// lists no storage route, so auth.ScopeGate (wired here exactly as
// cmd/server/main.go wires it, via newConnectorApp) answers 403
// deploy_only_key before site_storage.go's own owner check ever runs. If a
// storage route is ever added to deployRoutes, this fails with "wrong
// layer" instead of silently opening storage to CI secrets.
func TestDeployKeyStorageRefusedAtScopeLayer(t *testing.T) {
	a := servertest.NewConnectorApp(t)
	owner := a.NewPerson(t, "scope-owner")
	if r := a.Do(t, "POST", "/v1/sites/shop/files", servertest.JSONBody(map[string]any{
		"files": map[string]string{"index.html": "<h1>shop</h1>"},
	}), map[string]string{"X-API-Key": owner.Key}); r.Status != http.StatusCreated {
		t.Fatalf("deploy shop: %d %s", r.Status, r.Body)
	}
	if r := a.Do(t, "PUT", "/v1/sites/shop/storage/resources/vault", servertest.JSONBody(map[string]any{
		"kind": "kv",
	}), map[string]string{"X-API-Key": owner.Key}); r.Status != http.StatusCreated {
		t.Fatalf("owner create resource: %d %s", r.Status, r.Body)
	}

	mk := a.Do(t, "POST", "/v1/me/keys", servertest.JSONBody(map[string]any{"name": "ci", "scope": "deploy"}),
		map[string]string{"X-API-Key": owner.Key})
	if mk.Status != http.StatusCreated {
		t.Fatalf("mint deploy key: %d %s", mk.Status, mk.Body)
	}
	var minted map[string]any
	if err := json.Unmarshal(mk.Body, &minted); err != nil {
		t.Fatalf("mint deploy key: bad body: %s", mk.Body)
	}
	dk, _ := minted["api_key"].(string)
	if dk == "" {
		t.Fatalf("mint deploy key: no api_key in %s", mk.Body)
	}
	deploy := map[string]string{"X-API-Key": dk}

	for _, c := range []struct {
		method, path string
		body         any
	}{
		{"GET", "/v1/sites/shop/storage/resources", nil},
		{"PUT", "/v1/sites/shop/storage/resources/vault", map[string]any{"kind": "kv"}},
		{"DELETE", "/v1/sites/shop/storage/resources/vault", nil},
		{"GET", "/v1/sites/shop/storage/usage", nil},
	} {
		var rd io.Reader
		if c.body != nil {
			rd = servertest.JSONBody(c.body)
		}
		r := a.Do(t, c.method, c.path, rd, deploy)
		if r.Status != http.StatusForbidden {
			t.Fatalf("%s %s: got %d %s, want 403", c.method, c.path, r.Status, r.Body)
		}
		var e map[string]any
		if err := json.Unmarshal(r.Body, &e); err != nil {
			t.Fatalf("%s %s: bad error body: %s", c.method, c.path, r.Body)
		}
		if e["code"] != "deploy_only_key" {
			t.Fatalf("%s %s: refused at the wrong layer (code=%v, want deploy_only_key): a storage route reached the scope gate's allow list", c.method, c.path, e["code"])
		}
	}
}
