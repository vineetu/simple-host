package keys

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	. "github.com/vsriram/simple-host/host/access/featuretest"

	accountapp "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/access/auth"
	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
)

// Keys one at a time: list (marking the caller's own), mint a named key shown
// once, revoke one, and sign out (the calling key dies). A connected app or
// the admin env key cannot mint, revoke or sign out. Needs DB_DSN.
func TestKeysListCreateRevokeSignOut(t *testing.T) {
	a := NewApp(t)
	p := a.NewPerson(t, "keys")
	other := a.NewPerson(t, "keys-other")
	h := func(k string) map[string]string {
		return map[string]string{"X-API-Key": k, "Content-Type": "application/json"}
	}
	me := func(k string) Resp { return a.Do(t, http.MethodGet, "/v1/me/keys", nil, h(k)) }

	// A key from before names existed: no name, no last4, marked current, and
	// the lookup that authenticated the list has already stamped last_used_at.
	ks := a.KeysOf(t, p.Key)
	if len(ks) != 1 || ks[0]["current"] != true || ks[0]["name"] != nil || ks[0]["last4"] != nil || ks[0]["last_used_at"] == nil {
		t.Fatalf("earlier key listed as %v", ks)
	}

	if r := a.Do(t, http.MethodPost, "/v1/me/keys", rt.JSONBody(map[string]string{"name": "  "}), h(p.Key)); r.Status != http.StatusBadRequest {
		t.Fatalf("unnamed key: %d", r.Status)
	}
	if r := a.Do(t, http.MethodPost, "/v1/me/keys", rt.JSONBody(map[string]string{"name": strings.Repeat("x", 61)}), h(p.Key)); r.Status != http.StatusBadRequest {
		t.Fatalf("over-long name: %d", r.Status)
	}
	// Zero-width and bidi-override characters (Unicode Cf) are refused too.
	for _, bad := range []string{"CI\u200bkey", "laptop\u202etxt.exe"} {
		if r := a.Do(t, http.MethodPost, "/v1/me/keys", rt.JSONBody(map[string]string{"name": bad}), h(p.Key)); r.Status != http.StatusBadRequest {
			t.Fatalf("name %q: %d %s", bad, r.Status, r.Body)
		}
	}
	r := a.Do(t, http.MethodPost, "/v1/me/keys", rt.JSONBody(map[string]string{"name": "GitHub Actions"}), h(p.Key))
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
	ks = a.KeysOf(t, p.Key)
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
		r := a.Do(t, c.method, c.path, rt.JSONBody(map[string]string{"name": "sneaky"}), h(ik))
		if r.Status != http.StatusBadRequest || r.JSON(t)["code"] != "not_an_account_key" {
			t.Fatalf("%s %s with a connected app: %d %s", c.method, c.path, r.Status, r.Body)
		}
	}
	if ks := a.KeysOf(t, ik); len(ks) != 1 || ks[0]["current"] != false {
		t.Fatalf("list via connected app: %v", ks)
	}

	// Sign out ends exactly the key it was sent with.
	second := a.Do(t, http.MethodPost, "/v1/me/keys", rt.JSONBody(map[string]string{"name": "laptop"}), h(p.Key)).JSON(t)["api_key"].(string)
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

// An account mints at most 50 keys from the Keys panel; the
// next one is 409 key_limit, and revoking one makes room again.
func TestCreateKeyLimit(t *testing.T) {
	a := NewApp(t)
	p := a.NewPerson(t, "many-keys")
	h := map[string]string{"X-API-Key": p.Key, "Content-Type": "application/json"}
	for i := len(a.KeysOf(t, p.Key)); i < 50; i++ {
		if r := a.Do(t, http.MethodPost, "/v1/me/keys", rt.JSONBody(map[string]string{"name": "k"}), h); r.Status != http.StatusCreated {
			t.Fatalf("key %d: %d %s", i, r.Status, r.Body)
		}
	}
	r := a.Do(t, http.MethodPost, "/v1/me/keys", rt.JSONBody(map[string]string{"name": "one too many"}), h)
	if r.Status != http.StatusConflict || r.JSON(t)["code"] != "key_limit" {
		t.Fatalf("over the limit: %d %s", r.Status, r.Body)
	}
	ks := a.KeysOf(t, p.Key)
	if len(ks) != 50 {
		t.Fatalf("keys held: %d", len(ks))
	}
	id, _ := ks[0]["id"].(string)
	if r := a.Do(t, http.MethodDelete, "/v1/me/keys/"+id, nil, h); r.Status != http.StatusNoContent {
		t.Fatalf("revoke: %d", r.Status)
	}
	if r := a.Do(t, http.MethodPost, "/v1/me/keys", rt.JSONBody(map[string]string{"name": "room again"}), h); r.Status != http.StatusCreated {
		t.Fatalf("after revoke: %d %s", r.Status, r.Body)
	}
}
