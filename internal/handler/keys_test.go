package handler

import (
	"context"
	"database/sql"
	"net/http"
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
	r := reissue(id, a.admin)
	if r.status != http.StatusOK {
		t.Fatalf("reissue: %d %s", r.status, r.body)
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
