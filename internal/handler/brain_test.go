package handler

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/internal/auth"
	"github.com/vsriram/simple-host/internal/brain"
	db "github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/mcp"
)

// brainFixture builds a small bundle from two throwaway git checkouts.
func brainFixture(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	parity := "# PARITY\n\n## Feature table\n\n| Area | Hosted | Enterprise | Status |\n|---|---|---|---|\n" +
		"| WordPress import (2026-10-14) | `create_import_link` | Not built | `gap → enterprise` |\n"
	files := map[string]map[string]string{
		"host": {
			"INTENT.md":    "# INTENT\n\n## What this is, and why it exists\n\nSimple Host hosts sites.\n\n## Non-goals\n\n- Server code.\n\n## Decisions already made\n\n- **2026-10-14. Comments use Cloudflare Turnstile.** Never home-built.\n",
			"FEATURES.md":  "# FEATURES\n\n## 1. Sites\n\n### WordPress import\n\nConverts an export.\n",
			"PARITY.md":    parity,
			"CHANGELOG.md": "# Changelog\n\n## 2026-10-16: Brain\n\n- Owner knowledge.\n",
		},
		"ent": {
			"INTENT.md":    "# INTENT\n\n## What this is, and why it exists\n\nEnterprise.\n\n## Decisions already made\n\n- **2026-10-15. Muse.** Because.\n",
			"FEATURES.md":  "# FEATURES\n\n## 1. Identity\n\nOIDC.\n",
			"PARITY.md":    parity,
			"CHANGELOG.md": "# Changelog\n\n## v0.9.7 — 2026-10-15\n\n- Muse.\n",
		},
	}
	for repo, fs := range files {
		dir := filepath.Join(root, repo)
		for name, body := range fs {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}, {"-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "-qm", "x"}} {
			if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v %s", args, err, out)
			}
		}
	}
	b, err := brain.Build(filepath.Join(root, "host"), filepath.Join(root, "ent"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(root, "bundle.json")
	if err := brain.WriteFile(b, out); err != nil {
		t.Fatal(err)
	}
	return out
}

func newBrainApp(t *testing.T) *connectorApp {
	a := newConnectorApp(t)
	adminID, err := db.EnsureAdminUser(context.Background(), a.database)
	if err != nil {
		t.Fatal(err)
	}
	authMW := auth.Middleware(a.admin, adminID, a.database)
	a.conn.EnableBrain(a.mux, authMW, brainFixture(t))
	return a
}

func (a *connectorApp) makeAdmin(t *testing.T, p person, on bool) {
	t.Helper()
	if _, err := a.database.Exec(`UPDATE users SET is_admin = $1 WHERE username = $2`, on, p.email); err != nil {
		t.Fatal(err)
	}
}

// connectBrain runs the OAuth flow for resource /brain/mcp.
func (a *connectorApp) connectBrain(t *testing.T, p person, clientID string) (string, resp) {
	t.Helper()
	verifier, challenge := newVerifier()
	q := authorizeQuery(clientID, testRedirect, challenge)
	q.Set("resource", a.srv.URL+"/brain/mcp")
	page := a.do(t, http.MethodGet, "/oauth/authorize?"+q.Encode(), nil, nil)
	m := connectDataRe.FindSubmatch(page.body)
	if page.status != http.StatusOK || m == nil || !strings.Contains(string(m[1]), `"brain":true`) {
		t.Fatalf("brain consent page: %d %s", page.status, m)
	}
	csrf := strings.Split(strings.Split(string(m[1]), `"csrf":"`)[1], `"`)[0]
	r := a.do(t, http.MethodPost, "/oauth/authorize/decision", jsonBody(map[string]string{
		"query": q.Encode(), "csrf": csrf, "decision": "allow",
	}), map[string]string{"Content-Type": "application/json", "X-API-Key": p.key, "Origin": a.srv.URL})
	if r.status != http.StatusOK {
		return "", r
	}
	code := codeFrom(t, r.json(t)["redirect_to"].(string))
	tok := a.form(t, "/oauth/token", url.Values{
		"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {testRedirect},
		"client_id": {clientID}, "code_verifier": {verifier}, "resource": {a.srv.URL + "/brain/mcp"},
	}, nil)
	if tok.status != http.StatusOK {
		t.Fatalf("token: %d %s", tok.status, tok.body)
	}
	return tok.json(t)["access_token"].(string), tok
}

func (a *connectorApp) brainRPC(t *testing.T, token, method string, params any) resp {
	t.Helper()
	return a.do(t, http.MethodPost, "/brain/mcp", jsonBody(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params}),
		map[string]string{"Content-Type": "application/json", "Authorization": "Bearer " + token, "MCP-Protocol-Version": "2025-06-18"})
}

func TestBrainOwnerOnlyAndAudience(t *testing.T) {
	a := newBrainApp(t)
	owner := a.newPerson(t, "brain-owner")
	a.makeAdmin(t, owner, true)
	other := a.newPerson(t, "brain-other")
	client := a.registerClient(t, testRedirect)

	// Discovery: an unauthenticated probe learns the brain resource.
	r := a.do(t, http.MethodPost, "/brain/mcp", jsonBody(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"}), map[string]string{"Content-Type": "application/json"})
	if r.status != http.StatusUnauthorized || !strings.Contains(r.header.Get("WWW-Authenticate"), "/.well-known/oauth-protected-resource/brain/mcp") {
		t.Fatalf("challenge: %d %q", r.status, r.header.Get("WWW-Authenticate"))
	}
	meta := a.do(t, http.MethodGet, "/.well-known/oauth-protected-resource/brain/mcp", nil, nil).json(t)
	if meta["resource"] != a.srv.URL+"/brain/mcp" {
		t.Fatalf("metadata resource %v", meta["resource"])
	}

	// A non-admin cannot get a brain code at all.
	if _, r := a.connectBrain(t, other, client); r.status != http.StatusForbidden || !strings.Contains(string(r.body), "owner") {
		t.Fatalf("non-admin consent: %d %s", r.status, r.body)
	}

	token, _ := a.connectBrain(t, owner, client)
	list := a.brainRPC(t, token, "tools/list", map[string]any{})
	tools, _ := list.json(t)["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 7 {
		t.Fatalf("tools/list: %s", list.body)
	}
	init := a.brainRPC(t, token, "initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "t", "version": "1"}})
	if !strings.Contains(string(init.body), "private product knowledge") {
		t.Errorf("instructions: %s", init.body)
	}

	// The brain token is for /brain/mcp only: refused on /mcp and /v1.
	if r := a.rpc(t, token, "tools/list", map[string]any{}); r.status != http.StatusUnauthorized {
		t.Errorf("brain token at /mcp: %d", r.status)
	}
	if r := a.do(t, http.MethodGet, "/v1/me", nil, map[string]string{"Authorization": "Bearer " + token}); r.status != http.StatusUnauthorized {
		t.Errorf("brain token on /v1: %d", r.status)
	}
	// A public connector token, even the owner's, is refused at /brain/mcp.
	public := a.connect(t, owner, client, testRedirect)["access_token"].(string)
	if r := a.brainRPC(t, public, "tools/list", map[string]any{}); r.status != http.StatusUnauthorized {
		t.Errorf("public connector token at /brain/mcp: %d %s", r.status, r.body)
	}
	// An API key is not a way in to /brain/mcp, not even the owner's.
	if r := a.do(t, http.MethodPost, "/brain/mcp", jsonBody(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"}),
		map[string]string{"Content-Type": "application/json", "X-API-Key": owner.key}); r.status != http.StatusUnauthorized {
		t.Errorf("API key at /brain/mcp: %d", r.status)
	}

	// Demoted after connecting: 403, plain message, no tools.
	a.makeAdmin(t, owner, false)
	r = a.brainRPC(t, token, "tools/list", map[string]any{})
	if r.status != http.StatusForbidden || strings.Contains(string(r.body), "product_overview") || !strings.Contains(string(r.body), "owner") {
		t.Errorf("demoted owner: %d %s", r.status, r.body)
	}
	a.makeAdmin(t, owner, true)

	// REST mirror: the owner's key works, another person's does not.
	if r := a.do(t, http.MethodPost, "/v1/brain/decisions", jsonBody(map[string]any{"query": "Turnstile"}), map[string]string{"X-API-Key": owner.key, "Content-Type": "application/json"}); r.status != http.StatusOK || !strings.Contains(string(r.body), "2026-10-14") {
		t.Errorf("REST owner: %d %s", r.status, r.body)
	}
	for _, path := range []string{"/v1/brain/search", "/v1/brain/get_doc"} {
		if r := a.do(t, http.MethodPost, path, jsonBody(map[string]any{"query": "x"}), map[string]string{"X-API-Key": other.key, "Content-Type": "application/json"}); r.status != http.StatusForbidden || strings.Contains(string(r.body), "Turnstile") {
			t.Errorf("REST non-admin %s: %d %s", path, r.status, r.body)
		}
	}
	if r := a.do(t, http.MethodGet, "/v1/brain", nil, map[string]string{"X-API-Key": other.key}); r.status != http.StatusForbidden || strings.Contains(string(r.body), "product_overview") {
		t.Errorf("REST index non-admin: %d %s", r.status, r.body)
	}
	if r := a.do(t, http.MethodGet, "/v1/brain", nil, nil); r.status != http.StatusUnauthorized {
		t.Errorf("REST anonymous: %d", r.status)
	}
	if r := a.do(t, http.MethodPost, "/v1/brain/get_doc", jsonBody(map[string]any{"path": "../../etc/passwd"}), map[string]string{"X-API-Key": owner.key, "Content-Type": "application/json"}); r.status != http.StatusBadRequest {
		t.Errorf("REST traversal: %d %s", r.status, r.body)
	}
	if r := a.do(t, http.MethodPost, "/v1/brain/search", jsonBody(map[string]any{"query": "x", "extra": 1}), map[string]string{"X-API-Key": owner.key, "Content-Type": "application/json"}); r.status != http.StatusBadRequest {
		t.Errorf("REST unexpected argument: %d", r.status)
	}
	// A deploy-only key of the owner carries no admin powers.
	deployKey, _ := auth.GenerateAPIKey()
	if _, err := a.database.ExecContext(context.Background(), `INSERT INTO api_keys (user_id, key_hash, scope, name) SELECT id, $1, 'deploy', 'ci' FROM users WHERE username = $2`, db.HashAPIKey(deployKey), owner.email); err != nil {
		t.Fatal(err)
	}
	if r := a.do(t, http.MethodGet, "/v1/brain", nil, map[string]string{"X-API-Key": deployKey}); r.status == http.StatusOK {
		t.Errorf("deploy key read the brain")
	}
}

func TestBrainToolOutputsMatchSchemas(t *testing.T) {
	a := newBrainApp(t)
	owner := a.newPerson(t, "brain-schema")
	a.makeAdmin(t, owner, true)
	token, _ := a.connectBrain(t, owner, a.registerClient(t, testRedirect))
	schemas := map[string]any{}
	for _, tool := range mcp.BrainTools(func(string, map[string]any) (string, map[string]any, error) { return "", nil, nil }) {
		if err := mcp.CheckOutputSchema(tool.OutputSchema); err != nil {
			t.Errorf("%s schema: %v", tool.Name, err)
		}
		if tool.Annotations["readOnlyHint"] != true || tool.Annotations["destructiveHint"] != false {
			t.Errorf("%s hints: %v", tool.Name, tool.Annotations)
		}
		schemas[tool.Name] = tool.OutputSchema
	}
	calls := []struct {
		name string
		args map[string]any
	}{
		{"product_overview", map[string]any{"product": "host"}},
		{"product_overview", map[string]any{"product": "all"}},
		{"feature_lookup", map[string]any{"query": "WordPress importer"}},
		{"parity", map[string]any{}},
		{"parity", map[string]any{"feature": "WordPress"}},
		{"decisions", map[string]any{"query": "Turnstile"}},
		{"decisions", map[string]any{"since": "3d"}},
		{"recent_changes", map[string]any{"since": "2026-10-01"}},
		{"search", map[string]any{"query": "Turnstile comments"}},
		{"get_doc", map[string]any{"path": "simple-host/INTENT.md"}},
		{"get_doc", map[string]any{"path": "simple-host/INTENT.md", "section": "non-goals"}},
	}
	for _, c := range calls {
		r := a.brainRPC(t, token, "tools/call", map[string]any{"name": c.name, "arguments": c.args})
		text, structured, isErr := toolResultOf(t, r)
		if isErr || structured == nil || text == "" {
			t.Errorf("%s %v: error %v %s", c.name, c.args, isErr, text)
			continue
		}
		if err := mcp.ValidateOutput(schemas[c.name], structured, nil); err != nil {
			t.Errorf("%s %v: %v", c.name, c.args, err)
		}
	}
	// A bad argument is a tool error the model can read, not a crash.
	r := a.brainRPC(t, token, "tools/call", map[string]any{"name": "get_doc", "arguments": map[string]any{"path": "simple-host/.env"}})
	if text, _, isErr := toolResultOf(t, r); !isErr || strings.Contains(text, "SECRET") {
		t.Errorf("get_doc .env: %v %s", isErr, text)
	}
}
