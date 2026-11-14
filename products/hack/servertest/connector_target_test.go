package servertest

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	accountapp "github.com/vsriram/simple-host/internal/app/accounts"
	"github.com/vsriram/simple-host/internal/extension"
	"github.com/vsriram/simple-host/internal/repository/sqlkit"
	"github.com/vsriram/simple-host/internal/server"
	platformtest "github.com/vsriram/simple-host/internal/server/servertest"
	"github.com/vsriram/simple-host/internal/testdb"
	"github.com/vsriram/simple-host/products/hack"
	"github.com/vsriram/simple-host/products/hack/repository/hackteams"
)

type observedTarget struct {
	hackteams.ConnectorTarget
	reads, writes int
	err           error
}

func (h *observedTarget) Selected(ctx context.Context, q sqlkit.Querier, grant string) (sql.NullString, error) {
	h.reads++
	if h.err != nil {
		return sql.NullString{}, h.err
	}
	return h.ConnectorTarget.Selected(ctx, q, grant)
}
func (h *observedTarget) Select(ctx context.Context, q sqlkit.Querier, grant, user, team string) (bool, error) {
	h.writes++
	return h.ConnectorTarget.Select(ctx, q, grant, user, team)
}

func TestConnectorTargetAfterBuildRESTAndMCP(t *testing.T) {
	cfg := platformtest.Config(t, testdb.Fresh(t))
	db, err := sql.Open("postgres", cfg.DBDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	seed, err := os.ReadFile("../../../scripts/split/seed/seed.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(strings.TrimPrefix(string(seed), "\\set ON_ERROR_STOP on\n")); err != nil {
		t.Fatal(err)
	}
	const grant = "00000000-0000-4000-8000-000000000031"
	const team = "00000000-0000-4000-8000-000000000011"
	const other = "00000000-0000-4000-8000-000000000012"
	const token = "shat_build-target-test"
	sum := sha256.Sum256([]byte(token))
	if _, err := db.Exec(`UPDATE oauth_grants SET resource=$1,selected_team_id=$2 WHERE id=$3`, cfg.PublicBaseURL, other, grant); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO hack_grant_teams (grant_id,team_id) VALUES ($1,$2)`, grant, team); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO oauth_tokens (token_hash,grant_id,kind,expires_at) VALUES ($1,$2,'access',now()+interval '1 hour')`, hex.EncodeToString(sum[:]), grant); err != nil {
		t.Fatal(err)
	}
	observed := &observedTarget{}
	ext := hack.Extension()
	early := ext.Early
	ext.Early = func(d extension.EarlyDeps) extension.EarlyHooks {
		hooks := early(d)
		if _, ok := hooks.Accounts.Target.(hackteams.ConnectorTarget); !ok {
			t.Fatal("Hack early target missing")
		}
		hooks.Accounts.Target = observed
		return hooks
	}
	app, err := server.Build(cfg, []*extension.Extension{ext})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { accountapp.SetGroupHooks(accountapp.GroupHooks{}) })
	t.Cleanup(app.Close)
	request := func(app *server.App, method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, cfg.PublicBaseURL+path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		app.Handler.ServeHTTP(w, r)
		return w
	}
	// REST authentication and the Connected AI apps card both use the early hook.
	w := request(app, http.MethodGet, "/v1/me/connections", "")
	if w.Code != 200 || observed.reads == 0 || !strings.Contains(w.Body.String(), "Seed Live") || strings.Contains(w.Body.String(), "Seed Trash") {
		t.Fatalf("REST hook: %d %s; reads=%d", w.Code, w.Body.String(), observed.reads)
	}
	rpc := func(name string, args map[string]any) *httptest.ResponseRecorder {
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": name, "arguments": args}})
		return request(app, http.MethodPost, "/mcp", string(b))
	}
	before := observed.reads
	w = rpc("hack_select_team", map[string]any{"team_id": team})
	if w.Code != 200 || observed.reads <= before || observed.writes != 1 || !strings.Contains(w.Body.String(), "Selected team") {
		t.Fatalf("MCP hook: %d %s", w.Code, w.Body.String())
	}
	var equal bool
	if err := db.QueryRow(`SELECT g.selected_team_id=h.team_id FROM oauth_grants g JOIN hack_grant_teams h ON h.grant_id=g.id WHERE g.id=$1`, grant).Scan(&equal); err != nil || !equal {
		t.Fatalf("MCP double write: %v %v", equal, err)
	}
	// Loss of membership still refuses use of the stored choice on the MCP path.
	if _, err := db.Exec(`DELETE FROM event_members WHERE user_id='00000000-0000-4000-8000-000000000001'`); err != nil {
		t.Fatal(err)
	}
	w = rpc("hack_select_team", map[string]any{"team_id": team})
	if w.Code != 200 || !strings.Contains(w.Body.String(), "currently belong") {
		t.Fatalf("membership lost: %d %s", w.Code, w.Body.String())
	}
	observed.err = errors.New("selection lookup failed")
	for _, path := range []string{"/v1/me", "/mcp"} {
		method, body := http.MethodGet, ""
		if path == "/mcp" {
			method = http.MethodPost
			body = `{"jsonrpc":"2.0","id":1,"method":"ping","params":{}}`
		}
		if w := request(app, method, path, body); w.Code != 401 {
			t.Fatalf("hook error at %s: %d %s", path, w.Code, w.Body.String())
		}
	}
	app.Close()
	// Build without a product clears the process hook; even a stale old column
	// does not send a platform-only request back into Hack.
	host, err := server.Build(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(host.Close)
	before = observed.reads
	w = request(host, http.MethodGet, "/v1/me", "")
	if w.Code != 200 || observed.reads != before {
		t.Fatalf("Host nil hook: %d %s", w.Code, w.Body.String())
	}
}
