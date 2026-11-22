package keys

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	. "github.com/vsriram/simple-host/host/access/featuretest"

	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
)

// Keys that stopped working leave room under MAX_KEYS_PER_ACCOUNT, and are
// deleted 30 days after they stopped. Needs DB_DSN.
func TestExpiredKeysFreeTheCapAndArePurged(t *testing.T) {
	a := NewApp(t)
	a.SetLimits(WithLimits(t, map[string]string{"MAX_KEYS_PER_ACCOUNT": "3"}))
	p := a.NewPerson(t, "keycap")
	h := map[string]string{"X-API-Key": p.Key, "Content-Type": "application/json"}
	var ids []string
	for i := len(a.KeysOf(t, p.Key)); i < 3; i++ {
		r := a.Do(t, http.MethodPost, "/v1/me/keys", rt.JSONBody(map[string]string{"name": fmt.Sprint("k", i)}), h)
		if r.Status != http.StatusCreated {
			t.Fatalf("key %d: %d %s", i, r.Status, r.Body)
		}
		ids = append(ids, r.JSON(t)["id"].(string))
	}
	if r := a.Do(t, http.MethodPost, "/v1/me/keys", rt.JSONBody(map[string]string{"name": "full"}), h); r.Status != http.StatusConflict {
		t.Fatalf("at the cap: %d %s", r.Status, r.Body)
	}
	if len(ids) < 2 {
		t.Fatalf("need two minted keys, have %d", len(ids))
	}
	// One past its fixed expiry long ago, one idle long ago.
	if _, err := a.Database.Exec(`UPDATE api_keys SET expires_at = now() - interval '31 days' WHERE id = $1`, ids[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Database.Exec(`UPDATE api_keys SET idle_from = now() - interval '300 days', last_used_at = NULL WHERE id = $1`, ids[1]); err != nil {
		t.Fatal(err)
	}
	r := a.Do(t, http.MethodPost, "/v1/me/keys", rt.JSONBody(map[string]string{"name": "room"}), h)
	if r.Status != http.StatusCreated {
		t.Fatalf("expired keys still count toward the cap: %d %s", r.Status, r.Body)
	}
	recent := r.JSON(t)["id"].(string)
	if _, err := a.Database.Exec(`UPDATE api_keys SET expires_at = now() - interval '1 day' WHERE id = $1`, recent); err != nil {
		t.Fatal(err)
	}
	if _, err := accountsrepo.PurgeExpiredAPIKeys(context.Background(), a.Database, 180*24*time.Hour); err != nil {
		t.Fatal(err)
	}
	left := map[string]bool{}
	for _, k := range a.KeysOf(t, p.Key) {
		left[k["id"].(string)] = true
	}
	if left[ids[0]] || left[ids[1]] {
		t.Errorf("keys expired over 30 days ago were kept: %v", left)
	}
	if !left[recent] {
		t.Error("a key expired a day ago was purged; it should stay listed for 30 days")
	}
}
