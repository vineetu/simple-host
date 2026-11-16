package servertest

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	accountapp "github.com/vsriram/simple-host/internal/app/accounts"
	appstorage "github.com/vsriram/simple-host/internal/app/storage"
	"github.com/vsriram/simple-host/internal/extension"
	"github.com/vsriram/simple-host/internal/httpx"
	"github.com/vsriram/simple-host/internal/model"
	accountrepo "github.com/vsriram/simple-host/internal/repository/accounts"
	"github.com/vsriram/simple-host/internal/repository/sqlkit"
	platformtest "github.com/vsriram/simple-host/internal/server/servertest"
	"github.com/vsriram/simple-host/products/hack"
	"github.com/vsriram/simple-host/products/hack/repository/hackevents"
	"github.com/vsriram/simple-host/products/hack/testdb"
)

const keyMember = "00000000-0000-4000-8000-000000000001"
const keyHolder = "00000000-0000-4000-8000-000000000003"
const keyOrganiser = "00000000-0000-4000-8000-000000000004"
const keyEvent = "00000000-0000-4000-8000-000000000010"
const keyTeam = "00000000-0000-4000-8000-000000000011"
const keySite = "00000000-0000-4000-8000-000000000021"
const keyRaw = "shk_group-key-build-test"

func seedGroupKeys(t *testing.T, db *sql.DB) {
	t.Helper()
	seed, err := os.ReadFile("../../../scripts/split/seed/seed.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(strings.TrimPrefix(string(seed), "\\set ON_ERROR_STOP on\n")); err != nil {
		t.Fatal(err)
	}
	var keyID string
	if err := db.QueryRow(`INSERT INTO api_keys (key_hash,user_id,scope) VALUES ($1,$2,'team') RETURNING id`, accountrepo.HashAPIKey(keyRaw), keyMember).Scan(&keyID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO event_team_keys (key_id,event_id,team_id,user_id) VALUES ($1,$2,$3,$4)`, keyID, keyEvent, keyTeam, keyMember); err != nil {
		t.Fatal(err)
	}
}

type observedKeys struct {
	hackevents.GroupKeys
	bindingErr, identityErr, writeErr error
}

func (h *observedKeys) Binding(ctx context.Context, q sqlkit.Querier, key string) (string, string, error) {
	if h.bindingErr != nil {
		return "", "", h.bindingErr
	}
	return h.GroupKeys.Binding(ctx, q, key)
}
func (h *observedKeys) Identity(ctx context.Context, q sqlkit.Querier, member, team string) (model.TeamIdentity, error) {
	if h.identityErr != nil {
		return model.TeamIdentity{}, h.identityErr
	}
	return h.GroupKeys.Identity(ctx, q, member, team)
}
func (h *observedKeys) WriteRefusal(ctx context.Context, q sqlkit.Querier, team string) (int, string, string, error) {
	if h.writeErr != nil {
		return 0, "", "", h.writeErr
	}
	return h.GroupKeys.WriteRefusal(ctx, q, team)
}

func TestGroupKeysAfterBuildRESTAndMCP(t *testing.T) {
	cfg := platformtest.Config(t, testdb.Fresh(t))
	db, err := sql.Open("postgres", cfg.DBDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	seedGroupKeys(t, db)
	observed := &observedKeys{}
	ext := hack.Extension()
	early := ext.Early
	ext.Early = func(d extension.EarlyDeps) extension.EarlyHooks {
		hooks := early(d)
		if _, ok := hooks.Accounts.Keys.(hackevents.GroupKeys); !ok {
			t.Fatal("Hack early keys missing")
		}
		hooks.Accounts.Keys = observed
		return hooks
	}
	t.Cleanup(func() { accountapp.SetGroupHooks(accountapp.GroupHooks{}) })
	app := platformtest.Build(t, cfg, []*extension.Extension{ext})
	request := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, cfg.PublicBaseURL+path, strings.NewReader(body))
		r.Header.Set("X-API-Key", keyRaw)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		app.Handler.ServeHTTP(w, r)
		return w
	}
	check := func(method, path string, status int, code string) {
		t.Helper()
		w := request(method, path, "")
		if w.Code != status || !strings.Contains(w.Body.String(), `"`+code+`"`) {
			t.Fatalf("%s %s: %d %s, want %d %s", method, path, w.Code, w.Body.String(), status, code)
		}
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	check("DELETE", "/v1/sites/seed-live", 403, "team_key_scope")
	w := request("POST", "/mcp", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"delete_site","arguments":{"site":"seed-live","confirm_name":"seed-live"}}}`)
	var rpc struct {
		Result struct {
			IsError bool `json:"isError"`
			Content []struct{ Text string }
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &rpc); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || !rpc.Result.IsError || !strings.Contains(w.Body.String(), "team_key_scope") {
		t.Fatalf("MCP scope: %d %s", w.Code, w.Body.String())
	}
	check("GET", "/v1/sites/wrong/storage/resources", 403, "team_site_only")
	if w := request("GET", "/v1/me", ""); w.Code != 200 {
		t.Fatalf("live: %d %s", w.Code, w.Body.String())
	}
	exec(`UPDATE event_members SET team_id=NULL WHERE user_id=$1`, keyMember)
	check("GET", "/v1/me", 401, "team_key_inactive")
	check("DELETE", "/v1/sites/seed-live", 403, "team_key_scope") // Scope wins over membership.
	exec(`UPDATE event_members SET team_id=$1 WHERE user_id=$2`, keyTeam, keyMember)
	exec(`UPDATE events SET submission_deadline=now()-interval '1 hour' WHERE id=$1`, keyEvent)
	check("PUT", "/v1/sites/seed-live/storage/resources/test", 409, "submissions_closed")
	exec(`UPDATE events SET taken_down_at=now() WHERE id=$1`, keyEvent)
	check("PUT", "/v1/sites/seed-live/storage/resources/test", 403, "event_taken_down")
	exec(`UPDATE events SET taken_down_at=NULL,submission_deadline=NULL WHERE id=$1`, keyEvent)
	for _, user := range []string{keyMember, keyHolder} {
		exec(`UPDATE users SET suspended_at=now() WHERE id=$1`, user)
		check("GET", "/v1/me", 403, "account_suspended")
		exec(`UPDATE users SET suspended_at=NULL WHERE id=$1`, user)
	}
	for _, field := range []*error{&observed.bindingErr, &observed.identityErr, &observed.writeErr} {
		*field = errors.New("injected group key error")
		check("PUT", "/v1/sites/seed-live/storage/resources/test", 500, "internal server error")
		*field = nil
	}
	app.Close()
	host := platformtest.Build(t, cfg, nil)
	r := httptest.NewRequest("GET", cfg.PublicBaseURL+"/v1/me", nil)
	r.Header.Set("X-API-Key", keyRaw)
	w = httptest.NewRecorder()
	host.Handler.ServeHTTP(w, r)
	if w.Code != 401 || !strings.Contains(w.Body.String(), "invalid_api_key") {
		t.Fatalf("Host team key: %d %s", w.Code, w.Body.String())
	}
}

func TestGroupKeyFileLinkRevocation(t *testing.T) {
	db, err := sql.Open("postgres", testdb.Fresh(t))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	seedGroupKeys(t, db)
	accountapp.SetGroupHooks(accountapp.GroupHooks{Keys: hackevents.GroupKeys{}})
	t.Cleanup(func() { accountapp.SetGroupHooks(accountapp.GroupHooks{}) })
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	const eventSite = "00000000-0000-4000-8000-000000000026"
	exec(`INSERT INTO sites (id,user_id,name) VALUES ($1,$2,$3)`, eventSite, keyHolder, keyEvent)
	filePath := filepath.Join(t.TempDir(), "note.txt")
	if err := os.WriteFile(filePath, []byte("private note"), 0600); err != nil {
		t.Fatal(err)
	}
	sealKey := []byte("group-key-file-link-test-secret")
	svc := appstorage.New(db, appstorage.Deps{
		OpenFileToken:      func(token string) ([]byte, error) { return httpx.OpenLink(sealKey, "site storage file v1", token) },
		FileResourceExists: func(context.Context, string, string) bool { return true },
		FilePath:           func(appstorage.Call, string) string { return filePath },
	})
	link := func(site, kind, id, member string, expiry time.Time) string {
		payload := keyHolder + "." + site + ".uploads." + base64.RawURLEncoding.EncodeToString([]byte("note.txt")) + "." + strconv.FormatInt(expiry.Unix(), 10) + "." + base64.RawURLEncoding.EncodeToString([]byte(kind+":"+id+":"+member))
		return httpx.SealLink(sealKey, "site storage file v1", []byte(payload))
	}
	check := func(token string, allowed bool) {
		t.Helper()
		f, _, _, err := svc.DownloadFile(context.Background(), token)
		if f != nil {
			defer f.Close()
		}
		if allowed {
			if err != nil {
				t.Fatalf("valid download: %v", err)
			}
			body, err := io.ReadAll(f)
			if err != nil || string(body) != "private note" {
				t.Fatalf("download: %q %v", body, err)
			}
		} else {
			var refusal *appstorage.Error
			if f != nil || !errors.As(err, &refusal) || refusal.Code != "file_link_invalid" {
				t.Fatalf("refused download: %v", err)
			}
		}
	}
	future := time.Now().Add(5 * time.Minute)
	teamLink := link(keySite, "team", keyTeam, keyMember, future)
	eventLink := link(eventSite, "event", keyEvent, keyOrganiser, future)
	check(teamLink, true)
	check(eventLink, true)
	exec(`UPDATE event_members SET team_id=NULL WHERE user_id=$1`, keyMember)
	check(teamLink, false)
	check(link(eventSite, "event", keyEvent, keyMember, future), false)
	exec(`UPDATE events SET taken_down_at=now() WHERE id=$1`, keyEvent)
	check(eventLink, false)
	exec(`UPDATE events SET taken_down_at=NULL WHERE id=$1`, keyEvent)
	check(link(eventSite, "event", keyEvent, keyOrganiser, time.Now().Add(-time.Minute)), false)
	accountapp.SetGroupHooks(accountapp.GroupHooks{})
	check(eventLink, false)
	exec(`UPDATE event_members SET team_id=$1 WHERE user_id=$2`, keyTeam, keyMember)
	check(teamLink, false)
}
