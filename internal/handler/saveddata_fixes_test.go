package handler

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/internal/config"
	db "github.com/vsriram/simple-host/internal/db"
)

// Saved data, step 1, review fixes (2026-09-27). Needs DB_DSN.

// liveBytes recomputes what sites.data_bytes should hold.
func liveBytes(t *testing.T, a *privateApp, siteID string) (kept, want int64) {
	t.Helper()
	if err := a.database.QueryRow(`
		SELECT data_bytes,
		       COALESCE(octet_length(state::text), 0)
		     + COALESCE((SELECT sum(octet_length(data::text)) FROM collection_items WHERE site_id = $1 AND deleted_at IS NULL), 0)
		  FROM sites WHERE id = $1`, siteID).Scan(&kept, &want); err != nil {
		t.Fatal(err)
	}
	return
}

// ownerWrite is a write with the owner's key from a page on the site's
// address (writes need the page's Origin, like a browser's).
func (s *savedDataSite) ownerWrite(t *testing.T, method, path string, body any, extra ...string) resp {
	t.Helper()
	hdr := map[string]string{"X-API-Key": s.olive.key, "Origin": "https://" + s.dom}
	for i := 0; i+1 < len(extra); i += 2 {
		hdr[extra[i]] = extra[i+1]
	}
	return s.a.at(t, method, pcSiteDomain, "/v1/u/"+s.oh+strings.TrimPrefix(path, "/v1"), body, hdr)
}

func noise(t *testing.T, n int) string {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(b)
}

// H1 + M1: the cap counts live data only, refuses only growth, and the
// running size stays exact through every kind of write.
func TestSiteCapCountsLiveDataOnly(t *testing.T) {
	s := newSavedDataSite(t)
	a := s.a
	check := func(when string) {
		t.Helper()
		if kept, want := liveBytes(t, a, s.shopID); kept != want {
			t.Fatalf("%s: data_bytes %d, recomputed %d", when, kept, want)
		}
	}
	gb := "/v1/sites/shop/collections/guestbook"
	state := "/v1/sites/shop/state"
	s.visitor(t, "PUT", state, map[string]any{"a": 1, "list": []int{1, 2}})
	check("put")
	s.visitor(t, "PATCH", state, `{"ops":[{"op":"append","path":"list","value":3},{"op":"set","path":"b.c","value":"x"}]}`)
	check("patch")
	var ids []string
	for i := 0; i < 3; i++ {
		r := s.visitor(t, "POST", gb, map[string]any{"msg": strings.Repeat("m", 100*(i+1))})
		if r.status != 201 {
			t.Fatalf("post: %d %s", r.status, r.body)
		}
		ids = append(ids, itoa(int64(r.json(t)["id"].(float64))))
	}
	check("append")
	s.owner(t, "DELETE", gb+"/items/"+ids[0], nil)
	check("delete")
	s.owner(t, "POST", gb+"/items/"+ids[0]+"/restore", nil)
	check("undelete")
	s.owner(t, "DELETE", gb, map[string]string{"confirm": "guestbook"})
	check("clear")
	s.owner(t, "DELETE", gb+"/deleted/"+ids[1], nil)
	check("delete for good")

	// Fill the site to its cap with list items (1 MB here).
	c := config.DefaultSavedData()
	c.SiteMaxMB = 1
	a.sites.SetSavedData(c)
	s.owner(t, "POST", gb+"/deleted/restore", map[string]bool{"all": true})
	if _, err := a.database.Exec(`INSERT INTO collection_items (site_id, collection, data) VALUES ($1, 'flood', $2::jsonb)`,
		s.shopID, `{"x":"`+noise(t, 800<<10)+`"}`); err != nil {
		t.Fatal(err)
	}
	check("flood")
	full := s.visitor(t, "POST", gb, map[string]string{"msg": "one more"})
	if full.status != http.StatusInsufficientStorage || full.json(t)["code"] != "site_full" || !strings.Contains(full.json(t)["error"].(string), "clear lists") {
		t.Fatalf("full site takes a new item: %d %s", full.status, full.body)
	}
	// Growing the document is refused; the owner's smaller document is not.
	if r := s.visitor(t, "PATCH", state, `{"ops":[{"op":"set","path":"big","value":"`+strings.Repeat("y", 4000)+`"}]}`); r.status != http.StatusInsufficientStorage {
		t.Fatalf("growing patch on a full site: %d %s", r.status, r.body)
	}
	if r := s.ownerWrite(t, "PUT", state, map[string]int{"a": 1}); r.status != 200 {
		t.Fatalf("shrinking put on a full site: %d %s", r.status, r.body)
	}
	if r := s.visitor(t, "PATCH", state, `{"ops":[{"op":"inc","path":"a"}]}`); r.status != 200 {
		t.Fatalf("same-size patch on a full site: %d %s", r.status, r.body)
	}
	check("full")
	// Clearing the flooded list frees the room at once: Recently deleted is
	// not counted.
	if r := s.owner(t, "DELETE", "/v1/sites/shop/collections/flood", map[string]string{"confirm": "flood"}); r.status != 200 {
		t.Fatalf("clear flood: %d %s", r.status, r.body)
	}
	if r := s.visitor(t, "POST", gb, map[string]string{"msg": "room again"}); r.status != 201 {
		t.Fatalf("after clearing: %d %s", r.status, r.body)
	}
	// Bringing the flood back would pass the cap again.
	if r := s.owner(t, "POST", "/v1/sites/shop/collections/flood/deleted/restore", map[string]bool{"all": true}); r.status != http.StatusInsufficientStorage {
		t.Fatalf("restore past the cap: %d %s", r.status, r.body)
	}
	check("end")
}

// H1 + M5: delete for good, empty Recently deleted, clear history.
func TestDeleteForever(t *testing.T) {
	s := newSavedDataSite(t)
	a := s.a
	gb := "/v1/sites/shop/collections/guestbook"
	var ids []string
	for i := 0; i < 3; i++ {
		ids = append(ids, itoa(int64(s.visitor(t, "POST", gb, map[string]int{"n": i}).json(t)["id"].(float64))))
	}
	// A live item cannot be deleted for good; it goes to Recently deleted first.
	if r := s.owner(t, "DELETE", gb+"/deleted/"+ids[0], nil); r.status != 404 {
		t.Fatalf("live item deleted for good: %d %s", r.status, r.body)
	}
	s.owner(t, "DELETE", gb+"/items/"+ids[0], nil)
	s.owner(t, "DELETE", gb+"/items/"+ids[1], nil)
	for name, hdr := range map[string]map[string]string{"visitor's key": {"X-API-Key": s.vic.key}, "no key": nil} {
		if r := a.at(t, "DELETE", pcSiteDomain, gb+"/deleted/"+ids[0], nil, hdr); r.status == 200 {
			t.Errorf("%s deletes for good: %d", name, r.status)
		}
	}
	if r := s.owner(t, "DELETE", gb+"/deleted/"+ids[0], nil); r.status != 200 || r.json(t)["deleted_for_good"] != float64(1) {
		t.Fatalf("delete for good: %d %s", r.status, r.body)
	}
	var left int
	a.database.QueryRow(`SELECT count(*) FROM collection_items WHERE id = $1`, ids[0]).Scan(&left)
	a.database.QueryRow(`SELECT count(*) + $2 FROM data_history WHERE item_id = $1`, ids[0], left).Scan(&left)
	if left != 0 {
		t.Fatalf("item or its history kept: %d", left)
	}
	// Empty Recently deleted: confirmed with the list's name.
	if r := s.owner(t, "DELETE", gb+"/deleted", map[string]string{"confirm": "nope"}); r.status != 400 || r.json(t)["code"] != "confirm_required" {
		t.Fatalf("empty without confirming: %d %s", r.status, r.body)
	}
	if r := s.owner(t, "DELETE", "/v1/u/"+s.oh+"/sites/shop/collections/guestbook/deleted", map[string]string{"confirm": "guestbook"}); r.status != 200 || r.json(t)["deleted_for_good"] != float64(1) {
		t.Fatalf("empty recently deleted: %d %s", r.status, r.body)
	}
	if n := len(itemsOf(t, s.owner(t, "GET", gb, nil))); n != 1 {
		t.Fatalf("live items touched: %d left", n)
	}
	// Clear history: confirmed with the site's name; the data stays.
	s.visitor(t, "PUT", "/v1/sites/shop/state", map[string]int{"v": 1})
	s.visitor(t, "PUT", "/v1/sites/shop/state", map[string]int{"v": 2})
	if r := s.owner(t, "DELETE", "/v1/sites/shop/history", map[string]string{"confirm": "guestbook"}); r.status != 400 {
		t.Fatalf("clear history without confirming: %d %s", r.status, r.body)
	}
	if r := s.owner(t, "DELETE", "/v1/sites/shop/history", map[string]string{"confirm": "shop"}); r.status != 200 || r.json(t)["cleared"].(float64) < 2 {
		t.Fatalf("clear history: %d %s", r.status, r.body)
	}
	var hist int
	a.database.QueryRow(`SELECT count(*) FROM data_history WHERE site_id = $1`, s.shopID).Scan(&hist)
	if hist != 0 {
		t.Fatalf("history left: %d", hist)
	}
	if body := string(a.at(t, "GET", pcSiteDomain, "/v1/u/"+s.oh+"/sites/shop/state", nil, nil).body); !strings.Contains(body, `"v": 2`) {
		t.Fatalf("data went with the history: %s", body)
	}
	// Another owner's handle route finds nothing.
	other := a.newPerson(t, "other")
	if r := a.at(t, "DELETE", pcSiteDomain, "/v1/u/"+s.oh+"/sites/shop/history", map[string]string{"confirm": "shop"}, map[string]string{"X-API-Key": other.key}); r.status != 404 {
		t.Fatalf("another owner clears history: %d", r.status)
	}
}

// M2: a PATCH keeps a diff, and every earlier version comes back exactly,
// across many patches, a replace in the middle, snapshots and thinning.
func TestPatchHistoryRebuildsEveryVersion(t *testing.T) {
	s := newSavedDataSite(t)
	a := s.a
	c := config.DefaultSavedData()
	c.SnapshotEvery = 7
	a.sites.SetSavedData(c)
	state := "/v1/sites/shop/state"
	var docs []any // docs[i]: the document after write i
	decode := func(b []byte) any {
		var v any
		if err := json.Unmarshal(b, &v); err != nil {
			t.Fatalf("not JSON: %s", b)
		}
		return v
	}
	// A new address per write: 70 writes are past the per-address rate.
	ip := func() string { return fmt.Sprintf("203.0.113.%d", len(docs)) }
	put := func(v any) {
		r := s.ownerWrite(t, "PUT", state, v, "X-Forwarded-For", ip())
		if r.status != 200 {
			t.Fatalf("put: %d %s", r.status, r.body)
		}
		docs = append(docs, decode(r.body))
	}
	patch := func(ops string) {
		r := s.visitor(t, "PATCH", state, `{"ops":`+ops+`}`, "X-Forwarded-For", ip())
		if r.status != 200 {
			t.Fatalf("patch %s: %d %s", ops, r.status, r.body)
		}
		docs = append(docs, decode(r.body))
	}
	put(map[string]any{"likes": 0, "comments": []any{}, "cfg": map[string]any{"title": "hi", "tags": []string{"a"}}, "pad": strings.Repeat("p", 3000)})
	for i := 0; i < 70; i++ {
		switch i % 7 {
		case 0:
			patch(`[{"op":"inc","path":"likes"}]`)
		case 1:
			patch(fmt.Sprintf(`[{"op":"append","path":"comments","value":{"id":%d,"t":"c%d"}}]`, i, i))
		case 2:
			patch(fmt.Sprintf(`[{"op":"set","path":"cfg.title","value":"t%d"},{"op":"set","path":"cfg.new%d","value":[%d]}]`, i, i, i))
		case 3:
			patch(fmt.Sprintf(`[{"op":"removeWhere","path":"comments","match":{"id":%d}}]`, i-2))
		case 4:
			patch(fmt.Sprintf(`[{"op":"remove","path":"cfg.new%d"},{"op":"set","path":"n%d","value":null}]`, i-2, i))
		case 5:
			patch(fmt.Sprintf(`[{"op":"set","path":"cfg","value":{"title":"reset%d","tags":["b","c"]}},{"op":"inc","path":"likes","by":0.5}]`, i))
		case 6:
			patch(fmt.Sprintf(`[{"op":"append","path":"cfg.tags","value":"x%d"},{"op":"remove","path":"n%d"}]`, i, i-2))
		}
		if i == 35 {
			put(map[string]any{"likes": 100, "comments": []any{map[string]any{"id": -1}}, "cfg": map[string]any{"title": "replaced", "tags": []any{}}, "pad": strings.Repeat("q", 3000)})
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
	// The first write (from nothing) is a change too.
	if len(ids) != len(docs) {
		t.Fatalf("%d history rows for %d writes", len(ids), len(docs))
	}
	var diffs int
	a.database.QueryRow(`SELECT count(*) FROM data_history WHERE site_id = $1 AND diff IS NOT NULL`, s.shopID).Scan(&diffs)
	if diffs < len(ids)/2 {
		t.Fatalf("only %d of %d changes kept as a diff", diffs, len(ids))
	}
	before := func(i int) any {
		if i == 0 {
			return nil
		}
		return docs[i-1]
	}
	verify := func(when string) {
		t.Helper()
		for i, id := range ids {
			var gone bool
			a.database.QueryRow(`SELECT NOT EXISTS(SELECT 1 FROM data_history WHERE id = $1)`, id).Scan(&gone)
			if gone {
				continue
			}
			r := s.owner(t, "GET", state+"/history/"+itoa(id), nil)
			if r.status != 200 {
				t.Fatalf("%s: read %d: %d %s", when, id, r.status, r.body)
			}
			var e struct {
				Value any `json:"value"`
			}
			json.Unmarshal(r.body, &e)
			if !reflect.DeepEqual(e.Value, before(i)) {
				t.Fatalf("%s: version before write %d:\n got %v\nwant %v", when, i, e.Value, before(i))
			}
		}
	}
	verify("all kept")

	// Restore a version held only as a diff, deep in the chain.
	var mid int
	for i := len(ids) / 3; i < len(ids); i++ {
		var isDiff bool
		a.database.QueryRow(`SELECT diff IS NOT NULL FROM data_history WHERE id = $1`, ids[i]).Scan(&isDiff)
		if isDiff {
			mid = i
			break
		}
	}
	if r := s.owner(t, "POST", state+"/history/"+itoa(ids[mid])+"/restore", nil); r.status != 200 {
		t.Fatalf("restore: %d %s", r.status, r.body)
	}
	if got := decode(a.at(t, "GET", pcSiteDomain, "/v1/u/"+s.oh+"/sites/shop/state", nil, nil).body); !reflect.DeepEqual(got, before(mid)) {
		t.Fatalf("restored:\n got %v\nwant %v", got, before(mid))
	}
	docs = append(docs, before(mid))
	ids = append(ids, 0) // the restore row itself is checked by the rows above it

	// Thinning to a third keeps every remaining version exact.
	var total int64
	a.database.QueryRow(`SELECT sum(COALESCE(pg_column_size(prev), pg_column_size(diff), 0)) FROM data_history WHERE site_id = $1`, s.shopID).Scan(&total)
	if _, err := db.ThinSiteHistory(context.Background(), a.database, s.shopID, total/3); err != nil {
		t.Fatal(err)
	}
	var kept int
	a.database.QueryRow(`SELECT count(*) FROM data_history WHERE site_id = $1`, s.shopID).Scan(&kept)
	if kept == 0 || kept >= len(ids) {
		t.Fatalf("thinning kept %d of %d", kept, len(ids))
	}
	verify("after thinning")
}

// H2 + L1: an answer is replayed from what was kept (never a body); the same
// key with another body is refused; keys are per identity; no identity, no
// idempotency; rows are capped per site.
func TestIdempotencyKeepsNoBody(t *testing.T) {
	s := newSavedDataSite(t)
	a := s.a
	gb := "/v1/sites/shop/collections/guestbook"
	one := s.ownerWrite(t, "POST", gb, map[string]string{"m": "keyed"}, "Idempotency-Key", "vote-a")
	two := s.ownerWrite(t, "POST", gb, map[string]string{"m": "keyed"}, "Idempotency-Key", "vote-a")
	if one.status != 201 || two.status != 201 || string(one.body) != string(two.body) || two.header.Get("Idempotent-Replayed") != "true" {
		t.Fatalf("replay: %d %s / %d %s", one.status, one.body, two.status, two.body)
	}
	var ref int64
	var status int
	if err := a.database.QueryRow(`SELECT status, ref FROM idempotency_keys WHERE site_id = $1`, s.shopID).Scan(&status, &ref); err != nil || status != 201 || ref != int64(one.json(t)["id"].(float64)) {
		t.Fatalf("kept: %d %d %v", status, ref, err)
	}
	// Another body under the same key.
	if r := s.ownerWrite(t, "POST", gb, map[string]string{"m": "other"}, "Idempotency-Key", "vote-a"); r.status != 409 || r.json(t)["code"] != "idempotency_key_reused" {
		t.Fatalf("reused key: %d %s", r.status, r.body)
	}
	// Another person using the same key (a page's fixed 'vote-a') is saved.
	if r := s.visitor(t, "POST", gb, map[string]string{"m": "keyed"}, "Idempotency-Key", "vote-a"); r.status != 201 || r.header.Get("Idempotent-Replayed") != "" {
		t.Fatalf("second person's write replayed the first's: %d %v", r.status, r.header)
	}
	// A PATCH replay answers with the document now.
	inc := `{"ops":[{"op":"inc","path":"n"}]}`
	p1 := s.visitor(t, "PATCH", "/v1/sites/shop/state", inc, "Idempotency-Key", "i1")
	if r := s.ownerWrite(t, "PATCH", "/v1/sites/shop/state", `{"ops":[{"op":"set","path":"m","value":1}]}`); r.status != 200 {
		t.Fatalf("owner patch: %d %s", r.status, r.body)
	}
	p2 := s.visitor(t, "PATCH", "/v1/sites/shop/state", inc, "Idempotency-Key", "i1")
	if p1.status != 200 || p2.status != 200 || p2.header.Get("Idempotent-Replayed") != "true" || !strings.Contains(string(p2.body), `"m"`) || p2.header.Get("ETag") == p1.header.Get("ETag") {
		t.Fatalf("patch replay: %s %v / %s %v", p1.body, p1.header, p2.body, p2.header)
	}
	// The sweep keeps at most SAVED_DATA_IDEMPOTENCY_MAX_PER_SITE per site.
	c := config.DefaultSavedData()
	c.IdempotencyMaxPerSite = 1
	a.sites.SetSavedData(c)
	a.sites.sweepSavedData(context.Background())
	var n int
	a.database.QueryRow(`SELECT count(*) FROM idempotency_keys WHERE site_id = $1`, s.shopID).Scan(&n)
	if n != 1 {
		t.Fatalf("rows after the per-site cap: %d", n)
	}
}

// No identity (the open shared host of an instance without person hosts):
// the header changes nothing, so two visitors behind one address never share
// an answer.
func TestIdempotencyNeedsAnIdentity(t *testing.T) {
	a := newPrivateApp(t)
	olive := a.newPerson(t, "olive")
	a.deploy(t, olive, "board")
	path := "/v1/sites/board/collections/votes"
	hdr := map[string]string{"Origin": "https://" + pcContentHost, "Idempotency-Key": "vote-a"}
	for i := 0; i < 2; i++ {
		if r := a.at(t, "POST", pcContentHost, path, map[string]string{"v": "a"}, hdr); r.status != 201 || r.header.Get("Idempotent-Replayed") != "" {
			t.Fatalf("anonymous write %d: %d %s %v", i, r.status, r.body, r.header)
		}
	}
	if n := len(itemsOf(t, a.at(t, "GET", pcSiteDomain, path, nil, map[string]string{"X-API-Key": olive.key}))); n != 2 {
		t.Fatalf("want both votes, got %d", n)
	}
}

// M4: Recently deleted pages by (deleted_at, id), so nothing is skipped when
// items were deleted out of id order.
func TestRecentlyDeletedPagesEveryItem(t *testing.T) {
	s := newSavedDataSite(t)
	gb := "/v1/sites/shop/collections/guestbook"
	var ids []string
	for i := 0; i < 6; i++ {
		ids = append(ids, itoa(int64(s.visitor(t, "POST", gb, map[string]int{"n": i}).json(t)["id"].(float64))))
	}
	for _, i := range []int{4, 0, 5, 1, 3, 2} {
		if r := s.owner(t, "DELETE", gb+"/items/"+ids[i], nil); r.status != 204 {
			t.Fatal(r.status)
		}
	}
	seen := map[string]bool{}
	before := ""
	for page := 0; page < 10; page++ {
		r := s.owner(t, "GET", gb+"/deleted?limit=2&before="+before, nil)
		var body struct {
			Items []struct {
				ID int64 `json:"id"`
			} `json:"items"`
			Next *string `json:"next"`
		}
		if err := json.Unmarshal(r.body, &body); err != nil || r.status != 200 {
			t.Fatalf("page: %d %s", r.status, r.body)
		}
		for _, it := range body.Items {
			seen[itoa(it.ID)] = true
		}
		if body.Next == nil {
			break
		}
		before = *body.Next
	}
	if len(seen) != 6 {
		t.Fatalf("paged %d of 6: %v", len(seen), seen)
	}
}

// L2, L3, L4, L5 and the append limit.
func TestSavedDataAttributionAndLimits(t *testing.T) {
	s := newSavedDataSite(t)
	a := s.a
	gb := "/v1/sites/shop/collections/guestbook"
	// L2: the owner's key on the shared host of a site that lives on its own
	// address is recorded as the owner (log mode, where that write passes).
	a.sites.writeAuthMode = "log"
	if r := a.at(t, "PUT", pcContentHost, "/v1/u/"+s.oh+"/sites/shop/state", map[string]int{"k": 1}, map[string]string{"X-API-Key": s.olive.key, "Origin": "https://" + pcContentHost}); r.status != 200 {
		t.Fatalf("owner key on the shared host: %d %s", r.status, r.body)
	}
	if h := historyOf(t, s.owner(t, "GET", "/v1/sites/shop/state/history", nil)); len(h) == 0 || h[0]["by_kind"] != "owner" {
		t.Fatalf("attributed to: %v", h)
	}
	a.sites.writeAuthMode = "on"
	// L3: the admin's moderation shows as the operator, never an address.
	id := itoa(int64(s.visitor(t, "POST", gb, map[string]string{"m": "spam"}).json(t)["id"].(float64)))
	if r := a.at(t, "DELETE", pcSiteDomain, "/v1/u/"+s.oh+"/sites/shop/collections/guestbook/items/"+id, nil, map[string]string{"X-API-Key": a.admin}); r.status != 204 {
		t.Fatalf("admin delete: %d %s", r.status, r.body)
	}
	h := historyOf(t, s.owner(t, "GET", gb+"/history", nil))
	if len(h) == 0 || h[0]["by"] != db.OperatorLabel || h[0]["by_kind"] != "admin" {
		t.Fatalf("admin shown as: %v", h)
	}
	var stored string
	a.database.QueryRow(`SELECT COALESCE(actor_email, '') FROM data_history WHERE site_id = $1 AND actor_kind = 'admin' LIMIT 1`, s.shopID).Scan(&stored)
	if strings.Contains(stored, "@") {
		t.Fatalf("admin address stored: %q", stored)
	}
	// L4: an owner read carrying who sent each item is never cached.
	if r := s.owner(t, "GET", gb, nil); !strings.Contains(r.header.Get("Cache-Control"), "no-store") {
		t.Fatalf("owner read cache header: %v", r.header)
	}
	// L5: reads with a key are limited per identity, not per address.
	c := config.DefaultSavedData()
	c.ReadBurst, c.ReadPerSec = 2, 1
	c.AppendBurst, c.AppendPerMin = 1, 1
	a.sites.SetSavedData(c)
	for i, ip := range []string{"198.51.100.1", "198.51.100.2"} {
		if r := a.at(t, "GET", pcSiteDomain, "/v1/sites/shop/state", nil, map[string]string{"X-API-Key": s.olive.key, "X-Forwarded-For": ip}); r.status != 200 {
			t.Fatalf("key read %d: %d", i, r.status)
		}
	}
	if r := a.at(t, "GET", pcSiteDomain, "/v1/sites/shop/state", nil, map[string]string{"X-API-Key": s.olive.key, "X-Forwarded-For": "198.51.100.3"}); r.status != 429 {
		t.Fatalf("key read over its limit from a new address: %d", r.status)
	}
	if r := a.at(t, "GET", pcSiteDomain, "/v1/sites/shop/state", nil, map[string]string{"X-Forwarded-For": "198.51.100.3"}); r.status != 200 {
		t.Fatalf("an anonymous reader shares the key's bucket: %d", r.status)
	}
	// Items added without the owner's key: per address.
	if r := s.visitor(t, "POST", gb, map[string]string{"m": "1"}, "X-Forwarded-For", "198.51.100.9"); r.status != 201 {
		t.Fatalf("first append: %d %s", r.status, r.body)
	}
	if r := s.visitor(t, "POST", gb, map[string]string{"m": "2"}, "X-Forwarded-For", "198.51.100.9"); r.status != 429 {
		t.Fatalf("append over the limit: %d %s", r.status, r.body)
	}
	if r := s.ownerWrite(t, "POST", gb, map[string]string{"m": "owner"}, "X-Forwarded-For", "198.51.100.9"); r.status != 201 {
		t.Fatalf("owner append under the visitor limit: %d %s", r.status, r.body)
	}
}

// L9: whole numbers of any size match exactly; a float sum that overflows is
// a 400, not a 500.
func TestStateNumbersExact(t *testing.T) {
	root := map[string]any{"rows": []any{
		map[string]any{"id": json.Number("12345678901234567890")},
		map[string]any{"id": json.Number("12345678901234567891")},
	}}
	if err := applyStateOps(root, []stateOp{{Op: "removeWhere", Path: "rows", Match: map[string]any{"id": json.Number("12345678901234567891")}}}); err != nil {
		t.Fatal(err)
	}
	if rows := root["rows"].([]any); len(rows) != 1 || rows[0].(map[string]any)["id"] != json.Number("12345678901234567890") {
		t.Fatalf("removeWhere took the neighbour: %v", rows)
	}
	big := map[string]any{"x": json.Number("1e308")}
	if err := applyStateOps(big, []stateOp{{Op: "inc", Path: "x", By: json.RawMessage("1e308")}}); err == nil {
		t.Fatalf("overflow accepted: %v", big)
	}
}

// M3: Download my data lists the changes a person made to other people's
// saved data while signed in (where, what, when; never the value).
func TestExportListsChangesElsewhere(t *testing.T) {
	s := newSavedDataSite(t)
	a := s.a
	s.visitor(t, "PUT", "/v1/sites/shop/state", map[string]string{"secret": "owner data"})
	r := a.at(t, "GET", pcSiteDomain, "/v1/me/export.tar.gz", nil, map[string]string{"X-API-Key": s.vic.key})
	if r.status != 200 {
		t.Fatalf("export: %d %s", r.status, r.body)
	}
	body := fileUnder(t, tarFiles(t, r.body), "/visitor.json")
	var v struct {
		Changed []map[string]any `json:"changed"`
	}
	if err := json.Unmarshal([]byte(body), &v); err != nil || len(v.Changed) != 1 || v.Changed[0]["what"] != "page data" || v.Changed[0]["change"] != "replace" {
		t.Fatalf("visitor.json changed: %s", body)
	}
	if strings.Contains(body, "owner data") {
		t.Fatalf("the owner's data is in the visitor's export")
	}
}
