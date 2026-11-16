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

	accountapp "github.com/vsriram/simple-host/internal/app/accounts"
	appsites "github.com/vsriram/simple-host/internal/app/sites"
	"github.com/vsriram/simple-host/internal/extension"
	"github.com/vsriram/simple-host/internal/handler"
	"github.com/vsriram/simple-host/internal/model"
	accountrepo "github.com/vsriram/simple-host/internal/repository/accounts"
	"github.com/vsriram/simple-host/internal/repository/sqlkit"
	platformtest "github.com/vsriram/simple-host/internal/server/servertest"
	"github.com/vsriram/simple-host/products/hack"
	"github.com/vsriram/simple-host/products/hack/app/members"
	"github.com/vsriram/simple-host/products/hack/repository/hackevents"
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
	db, err := sql.Open("postgres", cfg.DBDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	seedGroupKeys(t, db)
	keys := map[string]string{keyHolder: "shk_erasure-holder", keyOrganiser: "shk_erasure-organiser"}
	for id, key := range keys {
		if err := accountrepo.AddAPIKey(context.Background(), db, id, key, model.KeyNameEvent); err != nil {
			t.Fatal(err)
		}
	}
	hook := &observedErasure{t: t}
	ext := hack.Extension()
	early := ext.Early
	var upstream http.Handler
	wire := ext.Wire
	ext.Wire = func(d extension.Deps) (extension.Parts, error) {
		upstream = d.Connector.MCPUpstreamForTest()
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
	// Reach the email refusal without configuring any outbound mail provider.
	handler.SetEmailSignIn(true)
	t.Cleanup(func() { handler.SetEmailSignIn(false) })
	server := httptest.NewServer(app.Handler)
	defer server.Close()
	// HTTP and the in-process router used by MCP both consume the early hook.
	// There is no account-deletion MCP tool; exercise its router directly.
	request := func(inProcess bool, method, path, key, body string) (int, map[string]any) {
		t.Helper()
		req := httptest.NewRequest(method, cfg.PublicBaseURL+path, strings.NewReader(body))
		req.Header.Set("X-API-Key", key)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		if inProcess {
			upstream.ServeHTTP(w, req)
		} else {
			req.RequestURI = ""
			req.URL.Scheme = "http"
			req.URL.Host = strings.TrimPrefix(server.URL, "http://")
			res, err := server.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			var out map[string]any
			_ = json.NewDecoder(res.Body).Decode(&out)
			return res.StatusCode, out
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
		check(internal, "DELETE", "/v1/me", keys[keyHolder], `{"confirm":"seed-rehearsal"}`, 409, "event_account", "this account holds an event; delete the event from the admin Events tab")
		check(internal, "DELETE", "/v1/admin/users/"+keyHolder, cfg.AdminAPIKey, "", 409, "event_account", "this account holds an event; delete the event from the Events tab")
		check(internal, "POST", "/v1/admin/users/"+keyHolder+"/key", cfg.AdminAPIKey, "", 409, "event_account", "this account holds an event and never signs in; manage the event from the admin Events tab")
		check(internal, "POST", "/v1/admin/users/"+keyHolder+"/suspend", cfg.AdminAPIKey, `{"reason":"fixture"}`, 409, "event_account", "this account holds an event; take the event down or restore it from the admin Events tab")
		check(internal, "POST", "/v1/me/email", keys[keyHolder], `{"email":"new@example.com"}`, 403, "event_account", "an event account's name is set by its organiser and cannot be changed")
		check(internal, "DELETE", "/v1/me", keys[keyOrganiser], `{"confirm":"seed-organiser"}`, 409, "organises_events", "you organise an event that has not ended; end it, or delete it while nobody has joined, before deleting the account")
		check(internal, "DELETE", "/v1/admin/users/"+keyOrganiser, cfg.AdminAPIKey, "", 409, "organises_events", "this account organises an event that has not ended; end it or delete it from the Events tab first")
	}
	if hook.holds != 12 {
		t.Fatalf("holding hook calls=%d", hook.holds)
	}
	hook.err = errors.New("injected holds failure")
	for _, path := range []string{"/v1/admin/users/" + keyHolder, "/v1/admin/users/" + keyHolder + "/key", "/v1/admin/users/" + keyHolder + "/suspend"} {
		method := "POST"
		body := `{"reason":"fixture"}`
		if !strings.Contains(path[len("/v1/admin/users/"):], "/") {
			method = "DELETE"
			body = ""
		}
		check(true, method, path, cfg.AdminAPIKey, body, 500, "", "")
	}
	hook.err = nil
	// Issued accounts retain their flag and protections but are not holding accounts.
	var issued string
	if err := db.QueryRow(`INSERT INTO users(username,handle,event_account) VALUES ('issued@example.com','issued-erasure',true) RETURNING id`).Scan(&issued); err != nil {
		t.Fatal(err)
	}
	check(true, "POST", "/v1/admin/users/"+issued+"/key", cfg.AdminAPIKey, "", 200, "", "")
	check(true, "POST", "/v1/admin/users/"+issued+"/suspend", cfg.AdminAPIKey, `{"reason":"fixture"}`, 200, "", "")
	var flagged, suspended bool
	if err := db.QueryRow(`SELECT event_account,suspended_at IS NOT NULL FROM users WHERE id=$1`, issued).Scan(&flagged, &suspended); err != nil || !flagged || !suspended {
		t.Fatalf("issued flags: %t %t %v", flagged, suspended, err)
	}
	check(true, "DELETE", "/v1/admin/users/"+issued, cfg.AdminAPIKey, "", 204, "", "")
	if !hook.swept {
		t.Fatal("successful erasure did not sweep after commit")
	}
	// Direct service callers keep the suspend wording too; the HTTP gate uses address.
	_, err = accountapp.New(db).SuspendAccount(context.Background(), accountapp.SuspensionInput{ID: keyHolder, HoldingAccounts: true})
	var refusal *accountapp.LifecycleError
	if !errors.As(err, &refusal) || refusal.Message != (members.AccountErasure{}).HoldingText("suspend") {
		t.Fatalf("suspend: %v", err)
	}
	if err = appsites.New(db, nil, nil, 0, appsites.Deps{}).SuspensionGate(context.Background(), keyHolder); err == nil {
		t.Fatal("address gate allowed holder")
	}
}

func TestEventDeletionRemovesHoldingAccountAtomically(t *testing.T) {
	db, err := sql.Open("postgres", testdb.Fresh(t))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	seedGroupKeys(t, db)
	ctx := context.Background()
	ev, err := hackevents.GetEventBySlug(ctx, db, "seed-rehearsal")
	if err != nil {
		t.Fatal(err)
	}
	for _, commit := range []bool{false, true} {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := hackevents.DeleteEventAndAccount(ctx, tx, ev); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		var n int
		if err := tx.QueryRow(`SELECT (SELECT count(*) FROM events WHERE id=$1)+(SELECT count(*) FROM users WHERE id=$2)`, ev.ID, ev.AccountID).Scan(&n); err != nil || n != 0 {
			tx.Rollback()
			t.Fatalf("transaction rows=%d err=%v", n, err)
		}
		if commit {
			err = tx.Commit()
		} else {
			err = tx.Rollback()
		}
		if err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow(`SELECT (SELECT count(*) FROM events WHERE id=$1)+(SELECT count(*) FROM users WHERE id=$2)`, ev.ID, ev.AccountID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		want := 2
		if commit {
			want = 0
		}
		if n != want {
			t.Fatalf("commit=%t rows=%d", commit, n)
		}
	}
}
