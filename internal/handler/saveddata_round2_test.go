package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/vsriram/simple-host/internal/config"
)

// Saved data, step 1, review round 2 (2026-09-27). Needs DB_DSN.

// historyBytes returns sites.history_bytes and what the history really holds.
func historyBytes(t *testing.T, a *privateApp, siteID string) (kept, want int64) {
	t.Helper()
	if err := a.database.QueryRow(`
		SELECT history_bytes,
		       COALESCE((SELECT sum(COALESCE(octet_length(prev::text), octet_length(diff::text), 0))
		                   FROM data_history WHERE site_id = $1), 0)
		  FROM sites WHERE id = $1`, siteID).Scan(&kept, &want); err != nil {
		t.Fatal(err)
	}
	return
}

// N1: history is bounded at write time, not only by the sweep: a write that
// takes it past SAVED_DATA_HISTORY_MAX_MB thins it at once, and the running
// size stays exact.
func TestHistoryBoundedAtWrite(t *testing.T) {
	s := newSavedDataSite(t)
	a := s.a
	c := config.DefaultSavedData()
	c.HistoryMaxMB = 1
	a.sites.SetSavedData(c)
	a.sites.thinLimiter = newRateLimiter(1000, 1000) // no spacing in the test
	capBytes := int64(1 << 20)
	state := "/v1/sites/shop/state"
	docs := []string{noise(t, 300<<10), noise(t, 300<<10)}
	for i := 0; i < 12; i++ {
		// Alternating two documents never grows the site's live data.
		r := s.ownerWrite(t, "PUT", state, map[string]string{"blob": docs[i%2]}, "X-Forwarded-For", fmt.Sprintf("198.51.100.%d", i))
		if r.status != 200 {
			t.Fatalf("put %d: %d %s", i, r.status, r.body)
		}
		kept, want := historyBytes(t, a, s.shopID)
		if kept != want {
			t.Fatalf("after put %d: history_bytes %d, recomputed %d", i, kept, want)
		}
		// The cap, plus the day's first copy that thinning always keeps and
		// the write that crossed it.
		if kept > capBytes+2*(420<<10) {
			t.Fatalf("after put %d: history holds %d bytes, cap %d", i, kept, capBytes)
		}
	}
	// Deletes keep the running size exact too.
	s.owner(t, "DELETE", state[:len("/v1/sites/shop")]+"/history", map[string]string{"confirm": "shop"})
	if kept, want := historyBytes(t, a, s.shopID); kept != 0 || want != 0 {
		t.Fatalf("after clearing: history_bytes %d, recomputed %d", kept, want)
	}
}

// N2: the read limit is per resolved site and address; another Host header
// for the same site does not open a fresh bucket.
func TestReadLimitIgnoresHostHeader(t *testing.T) {
	s := newSavedDataSite(t)
	a := s.a
	c := config.DefaultSavedData()
	c.ReadBurst, c.ReadPerSec = 2, 1
	a.sites.SetSavedData(c)
	ip := map[string]string{"X-Forwarded-For": "203.0.113.77"}
	for i := 0; i < 2; i++ {
		if r := a.at(t, "GET", pcSiteDomain, "/v1/u/"+s.oh+"/sites/shop/state", nil, ip); r.status != 200 {
			t.Fatalf("read %d: %d %s", i, r.status, r.body)
		}
	}
	// The same site through its own address: same bucket.
	if r := a.at(t, "GET", s.dom, "/v1/sites/shop/state", nil, ip); r.status != 429 || r.json(t)["code"] != "rate_limited" {
		t.Fatalf("read through another Host: %d %s", r.status, r.body)
	}
	// Another address has its own bucket.
	if r := a.at(t, "GET", s.dom, "/v1/sites/shop/state", nil, map[string]string{"X-Forwarded-For": "203.0.113.78"}); r.status != 200 {
		t.Fatalf("another address: %d %s", r.status, r.body)
	}
}

// N4: the undo-window purge never removes a change that a kept, earlier
// diff is rebuilt through, even when created_at is out of id order.
func TestPurgeKeepsDiffChain(t *testing.T) {
	s := newSavedDataSite(t)
	a := s.a
	c := config.DefaultSavedData()
	c.SnapshotEvery = 1000
	a.sites.SetSavedData(c)
	state := "/v1/sites/shop/state"
	pad := noise(t, 2000) // a large document, so a small PATCH is a diff
	if r := s.ownerWrite(t, "PUT", state, map[string]any{"n": 0, "pad": pad}); r.status != 200 {
		t.Fatalf("put: %d %s", r.status, r.body)
	}
	for i := 1; i <= 3; i++ {
		if r := s.visitor(t, "PATCH", state, fmt.Sprintf(`{"ops":[{"op":"set","path":"n","value":%d}]}`, i)); r.status != 200 {
			t.Fatalf("patch %d: %d %s", i, r.status, r.body)
		}
	}
	var ids []int64
	rows, _ := a.database.Query(`SELECT id FROM data_history WHERE site_id = $1 AND kind = 'state' ORDER BY id`, s.shopID)
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	if len(ids) != 4 {
		t.Fatalf("%d history rows, want 4", len(ids))
	}
	var diffs int
	a.database.QueryRow(`SELECT count(*) FROM data_history WHERE id = ANY($1) AND diff IS NOT NULL`, fmt.Sprintf("{%d,%d,%d}", ids[1], ids[2], ids[3])).Scan(&diffs)
	if diffs != 3 {
		t.Fatalf("%d of the patches kept a diff, want 3", diffs)
	}
	// ids[1] (the change n: 0 -> 1, kept as a diff; the version before it has n = 0) is kept;
	// ids[2], a newer change it is rebuilt through, carries an older time.
	if _, err := a.database.Exec(`UPDATE data_history SET created_at = now() - interval '31 days' WHERE id = $1`, ids[2]); err != nil {
		t.Fatal(err)
	}
	a.sites.sweepSavedData(context.Background())
	var left int
	a.database.QueryRow(`SELECT count(*) FROM data_history WHERE id = $1`, ids[2]).Scan(&left)
	if left != 1 {
		t.Fatal("the purge cut a change a kept diff depends on")
	}
	r := s.owner(t, "GET", state+"/history/"+itoa(ids[1]), nil)
	var e struct {
		Value map[string]any `json:"value"`
	}
	if r.status != 200 || json.Unmarshal(r.body, &e) != nil {
		t.Fatalf("history entry: %d %s", r.status, r.body)
	}
	if want := map[string]any{"n": float64(0), "pad": pad}; !reflect.DeepEqual(e.Value, want) {
		t.Fatalf("rebuilt version: n=%v", e.Value["n"])
	}
	// Once nothing earlier is kept, an expired change goes as usual.
	if _, err := a.database.Exec(`UPDATE data_history SET created_at = now() - interval '31 days' WHERE id = ANY($1)`, fmt.Sprintf("{%d,%d}", ids[0], ids[1])); err != nil {
		t.Fatal(err)
	}
	a.sites.sweepSavedData(context.Background())
	a.database.QueryRow(`SELECT count(*) FROM data_history WHERE id = ANY($1)`, fmt.Sprintf("{%d,%d,%d}", ids[0], ids[1], ids[2])).Scan(&left)
	if left != 0 {
		t.Fatalf("%d expired changes left once nothing depends on them", left)
	}
}

// L10: the admin deletes another account's data for good only by naming the
// owner (/v1/u/{handle}/…); a bare name is refused, since names are not
// unique across accounts.
func TestAdminPurgeNeedsHandle(t *testing.T) {
	s := newSavedDataSite(t)
	a := s.a
	admin := map[string]string{"X-API-Key": a.admin}
	gb := "/v1/sites/shop/collections/guestbook"
	id := itoa(int64(s.visitor(t, "POST", gb, map[string]int{"n": 1}).json(t)["id"].(float64)))
	s.owner(t, "DELETE", gb+"/items/"+id, nil)
	s.visitor(t, "PUT", "/v1/sites/shop/state", map[string]int{"v": 1})
	for path, body := range map[string]any{
		"/v1/sites/shop/history": map[string]string{"confirm": "shop"},
		gb + "/deleted":          map[string]string{"confirm": "guestbook"},
		gb + "/deleted/" + id:    nil,
	} {
		if r := a.at(t, "DELETE", pcSiteDomain, path, body, admin); r.status != 409 || r.json(t)["code"] != "handle_required" {
			t.Errorf("admin bare-name %s: %d %s", path, r.status, r.body)
		}
	}
	if r := a.at(t, "DELETE", pcSiteDomain, "/v1/u/"+s.oh+"/sites/shop/collections/guestbook/deleted/"+id, nil, admin); r.status != 200 {
		t.Fatalf("admin with the handle: %d %s", r.status, r.body)
	}
	if r := a.at(t, "DELETE", pcSiteDomain, "/v1/u/"+s.oh+"/sites/shop/history", map[string]string{"confirm": "shop"}, admin); r.status != 200 {
		t.Fatalf("admin clears history with the handle: %d %s", r.status, r.body)
	}
	// The owner's own bare name still works.
	s.visitor(t, "PUT", "/v1/sites/shop/state", map[string]int{"v": 2})
	if r := s.owner(t, "DELETE", "/v1/sites/shop/history", map[string]string{"confirm": "shop"}); r.status != 200 {
		t.Fatalf("owner bare name: %d %s", r.status, r.body)
	}
}
