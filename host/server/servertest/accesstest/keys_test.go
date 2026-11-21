package accesstest

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	accountapp "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/access/accesshttp"
	"github.com/vsriram/simple-host/host/access/auth"
	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	model "github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/server/servertest"
)

// Keys one at a time: list (marking the caller's own), mint a named key shown
// once, revoke one, and sign out (the calling key dies). A connected app or
// the admin env key cannot mint, revoke or sign out. Needs DB_DSN.
func TestKeysListCreateRevokeSignOut(t *testing.T) {
	a := servertest.NewConnectorApp(t)
	p := a.NewPerson(t, "keys")
	other := a.NewPerson(t, "keys-other")
	h := func(k string) map[string]string {
		return map[string]string{"X-API-Key": k, "Content-Type": "application/json"}
	}
	me := func(k string) servertest.Resp { return a.Do(t, http.MethodGet, "/v1/me", nil, h(k)) }

	// A key from before names existed: no name, no last4, marked current, and
	// the lookup that authenticated the list has already stamped last_used_at.
	ks := servertest.KeysOf(t, a, p.Key)
	if len(ks) != 1 || ks[0]["current"] != true || ks[0]["name"] != nil || ks[0]["last4"] != nil || ks[0]["last_used_at"] == nil {
		t.Fatalf("earlier key listed as %v", ks)
	}

	if r := a.Do(t, http.MethodPost, "/v1/me/keys", servertest.JSONBody(map[string]string{"name": "  "}), h(p.Key)); r.Status != http.StatusBadRequest {
		t.Fatalf("unnamed key: %d", r.Status)
	}
	if r := a.Do(t, http.MethodPost, "/v1/me/keys", servertest.JSONBody(map[string]string{"name": strings.Repeat("x", 61)}), h(p.Key)); r.Status != http.StatusBadRequest {
		t.Fatalf("over-long name: %d", r.Status)
	}
	// Zero-width and bidi-override characters (Unicode Cf) are refused too.
	for _, bad := range []string{"CI\u200bkey", "laptop\u202etxt.exe"} {
		if r := a.Do(t, http.MethodPost, "/v1/me/keys", servertest.JSONBody(map[string]string{"name": bad}), h(p.Key)); r.Status != http.StatusBadRequest {
			t.Fatalf("name %q: %d %s", bad, r.Status, r.Body)
		}
	}
	r := a.Do(t, http.MethodPost, "/v1/me/keys", servertest.JSONBody(map[string]string{"name": "GitHub Actions"}), h(p.Key))
	if r.Status != http.StatusCreated {
		t.Fatalf("create key: %d %s", r.Status, r.Body)
	}
	made := r.JSON(t)
	ci, _ := made["api_key"].(string)
	if !strings.HasPrefix(ci, auth.APIKeyPrefix) || len(ci) != len(auth.APIKeyPrefix)+64 || made["last4"] != ci[len(ci)-4:] || made["name"] != "GitHub Actions" {
		t.Fatalf("minted key: %v", made)
	}
	if me(ci).Status != http.StatusOK {
		t.Fatal("minted key does not work")
	}
	ks = servertest.KeysOf(t, a, p.Key)
	if len(ks) != 2 || ks[0]["name"] != "GitHub Actions" || ks[0]["current"] != false || ks[1]["current"] != true {
		t.Fatalf("after mint: %v", ks)
	}
	ciID, _ := ks[0]["id"].(string)

	// Someone else's key id reads as missing and nothing is deleted.
	if r := a.Do(t, http.MethodDelete, "/v1/me/keys/"+ciID, nil, h(other.Key)); r.Status != http.StatusNotFound {
		t.Fatalf("other account revoked my key: %d", r.Status)
	}
	if r := a.Do(t, http.MethodDelete, "/v1/me/keys/not-a-uuid", nil, h(p.Key)); r.Status != http.StatusNotFound {
		t.Fatalf("bad id: %d", r.Status)
	}
	if r := a.Do(t, http.MethodDelete, "/v1/me/keys/"+ciID, nil, h(p.Key)); r.Status != http.StatusNoContent {
		t.Fatalf("revoke: %d %s", r.Status, r.Body)
	}
	if r := me(ci); r.Status != http.StatusUnauthorized || r.JSON(t)["code"] != "invalid_api_key" {
		t.Fatalf("revoked key: %d %s", r.Status, r.Body)
	}
	if me(p.Key).Status != http.StatusOK {
		t.Fatal("revoking one key killed another")
	}

	// A connected app (internal credential) cannot mint, revoke or sign out.
	u, err := accountapp.New(a.Database).GetUserByAPIKey(context.Background(), p.Key, 180*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	ik, revoke, err := accountsrepo.IssueInternalKey(u.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer revoke()
	for _, c := range []struct{ method, path string }{
		{http.MethodPost, "/v1/me/keys"}, {http.MethodPost, "/v1/me/sign-out"}, {http.MethodDelete, "/v1/me/keys/" + ciID},
	} {
		r := a.Do(t, c.method, c.path, servertest.JSONBody(map[string]string{"name": "sneaky"}), h(ik))
		if r.Status != http.StatusBadRequest || r.JSON(t)["code"] != "not_an_account_key" {
			t.Fatalf("%s %s with a connected app: %d %s", c.method, c.path, r.Status, r.Body)
		}
	}
	if ks := servertest.KeysOf(t, a, ik); len(ks) != 1 || ks[0]["current"] != false {
		t.Fatalf("list via connected app: %v", ks)
	}

	// Sign out ends exactly the key it was sent with.
	second := a.Do(t, http.MethodPost, "/v1/me/keys", servertest.JSONBody(map[string]string{"name": "laptop"}), h(p.Key)).JSON(t)["api_key"].(string)
	if r := a.Do(t, http.MethodPost, "/v1/me/sign-out", nil, h(p.Key)); r.Status != http.StatusNoContent {
		t.Fatalf("sign out: %d %s", r.Status, r.Body)
	}
	if me(p.Key).Status != http.StatusUnauthorized || me(second).Status != http.StatusOK {
		t.Fatal("sign-out must end only the calling key")
	}

	// Owner-route 401s carry a code.
	if r := a.Do(t, http.MethodGet, "/v1/me/keys", nil, nil); r.Status != http.StatusUnauthorized || r.JSON(t)["code"] != "missing_api_key" {
		t.Fatalf("no key: %d %s", r.Status, r.Body)
	}
}

// Sign-in names the key it issues: a typed code defaults to "agent sign-in",
// a link to "dashboard sign-in", and the caller may name it.
func TestSignInNamesItsKey(t *testing.T) {
	a := servertest.NewConnectorApp(t)
	ann := a.NewPerson(t, "named")
	verify := func(code string, body map[string]string) string {
		t.Helper()
		lt, _ := auth.GenerateAPIKey()
		if err := accountsrepo.CreateAuthToken(context.Background(), a.Database, ann.Email, code, lt, time.Now().Add(time.Minute), "dashboard", sql.NullString{}, sql.NullString{}); err != nil {
			t.Fatal(err)
		}
		body["email"], body["code"] = ann.Email, code
		r := a.Do(t, http.MethodPost, "/v1/auth/verify", servertest.JSONBody(body), map[string]string{"Content-Type": "application/json"})
		if r.Status != http.StatusOK {
			t.Fatalf("verify: %d %s", r.Status, r.Body)
		}
		k, _ := r.JSON(t)["api_key"].(string)
		if !strings.HasPrefix(k, auth.APIKeyPrefix) {
			t.Fatalf("sign-in key without prefix: %q", k[:6])
		}
		return k
	}
	agent := verify("333333", map[string]string{})
	dash := verify("444444", map[string]string{"name": "dashboard sign-in"})
	names := map[string]string{}
	for _, k := range servertest.KeysOf(t, a, dash) {
		l4, _ := k["last4"].(string)
		n, _ := k["name"].(string)
		names[l4+"|"+n] = "ok"
	}
	if names[agent[len(agent)-4:]+"|agent sign-in"] == "" || names[dash[len(dash)-4:]+"|dashboard sign-in"] == "" {
		t.Fatalf("names: %v", names)
	}
}

// The admin replaces a participant's keys with one new key, shown once.
func TestAdminReissuesKey(t *testing.T) {
	a := servertest.NewConnectorApp(t)
	p := a.NewPerson(t, "lost")
	nobody := a.NewPerson(t, "nobody")
	var id, adminID string
	if err := a.Database.QueryRow(`SELECT id FROM users WHERE username = $1`, p.Email).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if err := a.Database.QueryRow(`SELECT id FROM users WHERE username = 'admin'`).Scan(&adminID); err != nil {
		t.Fatal(err)
	}
	reissue := func(uid, key string) servertest.Resp {
		return a.Do(t, http.MethodPost, "/v1/admin/users/"+uid+"/key", nil, map[string]string{"X-API-Key": key})
	}
	if r := reissue(id, nobody.Key); r.Status != http.StatusNotFound {
		t.Fatalf("non-admin reissue: %d", r.Status)
	}
	if r := reissue(adminID, a.Admin); r.Status != http.StatusBadRequest {
		t.Fatalf("admin account reissue: %d", r.Status)
	}
	var logged bytes.Buffer
	log.SetOutput(io.MultiWriter(&logged, os.Stderr))
	r := reissue(id, a.Admin)
	log.SetOutput(os.Stderr)
	if r.Status != http.StatusOK {
		t.Fatalf("reissue: %d %s", r.Status, r.Body)
	}
	// The reissue is logged with the account and the admin who did it.
	if !regexp.MustCompile(`admin_key_reissue user_id=` + id + ` by=\S+`).Match(logged.Bytes()) {
		t.Fatalf("reissue not logged: %q", logged.String())
	}
	fresh, _ := r.JSON(t)["api_key"].(string)
	if a.Do(t, http.MethodGet, "/v1/me", nil, map[string]string{"X-API-Key": p.Key}).Status != http.StatusUnauthorized {
		t.Fatal("old key survived a reissue")
	}
	ks := servertest.KeysOf(t, a, fresh)
	if len(ks) != 1 || ks[0]["name"] != model.KeyNameEvent || ks[0]["current"] != true {
		t.Fatalf("after reissue: %v", ks)
	}
	var event bool
	if err := a.Database.QueryRow(`SELECT event_account FROM users WHERE id = $1`, id).Scan(&event); err != nil || !event {
		t.Fatalf("reissue did not mark the event account: %v %v", event, err)
	}
}

// An account mints at most 50 keys from the Keys panel; the
// next one is 409 key_limit, and revoking one makes room again.
func TestCreateKeyLimit(t *testing.T) {
	a := servertest.NewConnectorApp(t)
	p := a.NewPerson(t, "many-keys")
	h := map[string]string{"X-API-Key": p.Key, "Content-Type": "application/json"}
	for i := len(servertest.KeysOf(t, a, p.Key)); i < 50; i++ {
		if r := a.Do(t, http.MethodPost, "/v1/me/keys", servertest.JSONBody(map[string]string{"name": "k"}), h); r.Status != http.StatusCreated {
			t.Fatalf("key %d: %d %s", i, r.Status, r.Body)
		}
	}
	r := a.Do(t, http.MethodPost, "/v1/me/keys", servertest.JSONBody(map[string]string{"name": "one too many"}), h)
	if r.Status != http.StatusConflict || r.JSON(t)["code"] != "key_limit" {
		t.Fatalf("over the limit: %d %s", r.Status, r.Body)
	}
	ks := servertest.KeysOf(t, a, p.Key)
	if len(ks) != 50 {
		t.Fatalf("keys held: %d", len(ks))
	}
	id, _ := ks[0]["id"].(string)
	if r := a.Do(t, http.MethodDelete, "/v1/me/keys/"+id, nil, h); r.Status != http.StatusNoContent {
		t.Fatalf("revoke: %d", r.Status)
	}
	if r := a.Do(t, http.MethodPost, "/v1/me/keys", servertest.JSONBody(map[string]string{"name": "room again"}), h); r.Status != http.StatusCreated {
		t.Fatalf("after revoke: %d %s", r.Status, r.Body)
	}
}

// A key revoked while a mint made with it is in flight cannot mint a
// successor: the mint waits on the revoke and then finds its key gone.
func TestCreateKeyRacesRevoke(t *testing.T) {
	a := servertest.NewConnectorApp(t)
	p := a.NewPerson(t, "mint-race")
	ctx := context.Background()
	u, err := accountapp.New(a.Database).GetUserByAPIKey(ctx, p.Key, 180*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := a.Database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := accountsrepo.DeleteAPIKeyByHash(ctx, tx, u.ID, u.KeyHash); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		plain, _ := auth.GenerateAPIKey()
		_, err := accountapp.New(a.Database).CreateKey(ctx, u.ID, u.KeyHash, plain, "racer", "", nil, 50, 180*24*time.Hour)
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("mint did not wait for the revoke: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("mint after revoke: %v", err)
	}
	var n int
	if err := a.Database.QueryRow(`SELECT count(*) FROM api_keys WHERE user_id = $1`, u.ID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("keys left: %d %v", n, err)
	}
	// Through the route, a revoked key is a 401 invalid_api_key.
	r := a.Do(t, http.MethodPost, "/v1/me/keys", servertest.JSONBody(map[string]string{"name": "late"}), map[string]string{"X-API-Key": p.Key, "Content-Type": "application/json"})
	if r.Status != http.StatusUnauthorized || r.JSON(t)["code"] != "invalid_api_key" {
		t.Fatalf("mint with revoked key: %d %s", r.Status, r.Body)
	}
}

// Sign out everywhere with a key revoked after it was checked (between the
// auth middleware and the rotation) answers 401 invalid_api_key, not 500, and
// mints nothing.
func TestRotateWithKeyRevokedMidRequest(t *testing.T) {
	a := servertest.NewConnectorApp(t)
	p := a.NewPerson(t, "rotate-race")
	u, err := accountapp.New(a.Database).GetUserByAPIKey(context.Background(), p.Key, 180*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	users := accesshttp.NewUserHandler(a.Database, accountapp.New(a.Database), nil, a.Srv.URL)
	h := auth.Middleware("", "", accountapp.New(a.Database), 180*24*time.Hour)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := a.Database.Exec(`DELETE FROM api_keys WHERE key_hash = $1`, u.KeyHash); err != nil {
			t.Fatal(err)
		}
		users.RotateAPIKey(w, r)
	}))
	req := httptest.NewRequest(http.MethodPost, "/v1/me/api-key/rotate", nil)
	req.Header.Set("X-API-Key", p.Key)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), `"invalid_api_key"`) {
		t.Fatalf("rotate with a revoked key: %d %s", rec.Code, rec.Body.String())
	}
	var n int
	if err := a.Database.QueryRow(`SELECT count(*) FROM api_keys WHERE user_id = $1`, u.ID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("keys after a refused rotate: %d %v", n, err)
	}
}
