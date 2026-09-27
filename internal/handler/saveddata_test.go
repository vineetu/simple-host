package handler

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/internal/config"
	db "github.com/vsriram/simple-host/internal/db"
)

// Saved data, step 1 (saveddata.go). Needs DB_DSN (db/schema.sql applied).

// savedDataSite: olive owns shop on a claimed address; vic is a visitor
// signed in there.
type savedDataSite struct {
	a        *privateApp
	olive    person
	vic      person
	oh       string
	shopID   string
	dom      string
	okey     map[string]string
	vicCooky string
}

func newSavedDataSite(t *testing.T) *savedDataSite {
	t.Helper()
	a, _ := newSiteApp(t, "canonical")
	s := &savedDataSite{a: a, olive: a.newPerson(t, "olive"), vic: a.newPerson(t, "vic")}
	a.deploy(t, s.olive, "shop")
	_, s.oh = a.userID(t, s.olive)
	s.shopID = a.siteID(t, s.olive, "shop")
	s.okey = map[string]string{"X-API-Key": s.olive.key}
	s.dom = s.oh + "-store." + pcSiteDomain
	if r := a.at(t, "POST", pcSiteDomain, "/v1/sites/shop/domain", map[string]string{"domain": s.dom}, s.okey); r.status != 200 {
		t.Fatalf("claim: %d %s", r.status, r.body)
	}
	s.vicCooky = a.session(t, s.vic, s.shopID, s.dom)
	return s
}

// ownerAt is an owner request on the apex with the owner's key.
func (s *savedDataSite) owner(t *testing.T, method, path string, body any) resp {
	t.Helper()
	return s.a.at(t, method, pcSiteDomain, path, body, s.okey)
}

// visitor is vic, signed in, from a page on the site's own address.
func (s *savedDataSite) visitor(t *testing.T, method, path string, body any, extra ...string) resp {
	t.Helper()
	return s.a.at(t, method, s.dom, path, body, browser(s.dom, s.vicCooky, extra...))
}

func historyOf(t *testing.T, r resp) []map[string]any {
	t.Helper()
	var body struct {
		History []map[string]any `json:"history"`
	}
	if r.status != 200 || json.Unmarshal(r.body, &body) != nil {
		t.Fatalf("history: %d %s", r.status, r.body)
	}
	return body.History
}

func TestStateHistoryAndRestore(t *testing.T) {
	s := newSavedDataSite(t)
	a := s.a
	state := "/v1/sites/shop/state"
	if r := s.visitor(t, "PUT", state, map[string]any{"votes": 1, "note": "first"}); r.status != 200 {
		t.Fatalf("visitor put: %d %s", r.status, r.body)
	}
	if r := s.visitor(t, "PATCH", state, map[string]any{"ops": []any{map[string]any{"op": "inc", "path": "votes", "by": 2}}}); r.status != 200 {
		t.Fatalf("visitor patch: %d %s", r.status, r.body)
	}
	// The owner's agent wipes it by mistake.
	if r := a.at(t, "PUT", pcSiteDomain, "/v1/u/"+s.oh+"/sites/shop/state", map[string]any{}, map[string]string{"X-API-Key": s.olive.key, "Origin": "https://" + s.dom}); r.status != 200 {
		t.Fatalf("owner put: %d %s", r.status, r.body)
	}
	// Writing the same document again records nothing.
	if r := a.at(t, "PUT", pcSiteDomain, "/v1/u/"+s.oh+"/sites/shop/state", map[string]any{}, map[string]string{"X-API-Key": s.olive.key, "Origin": "https://" + s.dom}); r.status != 200 {
		t.Fatalf("owner put again: %d %s", r.status, r.body)
	}

	h := historyOf(t, s.owner(t, "GET", state+"/history", nil))
	if len(h) != 3 {
		t.Fatalf("want 3 changes, got %d: %v", len(h), h)
	}
	if h[0]["op"] != "replace" || h[0]["by"] != s.olive.email || h[0]["by_kind"] != "owner" {
		t.Errorf("newest change: %v", h[0])
	}
	if h[1]["op"] != "change" || h[1]["by"] != s.vic.email || h[1]["by_kind"] != "visitor" {
		t.Errorf("visitor change: %v", h[1])
	}
	if h[2]["op"] != "replace" || h[2]["by"] != s.vic.email {
		t.Errorf("first change: %v", h[2])
	}
	// Only the owner (and the admin) see history.
	for name, hdr := range map[string]map[string]string{"visitor's key": {"X-API-Key": s.vic.key}, "no key": nil} {
		if r := a.at(t, "GET", pcSiteDomain, state+"/history", nil, hdr); r.status == 200 {
			t.Errorf("%s reads history: %d %s", name, r.status, r.body)
		}
	}
	if r := a.at(t, "GET", pcSiteDomain, state+"/history", nil, map[string]string{"X-API-Key": s.vic.key}); r.status != 404 {
		t.Errorf("visitor's key: %d", r.status)
	}

	wipe := int64(h[0]["id"].(float64))
	one := s.owner(t, "GET", state+"/history/"+itoa(wipe), nil).json(t)
	if v, _ := one["value"].(map[string]any); v == nil || v["votes"] != float64(3) {
		t.Fatalf("value before the wipe: %v", one)
	}
	r := s.owner(t, "POST", state+"/history/"+itoa(wipe)+"/restore", nil)
	if r.status != 200 || r.header.Get("ETag") == "" {
		t.Fatalf("restore: %d %s", r.status, r.body)
	}
	got := a.at(t, "GET", pcSiteDomain, "/v1/u/"+s.oh+"/sites/shop/state", nil, nil)
	if !strings.Contains(string(got.body), `"votes": 3`) && !strings.Contains(string(got.body), `"votes":3`) {
		t.Fatalf("state after restore: %s", got.body)
	}
	// The restore is a change itself, so it can be undone too.
	if h := historyOf(t, s.owner(t, "GET", state+"/history", nil)); len(h) != 4 || h[0]["op"] != "restore" {
		t.Fatalf("after restore: %v", h)
	}
	if r := s.owner(t, "POST", state+"/history/999999999/restore", nil); r.status != 404 {
		t.Errorf("unknown change: %d", r.status)
	}
	// The admin restores on the owner's behalf.
	if r := a.at(t, "GET", pcSiteDomain, state+"/history?limit=1", nil, map[string]string{"X-API-Key": a.admin}); r.status != 200 || len(historyOf(t, r)) > 1 {
		t.Errorf("admin history: %d %s", r.status, r.body)
	}
}

func TestListRecentlyDeletedAndAuthors(t *testing.T) {
	s := newSavedDataSite(t)
	a := s.a
	gb := "/v1/sites/shop/collections/guestbook"
	r := s.visitor(t, "POST", gb, map[string]string{"msg": "hi"})
	if r.status != 201 || strings.Contains(string(r.body), s.vic.email) || r.json(t)["by"] != nil {
		t.Fatalf("visitor post (the answer is unchanged): %d %s", r.status, r.body)
	}
	id := int64(r.json(t)["id"].(float64))
	vid, _ := a.userID(t, s.vic)
	var by string
	if err := a.database.QueryRow(`SELECT submitted_by::text FROM collection_items WHERE id = $1`, id).Scan(&by); err != nil || by != vid {
		t.Fatalf("author not recorded: %q %v", by, err)
	}
	// The owner sees who wrote it; nobody else does.
	if items := itemsOf(t, s.owner(t, "GET", gb, nil)); len(items) != 1 || items[0]["by"] != s.vic.email {
		t.Fatalf("owner read: %v", items)
	}
	for name, hdr := range map[string]map[string]string{
		"public":      nil,
		"page":        {"Origin": "https://" + s.dom},
		"visitor key": {"X-API-Key": s.vic.key},
	} {
		if body := string(a.at(t, "GET", pcSiteDomain, "/v1/u/"+s.oh+gb[len("/v1"):], nil, hdr).body); strings.Contains(body, s.vic.email) || strings.Contains(body, `"by"`) {
			t.Errorf("%s read shows the author: %s", name, body)
		}
	}
	if csv := string(s.owner(t, "GET", gb+"/export.csv", nil).body); !strings.Contains(csv, "sent_by") || !strings.Contains(csv, s.vic.email) {
		t.Errorf("owner CSV: %s", csv)
	}

	// Delete: gone from the list, kept in Recently deleted, restorable.
	if r := s.owner(t, "DELETE", gb+"/items/"+itoa(id), nil); r.status != 204 {
		t.Fatalf("delete: %d %s", r.status, r.body)
	}
	if n := len(itemsOf(t, a.at(t, "GET", pcSiteDomain, "/v1/u/"+s.oh+gb[len("/v1"):], nil, nil))); n != 0 {
		t.Fatalf("deleted item still public: %d", n)
	}
	del := s.owner(t, "GET", gb+"/deleted", nil)
	if items := itemsOf(t, del); del.status != 200 || len(items) != 1 || items[0]["by"] != s.vic.email || items[0]["deleted_at"] == nil {
		t.Fatalf("recently deleted: %d %s", del.status, del.body)
	}
	if r := a.at(t, "GET", pcSiteDomain, gb+"/deleted", nil, map[string]string{"X-API-Key": s.vic.key}); r.status != 404 {
		t.Errorf("visitor reads recently deleted: %d", r.status)
	}
	if r := s.owner(t, "POST", gb+"/items/"+itoa(id)+"/restore", nil); r.status != 200 || r.json(t)["restored"] != float64(1) {
		t.Fatalf("restore item: %d %s", r.status, r.body)
	}
	if r := s.owner(t, "POST", gb+"/items/"+itoa(id)+"/restore", nil); r.status != 404 {
		t.Errorf("restore a live item: %d", r.status)
	}
	if n := len(itemsOf(t, s.owner(t, "GET", gb, nil))); n != 1 {
		t.Fatalf("after restore: %d items", n)
	}

	// Clear: every item moves to Recently deleted; the list still shows up.
	for i := 0; i < 2; i++ {
		if r := s.visitor(t, "POST", gb, map[string]string{"msg": "more"}); r.status != 201 {
			t.Fatal(r.status)
		}
	}
	if r := s.owner(t, "DELETE", gb, map[string]string{"confirm": "guestbook"}); r.status != 200 || r.json(t)["deleted"] != float64(3) || r.json(t)["restorable_days"] != float64(30) {
		t.Fatalf("clear: %d %s", r.status, r.body)
	}
	sum := string(s.owner(t, "GET", "/v1/sites/shop/collections", nil).body)
	if !strings.Contains(sum, `"name":"guestbook"`) || !strings.Contains(sum, `"deleted":3`) {
		t.Fatalf("summary after clear: %s", sum)
	}
	if r := s.owner(t, "POST", gb+"/deleted/restore", map[string]bool{"all": false}); r.status != 400 {
		t.Errorf("restore all without confirming: %d", r.status)
	}
	if r := s.owner(t, "POST", gb+"/deleted/restore", map[string]bool{"all": true}); r.status != 200 || r.json(t)["restored"] != float64(3) {
		t.Fatalf("restore all: %d %s", r.status, r.body)
	}
	if n := len(itemsOf(t, s.owner(t, "GET", gb, nil))); n != 3 {
		t.Fatalf("after restore all: %d", n)
	}
	h := historyOf(t, s.owner(t, "GET", gb+"/history", nil))
	ops := map[string]int{}
	for _, e := range h {
		ops[e["op"].(string)]++
	}
	if ops["delete"] != 1 || ops["clear"] != 3 || ops["undelete"] != 4 {
		t.Fatalf("list history: %v", ops)
	}
	// Undoing a clear entry from History brings that item back too.
	s.owner(t, "DELETE", gb, map[string]string{"confirm": "guestbook"})
	h = historyOf(t, s.owner(t, "GET", gb+"/history", nil))
	if h[0]["op"] != "clear" {
		t.Fatalf("newest: %v", h[0])
	}
	if r := s.owner(t, "POST", gb+"/history/"+itoa(int64(h[0]["id"].(float64)))+"/restore", nil); r.status != 200 {
		t.Fatalf("restore from history: %d %s", r.status, r.body)
	}
	if n := len(itemsOf(t, s.owner(t, "GET", gb, nil))); n != 1 {
		t.Fatalf("after history restore: %d", n)
	}
}

func TestPrivateItemEditIsUndoable(t *testing.T) {
	s := newSavedDataSite(t)
	orders := "/v1/sites/shop/collections/orders"
	if r := s.owner(t, "PUT", orders+"/privacy", map[string]bool{"private": true}); r.status != 200 {
		t.Fatalf("privacy: %d %s", r.status, r.body)
	}
	r := s.visitor(t, "POST", orders, map[string]string{"item": "mug", "qty": "1"})
	if r.status != 201 {
		t.Fatalf("order: %d %s", r.status, r.body)
	}
	id := itoa(int64(r.json(t)["id"].(float64)))
	if r := s.owner(t, "PATCH", orders+"/items/"+id, map[string]any{"qty": "5", "status": "shipped"}); r.status != 200 {
		t.Fatalf("edit: %d %s", r.status, r.body)
	}
	h := historyOf(t, s.owner(t, "GET", orders+"/history", nil))
	if len(h) != 1 || h[0]["op"] != "edit" || h[0]["by"] != s.olive.email {
		t.Fatalf("history: %v", h)
	}
	if r := s.owner(t, "POST", orders+"/history/"+itoa(int64(h[0]["id"].(float64)))+"/restore", nil); r.status != 200 {
		t.Fatalf("restore: %d %s", r.status, r.body)
	}
	items := itemsOf(t, s.owner(t, "GET", orders, nil))
	d := items[0]["data"].(map[string]any)
	if d["qty"] != "1" || d["status"] != nil || d["_submitted_by"] != s.vic.email || items[0]["by"] != s.vic.email {
		t.Fatalf("after restore: %v", items[0])
	}
	// Private lists stay off the shared host here too.
	if r := s.a.at(t, "GET", pcContentHost, orders+"/history", nil, s.okey); r.status != 404 {
		t.Errorf("history of a private list on the shared host: %d", r.status)
	}
}

func TestIdempotencyKey(t *testing.T) {
	s := newSavedDataSite(t)
	gb := "/v1/sites/shop/collections/guestbook"
	first := s.visitor(t, "POST", gb, map[string]string{"msg": "once"}, "Idempotency-Key", "k-1")
	again := s.visitor(t, "POST", gb, map[string]string{"msg": "once"}, "Idempotency-Key", "k-1")
	if first.status != 201 || again.status != 201 || string(first.body) != string(again.body) || again.header.Get("Idempotent-Replayed") != "true" {
		t.Fatalf("retry: %d %s / %d %s", first.status, first.body, again.status, again.body)
	}
	if r := s.visitor(t, "POST", gb, map[string]string{"msg": "twice"}, "Idempotency-Key", "k-2"); r.status != 201 {
		t.Fatal(r.status)
	}
	if n := len(itemsOf(t, s.owner(t, "GET", gb, nil))); n != 2 {
		t.Fatalf("want 2 items, got %d", n)
	}
	// A retried inc counts once.
	state := "/v1/sites/shop/state"
	inc := map[string]any{"ops": []any{map[string]any{"op": "inc", "path": "n"}}}
	for i := 0; i < 2; i++ {
		if r := s.visitor(t, "PATCH", state, inc, "Idempotency-Key", "inc-1"); r.status != 200 || r.header.Get("ETag") == "" {
			t.Fatalf("patch %d: %d %s", i, r.status, r.body)
		}
	}
	if body := string(s.a.at(t, "GET", pcSiteDomain, "/v1/u/"+s.oh+"/sites/shop/state", nil, nil).body); !strings.Contains(body, `"n": 1`) {
		t.Fatalf("inc applied twice: %s", body)
	}
	// A failed write is not remembered: the retry runs.
	bad := s.visitor(t, "POST", gb, "not json", "Idempotency-Key", "k-3")
	good := s.visitor(t, "POST", gb, map[string]string{"msg": "fixed"}, "Idempotency-Key", "k-3")
	if bad.status != 400 || good.status != 201 {
		t.Fatalf("failed then retried: %d / %d %s", bad.status, good.status, good.body)
	}
	if r := s.visitor(t, "POST", gb, map[string]string{"msg": "x"}, "Idempotency-Key", strings.Repeat("k", 300)); r.status != 400 || r.json(t)["code"] != "invalid_idempotency_key" {
		t.Errorf("long key: %d %s", r.status, r.body)
	}
}

func TestPatchKeepsNumbersExact(t *testing.T) {
	s := newSavedDataSite(t)
	state := "/v1/sites/shop/state"
	if r := s.visitor(t, "PUT", state, `{"big": 9007199254740993, "price": 19.99, "rows": [{"n": 1}, {"n": 2}]}`); r.status != 200 {
		t.Fatalf("put: %d %s", r.status, r.body)
	}
	r := s.visitor(t, "PATCH", state, `{"ops": [{"op": "inc", "path": "big", "by": 1}, {"op": "inc", "path": "price", "by": 0.01}, {"op": "removeWhere", "path": "rows", "match": {"n": 1.0}}, {"op": "set", "path": "id", "value": 12345678901234567890}]}`)
	if r.status != 200 {
		t.Fatalf("patch: %d %s", r.status, r.body)
	}
	body := string(r.body)
	for _, want := range []string{`"big":9007199254740994`, `"id":12345678901234567890`, `"rows":[{"n":2}]`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %s in %s", want, body)
		}
	}
	if r := s.visitor(t, "PATCH", state, `{"ops": [{"op": "inc", "path": "big", "by": "1"}]}`); r.status != 400 {
		t.Errorf("inc by a string: %d %s", r.status, r.body)
	}
}

func TestDataWatchCounts(t *testing.T) {
	s := newSavedDataSite(t)
	state := "/v1/sites/shop/state"
	s.visitor(t, "PUT", state, `[1, 2]`)
	s.visitor(t, "PUT", state, `{"a": 1}`)
	s.visitor(t, "PATCH", state, `{"ops": [{"op": "inc", "path": "a", "by": 50}, {"op": "inc", "path": "a"}, {"op": "set", "path": "b", "value": 1}]}`)
	s.visitor(t, "POST", "/v1/sites/shop/collections/brandnew", `{"x": "`+strings.Repeat("y", 17<<10)+`"}`)
	// The owner's own replace is not a visitor's.
	s.a.at(t, "PUT", pcSiteDomain, "/v1/u/"+s.oh+"/sites/shop/state", map[string]any{"c": 1}, map[string]string{"X-API-Key": s.olive.key, "Origin": "https://" + s.dom})

	if r := s.a.at(t, "GET", pcSiteDomain, "/v1/admin/data-watch", nil, s.okey); r.status == 200 {
		t.Fatalf("a non-admin reads the watch")
	}
	r := s.a.at(t, "GET", pcSiteDomain, "/v1/admin/data-watch", nil, map[string]string{"X-API-Key": s.a.admin})
	if r.status != 200 {
		t.Fatalf("watch: %d %s", r.status, r.body)
	}
	var body struct {
		WatchDays int `json:"watch_days"`
		Sites     []struct {
			SiteID  string           `json:"site_id"`
			Metrics map[string]int64 `json:"metrics"`
		} `json:"sites"`
	}
	if err := json.Unmarshal(r.body, &body); err != nil || body.WatchDays != 7 {
		t.Fatalf("watch body: %s", r.body)
	}
	var got map[string]int64
	for _, site := range body.Sites {
		if site.SiteID == s.shopID {
			got = site.Metrics
		}
	}
	want := map[string]int64{"put_by_visitor": 2, "put_not_object": 1, "visitor_op_inc": 2, "visitor_inc_large": 1, "visitor_op_set": 1, "new_list_name": 1, "item_large": 1}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %d, want %d (all: %v)", k, got[k], v, got)
		}
	}
}

func TestSavedDataLimitsAndSweep(t *testing.T) {
	s := newSavedDataSite(t)
	a := s.a
	c := config.DefaultSavedData()
	c.ReadBurst, c.ReadPerSec = 2, 1
	a.sites.SetSavedData(c)
	for i := 0; i < 2; i++ {
		if r := a.at(t, "GET", pcSiteDomain, "/v1/sites/shop/state", nil, map[string]string{"X-Forwarded-For": "203.0.113.9"}); r.status != 200 {
			t.Fatalf("read %d: %d", i, r.status)
		}
	}
	if r := a.at(t, "GET", pcSiteDomain, "/v1/sites/shop/state", nil, map[string]string{"X-Forwarded-For": "203.0.113.9"}); r.status != 429 || r.json(t)["code"] != "rate_limited" {
		t.Fatalf("read over the limit: %d %s", r.status, r.body)
	}

	// Site full: a site past SAVED_DATA_SITE_MAX_MB takes no more.
	c = config.DefaultSavedData()
	c.SiteMaxMB = 1
	a.sites.SetSavedData(c)
	// Incompressible, so it takes its real size as stored.
	noise := make([]byte, 900<<10)
	if _, err := rand.Read(noise); err != nil {
		t.Fatal(err)
	}
	big, _ := json.Marshal(map[string]string{"blob": base64.StdEncoding.EncodeToString(noise)})
	if _, err := a.database.Exec(`UPDATE sites SET state = $2::jsonb WHERE id = $1`, s.shopID, string(big)); err != nil {
		t.Fatal(err)
	}
	r := s.visitor(t, "POST", "/v1/sites/shop/collections/x", `{"b": 1}`)
	if r.status != http.StatusInsufficientStorage || r.json(t)["code"] != "site_full" {
		t.Fatalf("full site: %d %s", r.status, r.body)
	}
	a.sites.SetSavedData(config.DefaultSavedData())

	// The sweep: history and deleted items past the undo window go for good.
	gb := "/v1/sites/shop/collections/guestbook"
	id := itoa(int64(s.visitor(t, "POST", gb, map[string]string{"m": "old"}).json(t)["id"].(float64)))
	s.owner(t, "DELETE", gb+"/items/"+id, nil)
	if _, err := a.database.Exec(`UPDATE collection_items SET deleted_at = now() - interval '31 days' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := a.database.Exec(`INSERT INTO data_history (site_id, kind, name, op, prev, actor_kind, created_at) VALUES ($1, 'state', '', 'replace', '{"old": true}', 'owner', now() - interval '31 days')`, s.shopID); err != nil {
		t.Fatal(err)
	}
	a.sites.sweepSavedData(context.Background())
	var items, hist int
	a.database.QueryRow(`SELECT count(*) FROM collection_items WHERE id = $1`, id).Scan(&items)
	a.database.QueryRow(`SELECT count(*) FROM data_history WHERE site_id = $1 AND created_at < now() - interval '30 days'`, s.shopID).Scan(&hist)
	if items != 0 || hist != 0 {
		t.Fatalf("sweep left item=%d history=%d", items, hist)
	}
}

func TestHistoryThinningKeepsOnePerDay(t *testing.T) {
	s := newSavedDataSite(t)
	a := s.a
	// Ten versions today and three yesterday, each ~1 KB.
	v := `{"v": "` + strings.Repeat("a", 1000) + `"}`
	for i := 0; i < 3; i++ {
		a.database.Exec(`INSERT INTO data_history (site_id, kind, name, op, prev, actor_kind, created_at) VALUES ($1, 'state', '', 'change', $2::jsonb, 'visitor', now() - interval '1 day' + make_interval(secs => $3))`, s.shopID, v, i)
	}
	for i := 0; i < 10; i++ {
		a.database.Exec(`INSERT INTO data_history (site_id, kind, name, op, prev, actor_kind) VALUES ($1, 'state', '', 'change', $2::jsonb, 'visitor')`, s.shopID, v)
	}
	var firstToday, firstYesterday int64
	a.database.QueryRow(`SELECT min(id) FROM data_history WHERE site_id = $1 AND created_at > now() - interval '1 hour'`, s.shopID).Scan(&firstToday)
	a.database.QueryRow(`SELECT min(id) FROM data_history WHERE site_id = $1 AND created_at < now() - interval '1 hour'`, s.shopID).Scan(&firstYesterday)
	// A cap far below what is held: only the first of each day survives.
	if _, err := db.ThinSiteHistory(context.Background(), a.database, s.shopID, 1); err != nil {
		t.Fatal(err)
	}
	rows, _ := a.database.Query(`SELECT id FROM data_history WHERE site_id = $1 ORDER BY id`, s.shopID)
	var kept []int64
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		kept = append(kept, id)
	}
	rows.Close()
	if len(kept) != 2 || kept[0] != firstYesterday || kept[1] != firstToday {
		// Midnight UTC between the two batches of "today" rows is possible
		// but not between inserts a millisecond apart.
		t.Fatalf("kept %v, want [%d %d]", kept, firstYesterday, firstToday)
	}
}

// Deleting an account takes the person's entries in other people's lists,
// public ones included now that they are recorded, and their address out of
// the history of other people's saved data.
func TestAccountDeletionTakesAuthoredData(t *testing.T) {
	s := newSavedDataSite(t)
	a := s.a
	if r := s.visitor(t, "POST", "/v1/sites/shop/collections/guestbook", map[string]string{"msg": "mine"}); r.status != 201 {
		t.Fatal(r.status)
	}
	s.visitor(t, "PUT", "/v1/sites/shop/state", map[string]int{"a": 1})
	s.visitor(t, "PUT", "/v1/sites/shop/state", map[string]int{"a": 2})
	vid, vh := a.userID(t, s.vic)
	if r := a.at(t, "DELETE", pcSiteDomain, "/v1/me", map[string]string{"confirm": vh}, map[string]string{"X-API-Key": s.vic.key}); r.status != http.StatusNoContent {
		t.Fatalf("delete account: %d %s", r.status, r.body)
	}
	var entries, named int
	a.database.QueryRow(`SELECT count(*) FROM collection_items WHERE site_id = $1`, s.shopID).Scan(&entries)
	a.database.QueryRow(`SELECT count(*) FROM data_history WHERE site_id = $1 AND (actor_email IS NOT NULL OR actor_id = $2)`, s.shopID, vid).Scan(&named)
	if entries != 0 || named != 0 {
		t.Fatalf("left entries=%d named history=%d", entries, named)
	}
	if h := historyOf(t, s.owner(t, "GET", "/v1/sites/shop/state/history", nil)); len(h) != 2 {
		t.Fatalf("the owner's earlier versions must stay: %v", h)
	}
}
