package handler

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/internal/config"
	db "github.com/vsriram/simple-host/internal/db"
)

// Saved data, steps 3 and 4: Personal (personal.go) and Shared board
// (board.go). Needs DB_DSN (db/schema.sql applied).

func recordOf(t *testing.T, r resp) map[string]any {
	t.Helper()
	if r.status != 200 {
		t.Fatalf("record: %d %s", r.status, r.body)
	}
	d, _ := r.json(t)["data"].(map[string]any)
	return d
}

func TestPersonalOnlyItsPerson(t *testing.T) {
	s := newKindsSite(t, false)
	const p = "/v1/sites/shop/data/habits"
	wantCode(t, "options", s.owner(t, "PUT", p+"/kind", map[string]any{"kind": "mine", "visibility": "owner"}), 400, "invalid_kind")
	out := s.declare(t, "habits", map[string]any{"kind": "personal"})
	if out["kind"] != "mine" || out["label"] != "Personal" {
		t.Fatalf("declare: %v", out)
	}
	// Before saving: nothing, for the visitor.
	if r := s.visitor(t, "GET", p, nil); r.status != 200 || r.json(t)["data"] != nil {
		t.Fatalf("empty: %d %s", r.status, r.body)
	}
	wantCode(t, "nobody signed in", s.a.at(t, "GET", s.dom, p, nil, browser(s.dom, "")), 401, "")
	if r := s.visitor(t, "PUT", p, map[string]any{"streak": 1, "theme": "dark"}); r.status != 200 {
		t.Fatalf("put: %d %s", r.status, r.body)
	}
	if r := s.visitor(t, "PATCH", p, map[string]any{"ops": []any{map[string]any{"op": "inc", "path": "streak"}}}); r.status != 200 || recordOf(t, r)["streak"] != float64(2) {
		t.Fatalf("patch: %d %s", r.status, r.body)
	}
	if d := recordOf(t, s.visitor(t, "GET", p, nil)); d["streak"] != float64(2) || d["theme"] != "dark" {
		t.Fatalf("read back: %v", d)
	}
	// Another visitor has their own, empty; theirs never shows vic's.
	if r := s.as(t, s.wesCooky, "GET", p, nil); r.json(t)["data"] != nil {
		t.Fatalf("wes sees: %s", r.body)
	}
	if r := s.as(t, s.wesCooky, "PUT", p, map[string]any{"streak": 9}); r.status != 200 {
		t.Fatalf("wes put: %d %s", r.status, r.body)
	}
	if d := recordOf(t, s.visitor(t, "GET", p, nil)); d["streak"] != float64(2) {
		t.Fatalf("vic's record changed by wes: %v", d)
	}
	// The owner signed in on the site reads their own record, not anyone's.
	ownerCookie := s.a.session(t, s.olive, s.shopID, s.dom)
	if r := s.as(t, ownerCookie, "GET", p, nil); r.json(t)["data"] != nil {
		t.Fatalf("owner browser sees: %s", r.body)
	}
	// The owner's key (and the connector) never reads a record: counts only.
	wantCode(t, "owner key read", s.owner(t, "GET", p, nil), 403, "personal_data")
	// Two people: "fewer than 3", never the number.
	if r := s.owner(t, "GET", p+"?count=1", nil); r.status != 200 || r.json(t)["count"] != float64(0) || r.json(t)["few"] != true {
		t.Fatalf("owner count: %d %s", r.status, r.body)
	}
	wantCode(t, "owner list, old route", s.owner(t, "GET", "/v1/sites/shop/collections/habits", nil), 403, "personal_data")
	wantCode(t, "owner list, from the page", s.ownerPage(t, "GET", "/v1/sites/shop/collections/habits", nil), 403, "personal_data")
	wantCode(t, "owner browser list", s.as(t, ownerCookie, "GET", "/v1/sites/shop/collections/habits", nil), 403, "personal_data")
	wantCode(t, "public list read", s.a.at(t, "GET", s.dom, "/v1/sites/shop/collections/habits", nil, nil), 403, "personal_data")
	wantCode(t, "csv", s.owner(t, "GET", "/v1/sites/shop/collections/habits/export.csv", nil), 403, "personal_data")
	wantCode(t, "owner history", s.owner(t, "GET", "/v1/sites/shop/collections/habits/history", nil), 403, "personal_data")
	wantCode(t, "owner deleted", s.owner(t, "GET", "/v1/sites/shop/collections/habits/deleted", nil), 403, "personal_data")
	wantCode(t, "owner put with key", s.owner(t, "PUT", p, map[string]any{"x": 1}), 403, "personal_data")
	wantCode(t, "owner post", s.ownerPage(t, "POST", "/v1/sites/shop/collections/habits", map[string]any{"x": 1}), 409, "wrong_kind")
	wantCode(t, "visitor post", s.visitor(t, "POST", p, map[string]any{"x": 1}), 409, "wrong_kind")
	var vicID int64
	if err := s.a.database.QueryRow(`SELECT id FROM collection_items WHERE site_id = $1 AND collection = 'habits' AND data->>'theme' = 'dark'`, s.shopID).Scan(&vicID); err != nil {
		t.Fatal(err)
	}
	id := strconv.FormatInt(vicID, 10)
	wantCode(t, "owner edit one", s.owner(t, "PATCH", "/v1/sites/shop/collections/habits/items/"+id, map[string]any{"x": 1}), 403, "personal_data")
	wantCode(t, "owner delete one", s.owner(t, "DELETE", "/v1/sites/shop/collections/habits/items/"+id, nil), 403, "personal_data")
	wantCode(t, "owner history entry", s.owner(t, "GET", "/v1/sites/shop/collections/habits/history/1", nil), 403, "personal_data")
	wantCode(t, "owner restore one", s.owner(t, "POST", "/v1/sites/shop/collections/habits/items/"+id+"/restore", nil), 403, "personal_data")
	wantCode(t, "block by record", s.owner(t, "POST", "/v1/sites/shop/savers/block", map[string]any{"collection": "habits", "id": vicID}), 403, "personal_data")
	wantCode(t, "item route", s.visitor(t, "PATCH", p+"/items/"+id, map[string]any{"x": 1}), 409, "wrong_kind")
	wantCode(t, "privacy", s.owner(t, "PUT", "/v1/sites/shop/collections/habits/privacy", map[string]any{"private": false}), 409, "wrong_kind")
	// The owner's list shows how many people and their size, never the data;
	// with one or two people, neither (they would point at one person).
	var summary map[string]any
	for _, n := range s.owner(t, "GET", "/v1/sites/shop/data", nil).json(t)["names"].([]any) {
		if m := n.(map[string]any); m["name"] == "habits" {
			summary = m
		}
	}
	if summary["label"] != "Personal" || summary["count"] != float64(0) || summary["bytes"] != float64(0) || summary["few"] != true || summary["last_at"] != nil {
		t.Fatalf("summary: %v", summary)
	}
	// The site's export carries nobody's record but the owner's own.
	items, err := db.ListExportItems(context.Background(), s.a.database, s.shopID)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		if it.Collection == "habits" {
			t.Fatalf("site export holds a personal record: %s", it.Data)
		}
	}
	// It cannot become another kind while it holds records.
	wantCode(t, "to entries", s.owner(t, "PUT", p+"/kind", map[string]any{"kind": "entries"}), 409, "has_records")
	wantCode(t, "to board", s.owner(t, "PUT", p+"/kind", map[string]any{"kind": "board"}), 409, "has_records")
	// A name that holds data never becomes Personal.
	s.declare(t, "notes", map[string]any{"kind": "entries", "visibility": "public"})
	if r := s.visitor(t, "POST", "/v1/sites/shop/data/notes", map[string]any{"n": 1}); r.status != 201 {
		t.Fatalf("note: %d %s", r.status, r.body)
	}
	wantCode(t, "notes to personal", s.owner(t, "PUT", "/v1/sites/shop/data/notes/kind", map[string]any{"kind": "mine"}), 409, "has_entries")
}

func TestPersonalLimitsHistoryAndErase(t *testing.T) {
	s := newKindsSite(t, false)
	const p = "/v1/sites/shop/data/progress"
	s.declare(t, "progress", map[string]any{"kind": "mine"})
	s.setLimits(t, func(c *config.SavedData) { c.PersonalMaxKB = 1 })
	wantCode(t, "not an object", s.visitor(t, "PUT", p, []any{1}), 400, "not_an_object")
	wantCode(t, "too large", s.visitor(t, "PUT", p, map[string]any{"x": strings.Repeat("a", 1100)}), 413, "item_too_large")
	if r := s.visitor(t, "PUT", p, map[string]any{"level": 1}); r.status != 200 {
		t.Fatalf("put: %d %s", r.status, r.body)
	}
	wantCode(t, "patch past the cap", s.visitor(t, "PATCH", p, map[string]any{"ops": []any{map[string]any{"op": "set", "path": "big", "value": strings.Repeat("b", 1100)}}}), 413, "item_too_large")
	// A write without the CSRF header, or from another origin, is refused.
	wantCode(t, "no csrf", s.visitor(t, "PUT", p, map[string]any{"level": 5}, "X-SH-CSRF", ""), 403, "csrf_required")
	// The same Idempotency-Key saves once.
	for i := 0; i < 2; i++ {
		if r := s.visitor(t, "PUT", p, map[string]any{"level": 2}, "Idempotency-Key", "k-1"); r.status != 200 || recordOf(t, r)["level"] != float64(2) {
			t.Fatalf("idempotent put %d: %d %s", i, r.status, r.body)
		}
	}
	hist := historyOf(t, s.visitor(t, "GET", p+"/history", nil))
	if len(hist) != 1 {
		t.Fatalf("history after two writes and a replay: %v", hist)
	}
	// ETag: a poll with the same tag is 304.
	r := s.visitor(t, "GET", p, nil)
	etag := r.header.Get("ETag")
	if etag == "" {
		t.Fatal("no ETag on a personal record")
	}
	if r := s.visitor(t, "GET", p, nil, "If-None-Match", etag); r.status != http.StatusNotModified {
		t.Fatalf("If-None-Match: %d", r.status)
	}
	// Their own history: the value before, and a restore.
	hid := strconv.FormatInt(int64(hist[0]["id"].(float64)), 10)
	if v := s.visitor(t, "GET", p+"/history/"+hid, nil).json(t)["value"].(map[string]any); v["level"] != float64(1) {
		t.Fatalf("value before: %v", v)
	}
	wantCode(t, "someone else's change", s.as(t, s.wesCooky, "GET", p+"/history/"+hid, nil), 404, "not_found")
	wantCode(t, "someone else's restore", s.as(t, s.wesCooky, "POST", p+"/history/"+hid+"/restore", nil), 404, "not_found")
	if r := s.visitor(t, "POST", p+"/history/"+hid+"/restore", nil); r.status != 200 || recordOf(t, r)["level"] != float64(1) {
		t.Fatalf("restore: %d %s", r.status, r.body)
	}
	// Delete their own, and bring it back.
	if r := s.visitor(t, "DELETE", p, nil); r.status != 200 {
		t.Fatalf("delete: %d %s", r.status, r.body)
	}
	if r := s.visitor(t, "GET", p, nil); r.json(t)["data"] != nil {
		t.Fatalf("after delete: %s", r.body)
	}
	wantCode(t, "delete again", s.visitor(t, "DELETE", p, nil), 404, "not_found")
	hist = historyOf(t, s.visitor(t, "GET", p+"/history", nil))
	if hist[0]["op"] != "delete" {
		t.Fatalf("latest change: %v", hist[0])
	}
	del := strconv.FormatInt(int64(hist[0]["id"].(float64)), 10)
	if r := s.visitor(t, "POST", p+"/history/"+del+"/restore", nil); r.status != 200 || recordOf(t, r)["level"] != float64(1) {
		t.Fatalf("undelete: %d %s", r.status, r.body)
	}
	// Saving after a delete reuses the one row: never two records.
	if r := s.visitor(t, "DELETE", p, nil); r.status != 200 {
		t.Fatal("delete")
	}
	if r := s.visitor(t, "PUT", p, map[string]any{"level": 7}); r.status != 200 {
		t.Fatalf("put after delete: %d %s", r.status, r.body)
	}
	var rows int
	_ = s.a.database.QueryRow(`SELECT count(*) FROM collection_items WHERE site_id = $1 AND collection = 'progress'`, s.shopID).Scan(&rows)
	if rows != 1 {
		t.Fatalf("rows for one person: %d", rows)
	}

	// The owner clears for everyone and brings back what the clear took, but
	// not a record its person deleted.
	if r := s.as(t, s.wesCooky, "PUT", p, map[string]any{"level": 3}); r.status != 200 {
		t.Fatal("wes put")
	}
	if r := s.as(t, s.wesCooky, "DELETE", p, nil); r.status != 200 {
		t.Fatal("wes delete")
	}
	if r := s.owner(t, "DELETE", "/v1/sites/shop/collections/progress", map[string]any{"confirm": "progress"}); r.status != 200 {
		t.Fatalf("clear: %d %s", r.status, r.body)
	}
	if r := s.visitor(t, "GET", p, nil); r.json(t)["data"] != nil {
		t.Fatalf("after clear: %s", r.body)
	}
	if r := s.owner(t, "POST", "/v1/sites/shop/collections/progress/deleted/restore", map[string]any{"all": true}); r.status != 200 || r.json(t)["restored"] != float64(1) {
		t.Fatalf("restore all: %d %s", r.status, r.body)
	}
	if d := recordOf(t, s.visitor(t, "GET", p, nil)); d["level"] != float64(7) {
		t.Fatalf("vic after restore: %v", d)
	}
	if r := s.as(t, s.wesCooky, "GET", p, nil); r.json(t)["data"] != nil {
		t.Fatalf("wes's own delete came back: %s", r.body)
	}
	// The owner's clear shows in the visitor's history without the owner's address.
	sawClear := false
	for _, e := range historyOf(t, s.visitor(t, "GET", p+"/history", nil)) {
		if e["op"] == "clear" {
			sawClear = true
			if e["by"] != nil || e["by_kind"] != "owner" {
				t.Fatalf("clear as the visitor sees it: %v", e)
			}
		}
	}
	if !sawClear {
		t.Fatal("no clear in the visitor's history")
	}

	// The visitor's "Download my data" has it; deleting their account erases it.
	vicID, _ := s.a.userID(t, s.vic)
	subs, err := db.ListSubmissionsElsewhere(context.Background(), s.a.database, vicID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, sub := range subs {
		found = found || (sub.Collection == "progress" && strings.Contains(string(sub.Data), `"level": 7`))
	}
	if !found {
		t.Fatalf("export lacks the record: %+v", subs)
	}
	tx, err := s.a.database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	acct, err := db.LockAccountForDelete(context.Background(), tx, vicID)
	if err == nil {
		_, err = db.EraseAccount(context.Background(), tx, acct)
	}
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var left, hist2 int
	_ = s.a.database.QueryRow(`SELECT count(*) FROM collection_items WHERE site_id = $1 AND collection = 'progress' AND data->>'level' = '7'`, s.shopID).Scan(&left)
	_ = s.a.database.QueryRow(`SELECT count(*) FROM data_history WHERE site_id = $1 AND name = 'progress' AND prev->>'level' = '1'`, s.shopID).Scan(&hist2)
	if left != 0 || hist2 != 0 {
		t.Fatalf("after erase: %d records, %d history rows", left, hist2)
	}
}

func TestSharedBoardRules(t *testing.T) {
	s := newKindsSite(t, false)
	const p = "/v1/sites/shop/data/todo"
	wantCode(t, "options", s.owner(t, "PUT", p+"/kind", map[string]any{"kind": "board", "one_per_person": true}), 400, "invalid_kind")
	if out := s.declare(t, "todo", map[string]any{"kind": "Shared board"}); out["kind"] != "board" {
		t.Fatalf("declare: %v", out)
	}
	anon := func(method, path string, body any) resp {
		return s.a.at(t, method, s.dom, path, body, browser(s.dom, ""))
	}
	wantCode(t, "anonymous add", anon("POST", p, map[string]any{"text": "x"}), 401, "")
	wantCode(t, "not an object", s.visitor(t, "POST", p, []any{1}), 400, "not_an_object")
	r := s.visitor(t, "POST", p, map[string]any{"text": "milk"})
	if r.status != 201 || r.json(t)["version"] != float64(1) {
		t.Fatalf("add: %d %s", r.status, r.body)
	}
	id := idOf(t, r)
	// Anyone reads it, without who added each; the owner sees who.
	items := itemsOf(t, anon("GET", p, nil))
	if len(items) != 1 || items[0]["by"] != nil || items[0]["version"] != float64(1) {
		t.Fatalf("public read: %v", items)
	}
	if items := itemsOf(t, s.owner(t, "GET", "/v1/sites/shop/collections/todo", nil)); items[0]["by"] == nil {
		t.Fatalf("owner read: %v", items)
	}
	// Any signed-in visitor changes any item; a stale version is refused.
	if r := s.a.at(t, "PATCH", s.dom, p+"/items/"+id, map[string]any{"done": true}, browser(s.dom, s.wesCooky, "If-Match", `"1"`)); r.status != 200 || r.json(t)["version"] != float64(2) {
		t.Fatalf("wes edit: %d %s", r.status, r.body)
	}
	r = s.visitor(t, "PATCH", p+"/items/"+id, map[string]any{"text": "oat milk"}, "If-Match", `"1"`)
	wantCode(t, "stale version", r, 409, "version_conflict")
	if it, _ := r.json(t)["item"].(map[string]any); it["version"] != float64(2) {
		t.Fatalf("conflict answer: %s", r.body)
	}
	if r := s.visitor(t, "PATCH", p+"/items/"+id, map[string]any{"text": "oat milk"}); r.status != 200 || r.json(t)["version"] != float64(3) {
		t.Fatalf("unconditional edit: %d %s", r.status, r.body)
	}
	wantCode(t, "anonymous edit", anon("PATCH", p+"/items/"+id, map[string]any{"x": 1}), 401, "")
	// ETag polling.
	g := anon("GET", p, nil)
	etag := g.header.Get("ETag")
	if etag == "" {
		t.Fatal("no ETag on a board")
	}
	if r := s.a.at(t, "GET", s.dom, p, nil, map[string]string{"If-None-Match": etag}); r.status != http.StatusNotModified {
		t.Fatalf("unchanged board: %d", r.status)
	}
	// Delete by someone else, undo by the one who deleted only.
	if r := s.as(t, s.wesCooky, "DELETE", p+"/items/"+id, nil); r.status != 200 {
		t.Fatalf("wes delete: %d %s", r.status, r.body)
	}
	if r := s.a.at(t, "GET", s.dom, p, nil, map[string]string{"If-None-Match": etag}); r.status != 200 {
		t.Fatalf("changed board: %d", r.status)
	}
	wantCode(t, "undo by another", s.visitor(t, "POST", p+"/items/"+id+"/undo", nil), 409, "undo_expired")
	if r := s.as(t, s.wesCooky, "POST", p+"/items/"+id+"/undo", nil); r.status != 200 {
		t.Fatalf("wes undo: %d %s", r.status, r.body)
	}
	// Nobody but the owner empties or replaces it.
	wantCode(t, "visitor replace", s.visitor(t, "PUT", p, map[string]any{"x": 1}), 409, "wrong_kind")
	if r := s.visitor(t, "DELETE", "/v1/sites/shop/collections/todo", map[string]any{"confirm": "todo"}); r.status != 404 {
		t.Fatalf("visitor clear: %d %s", r.status, r.body)
	}
	wantCode(t, "visitor delete all", s.visitor(t, "DELETE", p, nil), 409, "wrong_kind")
	wantCode(t, "privacy", s.owner(t, "PUT", "/v1/sites/shop/collections/todo/privacy", map[string]any{"private": true}), 409, "wrong_kind")
	// The owner edits items too (key, both routes) and restores from history.
	if r := s.ownerPage(t, "PATCH", "/v1/sites/shop/collections/todo/items/"+id, map[string]any{"qty": 2}); r.status != 200 {
		t.Fatalf("owner edit, old route: %d %s", r.status, r.body)
	}
	if r := s.owner(t, "PATCH", p+"/items/"+id, map[string]any{"qty": 3}); r.status != 200 {
		t.Fatalf("owner edit: %d %s", r.status, r.body)
	}
	if hist := historyOf(t, s.owner(t, "GET", "/v1/sites/shop/collections/todo/history", nil)); len(hist) < 4 {
		t.Fatalf("history: %v", hist)
	}
	// A blocked person can no longer add, change or delete.
	if r := s.owner(t, "POST", "/v1/sites/shop/savers/block", map[string]any{"email": s.wes.email}); r.status != 200 {
		t.Fatalf("block: %d %s", r.status, r.body)
	}
	wantCode(t, "blocked add", s.as(t, s.wesCooky, "POST", p, map[string]any{"text": "x"}), 403, "not_allowed_to_save")
	wantCode(t, "blocked edit", s.as(t, s.wesCooky, "PATCH", p+"/items/"+id, map[string]any{"x": 1}), 403, "not_allowed_to_save")
	wantCode(t, "blocked delete", s.as(t, s.wesCooky, "DELETE", p+"/items/"+id, nil), 403, "not_allowed_to_save")
	// Limits: item size and count.
	s.setLimits(t, func(c *config.SavedData) { c.BoardItemMaxKB = 1; c.BoardMax = 1 })
	wantCode(t, "too large", s.visitor(t, "POST", p, map[string]any{"text": strings.Repeat("a", 1100)}), 413, "item_too_large")
	wantCode(t, "full", s.visitor(t, "POST", p, map[string]any{"text": "eggs"}), 409, "list_full")
	// The owner clears it (typed confirmation), restorable.
	if r := s.owner(t, "DELETE", "/v1/sites/shop/collections/todo", map[string]any{"confirm": "todo"}); r.status != 200 {
		t.Fatalf("owner clear: %d %s", r.status, r.body)
	}
	wantCode(t, "board restore all without a window", s.owner(t, "POST", "/v1/sites/shop/collections/todo/deleted/restore", map[string]any{"all": true}), 400, "window_required")
	if r := s.owner(t, "POST", "/v1/sites/shop/collections/todo/deleted/restore", map[string]any{"all": true, "within_minutes": 60}); r.status != 200 || r.json(t)["restored"] != float64(1) {
		t.Fatalf("restore: %d %s", r.status, r.body)
	}
}

func TestBoardFromPrivateNeedsConfirm(t *testing.T) {
	s := newKindsSite(t, false)
	s.declare(t, "ideas", map[string]any{"kind": "entries"})
	if r := s.visitor(t, "POST", "/v1/sites/shop/data/ideas", map[string]any{"idea": "x"}); r.status != 201 {
		t.Fatalf("entry: %d %s", r.status, r.body)
	}
	wantCode(t, "private to board", s.owner(t, "PUT", "/v1/sites/shop/data/ideas/kind", map[string]any{"kind": "board"}), 409, "confirm_public")
	if r := s.owner(t, "PUT", "/v1/sites/shop/data/ideas/kind", map[string]any{"kind": "board", "confirm_public": true}); r.status != 200 {
		t.Fatalf("confirmed: %d %s", r.status, r.body)
	}
	if items := itemsOf(t, s.a.at(t, "GET", s.dom, "/v1/sites/shop/data/ideas", nil, nil)); len(items) != 1 || items[0]["data"].(map[string]any)["_submitted_by"] != nil {
		t.Fatalf("board read: %v", items)
	}
}

func TestPersonalOtherHostsAndConnector(t *testing.T) {
	s := newKindsSite(t, false)
	s.declare(t, "prefs", map[string]any{"kind": "mine"})
	if r := s.visitor(t, "PUT", "/v1/sites/shop/data/prefs", map[string]any{"theme": "dark"}); r.status != 200 {
		t.Fatalf("put: %d %s", r.status, r.body)
	}
	// The shared content host and the handle path never read a record, even
	// with the visitor's cookie.
	shared := "/v1/u/" + s.oh + "/sites/shop/data/prefs"
	for _, host := range []string{pcContentHost, s.dom} {
		r := s.a.at(t, "GET", host, shared, nil, browser(host, s.vicCooky))
		if host == pcContentHost && r.status != 401 {
			t.Fatalf("content host read: %d %s", r.status, r.body)
		}
		if host == s.dom && (r.status != 200 || recordOf(t, r)["theme"] != "dark") {
			t.Fatalf("handle path on the site's address: %d %s", r.status, r.body)
		}
	}
	wantCode(t, "content host write", s.a.at(t, "PUT", pcContentHost, shared, map[string]any{"x": 1}, browser(pcContentHost, s.vicCooky)), 401, "")
	wantCode(t, "cross-origin write", s.a.at(t, "PUT", s.dom, "/v1/sites/shop/data/prefs", map[string]any{"x": 1}, browser(s.dom, s.vicCooky, "Sec-Fetch-Site", "cross-site")), 401, "")
	// The connector: counts in list_data, and read_collection is refused.
	clientID := s.a.registerClient(t, testRedirect)
	tok := s.a.connect(t, s.olive, clientID, testRedirect)["access_token"].(string)
	call := func(name string, args map[string]any) (string, bool) {
		text, _, isErr := toolResultOf(t, s.a.rpc(t, tok, "tools/call", map[string]any{"name": name, "arguments": args}))
		return text, isErr
	}
	if text, isErr := call("read_collection", map[string]any{"site": "shop", "collection": "prefs"}); !isErr || strings.Contains(text, "dark") {
		t.Fatalf("read_collection on a personal name: %v %s", isErr, text)
	}
	if text, isErr := call("list_data", map[string]any{"site": "shop"}); isErr || !strings.Contains(text, `"Personal"`) || strings.Contains(text, "dark") {
		t.Fatalf("list_data: %s", text)
	}
	if text, isErr := call("declare_data", map[string]any{"site": "shop", "name": "board1", "kind": "board"}); isErr || !strings.Contains(text, "Shared board") {
		t.Fatalf("declare_data board: %s", text)
	}
	if text, isErr := call("data_history", map[string]any{"site": "shop", "collection": "prefs"}); !isErr {
		t.Fatalf("data_history on a personal name: %s", text)
	}
}

// Review fixes (2026-09-27): Personal names are stored private (H2), a kind
// change re-checks under the name's lock (M1), the people cap (M4), counts
// only from 3 people (L1), and a person's delete after an owner clear sticks
// (L3).
func TestPersonalReviewFixes(t *testing.T) {
	s := newKindsSite(t, false)
	const p = "/v1/sites/shop/data/habits"
	s.declare(t, "habits", map[string]any{"kind": "mine"})
	var private bool
	if err := s.a.database.QueryRow(`SELECT private FROM collection_settings WHERE site_id = $1 AND collection = 'habits'`, s.shopID).Scan(&private); err != nil || !private {
		t.Fatalf("a Personal name is stored private: %v %v", private, err)
	}
	// People cap: a new person past it is refused; people who have a record
	// keep saving.
	s.setLimits(t, func(c *config.SavedData) { c.PersonalPeopleMax = 2 })
	if r := s.visitor(t, "PUT", p, map[string]any{"n": 1}); r.status != 200 {
		t.Fatalf("vic: %d %s", r.status, r.body)
	}
	if r := s.as(t, s.wesCooky, "PUT", p, map[string]any{"n": 2}); r.status != 200 {
		t.Fatalf("wes: %d %s", r.status, r.body)
	}
	pam := s.a.newPerson(t, "pam")
	pamCooky := s.a.session(t, pam, s.shopID, s.dom)
	wantCode(t, "third person", s.as(t, pamCooky, "PUT", p, map[string]any{"n": 3}), 409, "people_full")
	if r := s.visitor(t, "PATCH", p, map[string]any{"ops": []any{map[string]any{"op": "inc", "path": "n"}}}); r.status != 200 {
		t.Fatalf("vic again: %d %s", r.status, r.body)
	}
	summary := func() map[string]any {
		for _, n := range s.owner(t, "GET", "/v1/sites/shop/data", nil).json(t)["names"].([]any) {
			if m := n.(map[string]any); m["name"] == "habits" {
				return m
			}
		}
		t.Fatal("no habits in list_data")
		return nil
	}
	if m := summary(); m["few"] != true || m["count"] != float64(0) || m["bytes"] != float64(0) {
		t.Fatalf("two people: %v", m)
	}
	s.setLimits(t, func(*config.SavedData) {})
	if r := s.as(t, pamCooky, "PUT", p, map[string]any{"n": 3}); r.status != 200 {
		t.Fatalf("pam: %d %s", r.status, r.body)
	}
	if m := summary(); m["few"] != nil || m["count"] != float64(3) || m["bytes"].(float64) <= 0 {
		t.Fatalf("three people: %v", m)
	}
	if r := s.owner(t, "GET", p+"?count=1", nil); r.json(t)["count"] != float64(3) || r.json(t)["few"] != nil {
		t.Fatalf("count, three people: %s", r.body)
	}
	// The kind change re-checks under the lock: records never change hands.
	ctx := context.Background()
	if err := db.DeclareDataLocked(ctx, s.a.database, s.shopID, "habits", db.KindEntries, true, false, db.NotifyOff, false); err != db.ErrNameHasRows {
		t.Fatalf("Personal with records to entries: %v", err)
	}
	s.declare(t, "notes", map[string]any{"kind": "entries", "visibility": "public"})
	if r := s.visitor(t, "POST", "/v1/sites/shop/data/notes", map[string]any{"n": 1}); r.status != 201 {
		t.Fatalf("note: %d %s", r.status, r.body)
	}
	if err := db.DeclareDataLocked(ctx, s.a.database, s.shopID, "notes", db.KindPersonal, false, false, db.NotifyOff, false); err != db.ErrNameHasRows {
		t.Fatalf("entries with items to Personal: %v", err)
	}
	if err := db.DeclareDataLocked(ctx, s.a.database, s.shopID, "fresh", db.KindPersonal, false, false, db.NotifyOff, false); err != nil {
		t.Fatalf("an unused name to Personal: %v", err)
	}
	// The owner clears; vic then deletes theirs; the owner's Restore brings
	// back the others but not vic's.
	if r := s.owner(t, "DELETE", "/v1/sites/shop/collections/habits", map[string]any{"confirm": "habits"}); r.status != 200 {
		t.Fatalf("clear: %d %s", r.status, r.body)
	}
	if m := summary(); m["deleted"] != float64(3) || m["deleted_few"] != nil {
		t.Fatalf("after clear: %v", m)
	}
	if r := s.visitor(t, "DELETE", p, nil); r.status != 200 {
		t.Fatalf("vic deletes a cleared record: %d %s", r.status, r.body)
	}
	wantCode(t, "delete it twice", s.visitor(t, "DELETE", p, nil), 404, "not_found")
	if r := s.owner(t, "POST", "/v1/sites/shop/collections/habits/deleted/restore", map[string]any{"all": true}); r.status != 200 || r.json(t)["restored"] != float64(2) {
		t.Fatalf("restore: %d %s", r.status, r.body)
	}
	if r := s.visitor(t, "GET", p, nil); r.json(t)["data"] != nil {
		t.Fatalf("vic's deleted record came back: %s", r.body)
	}
	if m := summary(); m["deleted"] != float64(0) || m["deleted_few"] != true || m["few"] != true {
		t.Fatalf("one deleted, two live: %v", m)
	}
}

// Review fixes (2026-09-27): board adds drop the server's stamp keys (L4),
// a per-person write rate on top of the per-address one (M2), and the owner's
// restores keep a window and the board's cap (M2, L5).
func TestBoardReviewFixes(t *testing.T) {
	s := newKindsSite(t, false)
	const p = "/v1/sites/shop/data/todo"
	s.declare(t, "todo", map[string]any{"kind": "board"})
	r := s.visitor(t, "POST", p, map[string]any{"text": "a", "_submitted_by": "ceo@corp.com", "_submitted_at": "x"})
	if r.status != 201 {
		t.Fatalf("add: %d %s", r.status, r.body)
	}
	a := idOf(t, r)
	for _, it := range itemsOf(t, s.owner(t, "GET", "/v1/sites/shop/collections/todo", nil)) {
		d := it["data"].(map[string]any)
		if _, ok := d["_submitted_by"]; ok || d["_submitted_at"] != nil || d["text"] != "a" {
			t.Fatalf("stamp keys kept: %v", it)
		}
	}
	// One person, however many addresses: SAVED_DATA_BOARD_WRITES_PER_MIN.
	s.setLimits(t, func(c *config.SavedData) { c.AppendPerMin, c.AppendBurst, c.BoardWritesPerMin = 1000, 1000, 3 })
	for i := 0; i < 3; i++ {
		if r := s.visitor(t, "POST", p, map[string]any{"text": "v" + strconv.Itoa(i)}); r.status != 201 {
			t.Fatalf("vic add %d: %d %s", i, r.status, r.body)
		}
	}
	wantCode(t, "vic past the per-person rate", s.visitor(t, "POST", p, map[string]any{"text": "v4"}), 429, "rate_limited")
	b := idOf(t, s.as(t, s.wesCooky, "POST", p, map[string]any{"text": "w"}))
	s.setLimits(t, func(*config.SavedData) {})
	// Restore all names a window: a delete from before it stays deleted.
	for _, id := range []string{a, b} {
		if r := s.owner(t, "DELETE", "/v1/sites/shop/collections/todo/items/"+id, nil); r.status != 204 {
			t.Fatalf("owner delete %s: %d %s", id, r.status, r.body)
		}
	}
	if _, err := s.a.database.Exec(`UPDATE collection_items SET deleted_at = now() - interval '2 hours' WHERE id = $1`, a); err != nil {
		t.Fatal(err)
	}
	wantCode(t, "bad window", s.owner(t, "POST", "/v1/sites/shop/collections/todo/deleted/restore", map[string]any{"all": true, "within_minutes": 999999}), 400, "invalid_window")
	if r := s.owner(t, "POST", "/v1/sites/shop/collections/todo/deleted/restore", map[string]any{"all": true, "within_minutes": 60}); r.status != 200 || r.json(t)["restored"] != float64(1) {
		t.Fatalf("restore the last hour: %d %s", r.status, r.body)
	}
	if n := len(itemsOf(t, s.owner(t, "GET", "/v1/sites/shop/collections/todo", nil))); n != 4 {
		t.Fatalf("live after restore: %d", n)
	}
	// The cap holds for every owner restore.
	s.setLimits(t, func(c *config.SavedData) { c.BoardMax = 4 })
	wantCode(t, "restore one past the cap", s.owner(t, "POST", "/v1/sites/shop/collections/todo/items/"+a+"/restore", nil), 409, "list_full")
	wantCode(t, "restore all past the cap", s.owner(t, "POST", "/v1/sites/shop/collections/todo/deleted/restore", map[string]any{"all": true, "within_minutes": 43200}), 409, "list_full")
	var hid string
	for _, e := range historyOf(t, s.owner(t, "GET", "/v1/sites/shop/collections/todo/history", nil)) {
		if e["op"] == "delete" && strconv.FormatInt(int64(e["item_id"].(float64)), 10) == a {
			hid = strconv.FormatInt(int64(e["id"].(float64)), 10)
		}
	}
	if hid == "" {
		t.Fatal("no delete of a in the history")
	}
	wantCode(t, "history restore past the cap", s.owner(t, "POST", "/v1/sites/shop/collections/todo/history/"+hid+"/restore", nil), 409, "list_full")
	s.setLimits(t, func(c *config.SavedData) { c.BoardMax = 5 })
	if r := s.owner(t, "POST", "/v1/sites/shop/collections/todo/history/"+hid+"/restore", nil); r.status != 200 {
		t.Fatalf("history restore with room: %d %s", r.status, r.body)
	}
}
