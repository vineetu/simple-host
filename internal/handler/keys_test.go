package handler

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/internal/auth"
	"github.com/vsriram/simple-host/internal/db"
)

func keysOf(t *testing.T, a *connectorApp, key string) []map[string]any {
	t.Helper()
	r := a.do(t, http.MethodGet, "/v1/me/keys", nil, map[string]string{"X-API-Key": key})
	if r.status != http.StatusOK {
		t.Fatalf("list keys: %d %s", r.status, r.body)
	}
	raw, _ := r.json(t)["keys"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, k := range raw {
		out = append(out, k.(map[string]any))
	}
	return out
}

// Keys one at a time: list (marking the caller's own), mint a named key shown
// once, revoke one, and sign out (the calling key dies). A connected app or
// the admin env key cannot mint, revoke or sign out. Needs DB_DSN.
func TestKeysListCreateRevokeSignOut(t *testing.T) {
	a := newConnectorApp(t)
	p := a.newPerson(t, "keys")
	other := a.newPerson(t, "keys-other")
	h := func(k string) map[string]string {
		return map[string]string{"X-API-Key": k, "Content-Type": "application/json"}
	}
	me := func(k string) resp { return a.do(t, http.MethodGet, "/v1/me", nil, h(k)) }

	// A key from before names existed: no name, no last4, marked current, and
	// the lookup that authenticated the list has already stamped last_used_at.
	ks := keysOf(t, a, p.key)
	if len(ks) != 1 || ks[0]["current"] != true || ks[0]["name"] != nil || ks[0]["last4"] != nil || ks[0]["last_used_at"] == nil {
		t.Fatalf("earlier key listed as %v", ks)
	}

	if r := a.do(t, http.MethodPost, "/v1/me/keys", jsonBody(map[string]string{"name": "  "}), h(p.key)); r.status != http.StatusBadRequest {
		t.Fatalf("unnamed key: %d", r.status)
	}
	if r := a.do(t, http.MethodPost, "/v1/me/keys", jsonBody(map[string]string{"name": strings.Repeat("x", 61)}), h(p.key)); r.status != http.StatusBadRequest {
		t.Fatalf("over-long name: %d", r.status)
	}
	// Zero-width and bidi-override characters (Unicode Cf) are refused too.
	for _, bad := range []string{"CI\u200bkey", "laptop\u202etxt.exe"} {
		if r := a.do(t, http.MethodPost, "/v1/me/keys", jsonBody(map[string]string{"name": bad}), h(p.key)); r.status != http.StatusBadRequest {
			t.Fatalf("name %q: %d %s", bad, r.status, r.body)
		}
	}
	r := a.do(t, http.MethodPost, "/v1/me/keys", jsonBody(map[string]string{"name": "GitHub Actions"}), h(p.key))
	if r.status != http.StatusCreated {
		t.Fatalf("create key: %d %s", r.status, r.body)
	}
	made := r.json(t)
	ci, _ := made["api_key"].(string)
	if !strings.HasPrefix(ci, auth.APIKeyPrefix) || len(ci) != len(auth.APIKeyPrefix)+64 || made["last4"] != ci[len(ci)-4:] || made["name"] != "GitHub Actions" {
		t.Fatalf("minted key: %v", made)
	}
	if me(ci).status != http.StatusOK {
		t.Fatal("minted key does not work")
	}
	ks = keysOf(t, a, p.key)
	if len(ks) != 2 || ks[0]["name"] != "GitHub Actions" || ks[0]["current"] != false || ks[1]["current"] != true {
		t.Fatalf("after mint: %v", ks)
	}
	ciID, _ := ks[0]["id"].(string)

	// Someone else's key id reads as missing and nothing is deleted.
	if r := a.do(t, http.MethodDelete, "/v1/me/keys/"+ciID, nil, h(other.key)); r.status != http.StatusNotFound {
		t.Fatalf("other account revoked my key: %d", r.status)
	}
	if r := a.do(t, http.MethodDelete, "/v1/me/keys/not-a-uuid", nil, h(p.key)); r.status != http.StatusNotFound {
		t.Fatalf("bad id: %d", r.status)
	}
	if r := a.do(t, http.MethodDelete, "/v1/me/keys/"+ciID, nil, h(p.key)); r.status != http.StatusNoContent {
		t.Fatalf("revoke: %d %s", r.status, r.body)
	}
	if r := me(ci); r.status != http.StatusUnauthorized || r.json(t)["code"] != "invalid_api_key" {
		t.Fatalf("revoked key: %d %s", r.status, r.body)
	}
	if me(p.key).status != http.StatusOK {
		t.Fatal("revoking one key killed another")
	}

	// A connected app (internal credential) cannot mint, revoke or sign out.
	u, err := db.GetUserByAPIKey(context.Background(), a.database, p.key)
	if err != nil {
		t.Fatal(err)
	}
	ik, revoke, err := db.IssueInternalKey(u.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer revoke()
	for _, c := range []struct{ method, path string }{
		{http.MethodPost, "/v1/me/keys"}, {http.MethodPost, "/v1/me/sign-out"}, {http.MethodDelete, "/v1/me/keys/" + ciID},
	} {
		r := a.do(t, c.method, c.path, jsonBody(map[string]string{"name": "sneaky"}), h(ik))
		if r.status != http.StatusBadRequest || r.json(t)["code"] != "not_an_account_key" {
			t.Fatalf("%s %s with a connected app: %d %s", c.method, c.path, r.status, r.body)
		}
	}
	if ks := keysOf(t, a, ik); len(ks) != 1 || ks[0]["current"] != false {
		t.Fatalf("list via connected app: %v", ks)
	}

	// Sign out ends exactly the key it was sent with.
	second := a.do(t, http.MethodPost, "/v1/me/keys", jsonBody(map[string]string{"name": "laptop"}), h(p.key)).json(t)["api_key"].(string)
	if r := a.do(t, http.MethodPost, "/v1/me/sign-out", nil, h(p.key)); r.status != http.StatusNoContent {
		t.Fatalf("sign out: %d %s", r.status, r.body)
	}
	if me(p.key).status != http.StatusUnauthorized || me(second).status != http.StatusOK {
		t.Fatal("sign-out must end only the calling key")
	}

	// Owner-route 401s carry a code.
	if r := a.do(t, http.MethodGet, "/v1/me/keys", nil, nil); r.status != http.StatusUnauthorized || r.json(t)["code"] != "missing_api_key" {
		t.Fatalf("no key: %d %s", r.status, r.body)
	}
}

// Sign-in names the key it issues: a typed code defaults to "agent sign-in",
// a link to "dashboard sign-in", and the caller may name it.
func TestSignInNamesItsKey(t *testing.T) {
	a := newConnectorApp(t)
	ann := a.newPerson(t, "named")
	verify := func(code string, body map[string]string) string {
		t.Helper()
		lt, _ := auth.GenerateAPIKey()
		if err := db.CreateAuthToken(context.Background(), a.database, ann.email, code, lt, time.Now().Add(time.Minute), "dashboard", sql.NullString{}, sql.NullString{}); err != nil {
			t.Fatal(err)
		}
		body["email"], body["code"] = ann.email, code
		r := a.do(t, http.MethodPost, "/v1/auth/verify", jsonBody(body), map[string]string{"Content-Type": "application/json"})
		if r.status != http.StatusOK {
			t.Fatalf("verify: %d %s", r.status, r.body)
		}
		k, _ := r.json(t)["api_key"].(string)
		if !strings.HasPrefix(k, auth.APIKeyPrefix) {
			t.Fatalf("sign-in key without prefix: %q", k[:6])
		}
		return k
	}
	agent := verify("333333", map[string]string{})
	dash := verify("444444", map[string]string{"name": "dashboard sign-in"})
	names := map[string]string{}
	for _, k := range keysOf(t, a, dash) {
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
	a := newConnectorApp(t)
	p := a.newPerson(t, "lost")
	nobody := a.newPerson(t, "nobody")
	var id, adminID string
	if err := a.database.QueryRow(`SELECT id FROM users WHERE username = $1`, p.email).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if err := a.database.QueryRow(`SELECT id FROM users WHERE username = 'admin'`).Scan(&adminID); err != nil {
		t.Fatal(err)
	}
	reissue := func(uid, key string) resp {
		return a.do(t, http.MethodPost, "/v1/admin/users/"+uid+"/key", nil, map[string]string{"X-API-Key": key})
	}
	if r := reissue(id, nobody.key); r.status != http.StatusNotFound {
		t.Fatalf("non-admin reissue: %d", r.status)
	}
	if r := reissue(adminID, a.admin); r.status != http.StatusBadRequest {
		t.Fatalf("admin account reissue: %d", r.status)
	}
	var logged bytes.Buffer
	log.SetOutput(io.MultiWriter(&logged, os.Stderr))
	r := reissue(id, a.admin)
	log.SetOutput(os.Stderr)
	if r.status != http.StatusOK {
		t.Fatalf("reissue: %d %s", r.status, r.body)
	}
	// The reissue is logged with the account and the admin who did it.
	if !regexp.MustCompile(`admin_key_reissue user_id=` + id + ` by=\S+`).Match(logged.Bytes()) {
		t.Fatalf("reissue not logged: %q", logged.String())
	}
	fresh, _ := r.json(t)["api_key"].(string)
	if a.do(t, http.MethodGet, "/v1/me", nil, map[string]string{"X-API-Key": p.key}).status != http.StatusUnauthorized {
		t.Fatal("old key survived a reissue")
	}
	ks := keysOf(t, a, fresh)
	if len(ks) != 1 || ks[0]["name"] != db.KeyNameEvent || ks[0]["current"] != true {
		t.Fatalf("after reissue: %v", ks)
	}
}

// An account mints at most db.MaxAccountKeys keys from the Keys panel; the
// next one is 409 key_limit, and revoking one makes room again.
func TestCreateKeyLimit(t *testing.T) {
	a := newConnectorApp(t)
	p := a.newPerson(t, "many-keys")
	h := map[string]string{"X-API-Key": p.key, "Content-Type": "application/json"}
	for i := len(keysOf(t, a, p.key)); i < db.MaxAccountKeys; i++ {
		if r := a.do(t, http.MethodPost, "/v1/me/keys", jsonBody(map[string]string{"name": "k"}), h); r.status != http.StatusCreated {
			t.Fatalf("key %d: %d %s", i, r.status, r.body)
		}
	}
	r := a.do(t, http.MethodPost, "/v1/me/keys", jsonBody(map[string]string{"name": "one too many"}), h)
	if r.status != http.StatusConflict || r.json(t)["code"] != "key_limit" {
		t.Fatalf("over the limit: %d %s", r.status, r.body)
	}
	ks := keysOf(t, a, p.key)
	if len(ks) != db.MaxAccountKeys {
		t.Fatalf("keys held: %d", len(ks))
	}
	id, _ := ks[0]["id"].(string)
	if r := a.do(t, http.MethodDelete, "/v1/me/keys/"+id, nil, h); r.status != http.StatusNoContent {
		t.Fatalf("revoke: %d", r.status)
	}
	if r := a.do(t, http.MethodPost, "/v1/me/keys", jsonBody(map[string]string{"name": "room again"}), h); r.status != http.StatusCreated {
		t.Fatalf("after revoke: %d %s", r.status, r.body)
	}
}

// A key revoked while a mint made with it is in flight cannot mint a
// successor: the mint waits on the revoke and then finds its key gone.
func TestCreateKeyRacesRevoke(t *testing.T) {
	a := newConnectorApp(t)
	p := a.newPerson(t, "mint-race")
	ctx := context.Background()
	u, err := db.GetUserByAPIKey(ctx, a.database, p.key)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := a.database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := db.DeleteAPIKeyByHash(ctx, tx, u.ID, u.KeyHash); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		plain, _ := auth.GenerateAPIKey()
		_, err := db.CreateAPIKey(ctx, a.database, u.ID, u.KeyHash, plain, "racer")
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
	if err := a.database.QueryRow(`SELECT count(*) FROM api_keys WHERE user_id = $1`, u.ID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("keys left: %d %v", n, err)
	}
	// Through the route, a revoked key is a 401 invalid_api_key.
	r := a.do(t, http.MethodPost, "/v1/me/keys", jsonBody(map[string]string{"name": "late"}), map[string]string{"X-API-Key": p.key, "Content-Type": "application/json"})
	if r.status != http.StatusUnauthorized || r.json(t)["code"] != "invalid_api_key" {
		t.Fatalf("mint with revoked key: %d %s", r.status, r.body)
	}
}
