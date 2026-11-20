package servertest

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	accountapp "github.com/vsriram/simple-host/host/app/accounts"
	appsites "github.com/vsriram/simple-host/host/app/sites"
	"github.com/vsriram/simple-host/host/extension"
	"github.com/vsriram/simple-host/host/repository/sqlkit"
	platformtest "github.com/vsriram/simple-host/host/server/servertest"
	"github.com/vsriram/simple-host/products/hack"
	"github.com/vsriram/simple-host/products/hack/app/members"
	"github.com/vsriram/simple-host/products/hack/testdb"
)

type observedErasure struct {
	members.AccountErasure
	holds int
	err   error
	tx    *sql.Tx
	swept bool
	t     *testing.T
}

func (h *observedErasure) Holds(ctx context.Context, q sqlkit.Querier, id string) (bool, error) {
	h.holds++
	if h.err != nil {
		return false, h.err
	}
	return h.AccountErasure.Holds(ctx, q, id)
}
func (h *observedErasure) After(ctx context.Context, tx *sql.Tx, ids []string) ([]string, error) {
	h.tx = tx
	return h.AccountErasure.After(ctx, tx, ids)
}
func (h *observedErasure) Sweep(ctx context.Context, db *sql.DB, ids []string, trash func(context.Context, string, string) error) {
	h.swept = true
	if h.tx == nil {
		h.t.Fatal("sweep without After")
	}
	var n int
	if err := h.tx.QueryRowContext(ctx, "SELECT 1").Scan(&n); !errors.Is(err, sql.ErrTxDone) {
		h.t.Fatalf("sweep before commit: %v", err)
	}
	h.AccountErasure.Sweep(ctx, db, ids, trash)
}

func TestErasureAfterBuildRESTAndInProcess(t *testing.T) {
	cfg := platformtest.Config(t, testdb.Fresh(t))
	cfg.SiteDomain = tsDomain
	cfg.ContentHost = "sites." + tsDomain
	cfg.PublicBaseURL = "https://" + tsDomain
	cfg.PersonHosts = "canonical"
	cfg.SiteHosts = "canonical"
	cfg.SiteCertDir = ""
	hook := &observedErasure{t: t}
	ext := hack.Extension()
	early := ext.Early
	var upstream http.Handler
	a := &teamSiteApp{}
	wire := ext.Wire
	ext.Wire = func(d extension.Deps) (extension.Parts, error) {
		upstream = d.Connector.MCPUpstreamForTest()
		a.database = d.DB
		return wire(d)
	}
	ext.Early = func(d extension.EarlyDeps) extension.EarlyHooks {
		hooks := early(d)
		if _, ok := hooks.Accounts.Erasure.(members.AccountErasure); !ok {
			t.Fatal("missing early erasure")
		}
		hooks.Accounts.Erasure = hook
		return hooks
	}
	t.Cleanup(func() { accountapp.SetGroupHooks(accountapp.GroupHooks{}) })
	app := platformtest.Build(t, cfg, []*extension.Extension{ext})
	a.srv = httptest.NewServer(app.Handler)
	defer a.srv.Close()
	org := a.newPerson(t, "erasure-org")
	slug := a.makeEvent(t, org)
	var holder string
	if err := a.database.QueryRow(`SELECT account_id FROM events WHERE slug=$1`, slug).Scan(&holder); err != nil {
		t.Fatal(err)
	}
	organiser := a.uid(t, org)
	// HTTP and the in-process router used by MCP both consume the early hook.
	// There is no account-deletion MCP tool; exercise its router directly.
	request := func(inProcess bool, method, path, key, body string) (int, map[string]any) {
		t.Helper()
		req := httptest.NewRequest(method, cfg.PublicBaseURL+path, strings.NewReader(body))
		req.Header.Set("X-API-Key", key)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Forwarded-Proto", "https")
		w := httptest.NewRecorder()
		if inProcess {
			upstream.ServeHTTP(w, req)
		} else {
			app.Handler.ServeHTTP(w, req)
		}
		var out map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}
	check := func(inProcess bool, method, path, key, body string, status int, code, msg string) {
		t.Helper()
		got, out := request(inProcess, method, path, key, body)
		if got != status || (code != "" && out["code"] != code) || (msg != "" && out["error"] != msg) {
			t.Fatalf("%s %s: status=%d code=%v message=%v", method, path, got, out["code"], out["error"])
		}
	}
	for _, internal := range []bool{false, true} {
		check(internal, "DELETE", "/v1/admin/users/"+holder, cfg.AdminAPIKey, "", 409, "event_account", "this account holds an event; delete the event from the Events tab")
		check(internal, "POST", "/v1/admin/users/"+holder+"/suspend", cfg.AdminAPIKey, `{"reason":"fixture"}`, 409, "event_account", "this account holds an event; take the event down or restore it from the admin Events tab")
		check(internal, "DELETE", "/v1/me", org.key, `{"confirm":"`+strings.Split(org.email, "@")[0]+`"}`, 409, "organises_events", "you organise an event that has not ended; end it, or delete it while nobody has joined, before deleting the account")
		check(internal, "DELETE", "/v1/admin/users/"+organiser, cfg.AdminAPIKey, "", 409, "organises_events", "this account organises an event that has not ended; end it or delete it from the Events tab first")
	}
	if hook.holds == 0 {
		t.Fatal("holding hook never consulted")
	}
	hook.err = errors.New("injected holds failure")
	check(true, "DELETE", "/v1/admin/users/"+holder, cfg.AdminAPIKey, "", 500, "", "")
	check(true, "POST", "/v1/admin/users/"+holder+"/suspend", cfg.AdminAPIKey, `{"reason":"fixture"}`, 500, "", "")
	hook.err = nil
	// Issued accounts retain their flag and protections but are not holding accounts.
	var issued string
	if err := a.database.QueryRow(`INSERT INTO users(username,handle,event_account) VALUES ('issued@example.com','issued-erasure',true) RETURNING id`).Scan(&issued); err != nil {
		t.Fatal(err)
	}
	check(true, "POST", "/v1/admin/users/"+issued+"/key", cfg.AdminAPIKey, "", 200, "", "")
	check(true, "POST", "/v1/admin/users/"+issued+"/suspend", cfg.AdminAPIKey, `{"reason":"fixture"}`, 200, "", "")
	var flagged, suspended bool
	if err := a.database.QueryRow(`SELECT event_account,suspended_at IS NOT NULL FROM users WHERE id=$1`, issued).Scan(&flagged, &suspended); err != nil || !flagged || !suspended {
		t.Fatalf("issued flags: %t %t %v", flagged, suspended, err)
	}
	check(true, "DELETE", "/v1/admin/users/"+issued, cfg.AdminAPIKey, "", 204, "", "")
	if !hook.swept {
		t.Fatal("successful erasure did not sweep after commit")
	}
	if err := appsites.New(a.database, nil, nil, 0, appsites.Deps{}).SuspensionGate(context.Background(), holder); err == nil {
		t.Fatal("address gate allowed holder")
	}
}
