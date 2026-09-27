package handler

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	db "github.com/vsriram/simple-host/internal/db"
)

// v0.7.2 correctness fixes (final/hosted-correctness.md). Needs DB_DSN.

// M1: a private list never becomes public through its Recently deleted: the
// kind and privacy changes count what a restore would bring back, and run
// under the declaration lock.
func TestPrivateToPublicCountsRecentlyDeleted(t *testing.T) {
	s := newKindsSite(t, false)
	s.declare(t, "orders", map[string]any{"kind": "entries"})
	base := "/v1/sites/shop/data/orders"
	if r := s.as(t, s.vicCooky, "POST", base, map[string]any{"name": "Vic", "address": "1 Main St"}); r.status != 201 {
		t.Fatalf("order: %d %s", r.status, r.body)
	}
	if r := s.owner(t, "DELETE", "/v1/sites/shop/collections/orders", map[string]string{"confirm": "orders"}); r.status != 200 {
		t.Fatalf("clear: %d %s", r.status, r.body)
	}
	kind := "/v1/sites/shop/data/orders/kind"
	r := s.owner(t, "PUT", kind, map[string]any{"kind": "content"})
	wantCode(t, "cleared private list to page info", r, 409, "has_entries")
	if !strings.Contains(string(r.body), "Recently deleted") {
		t.Fatalf("says where the entries are: %s", r.body)
	}
	wantCode(t, "cleared private list to page info, confirmed", s.owner(t, "PUT", kind, map[string]any{"kind": "content", "confirm_public": true}), 409, "has_entries")
	wantCode(t, "cleared private list to board", s.owner(t, "PUT", kind, map[string]any{"kind": "board"}), 409, "confirm_public")
	wantCode(t, "cleared private list to public", s.owner(t, "PUT", kind, map[string]any{"kind": "entries", "visibility": "public"}), 409, "confirm_public")
	wantCode(t, "cleared private list, privacy off", s.owner(t, "PUT", "/v1/sites/shop/collections/orders/privacy", map[string]bool{"private": false}), 409, "confirm_public")

	// Under the lock, not only in the handler.
	var pe *db.PrivateItemsError
	err := db.DeclareDataLocked(context.Background(), s.a.database, s.shopID, "orders", db.KindContent, false, false, db.NotifyOff, true)
	if !errors.As(err, &pe) || pe.Live != 0 || pe.Deleted != 1 || !pe.Content {
		t.Fatalf("locked check: %v %+v", err, pe)
	}

	// Emptied for good, the name may become page info; nothing comes back.
	if r := s.owner(t, "DELETE", "/v1/sites/shop/collections/orders/deleted", map[string]string{"confirm": "orders"}); r.status != 200 {
		t.Fatalf("empty Recently deleted: %d %s", r.status, r.body)
	}
	s.declare(t, "orders", map[string]any{"kind": "content"})
	if r := s.owner(t, "POST", "/v1/sites/shop/collections/orders/deleted/restore", map[string]bool{"all": true}); r.status != 200 || r.json(t)["restored"] != float64(0) {
		t.Fatalf("restore after: %d %s", r.status, r.body)
	}
	if items := itemsOf(t, s.a.at(t, "GET", s.dom, "/v1/sites/shop/collections/orders", nil, nil)); len(items) != 0 {
		t.Fatalf("public read shows old orders: %v", items)
	}
}

// M1: a save that was checked against the old kind never lands under the new
// one (the append re-checks the kind under the declaration lock).
func TestAppendRechecksKindUnderLock(t *testing.T) {
	s := newKindsSite(t, true)
	ctx := context.Background()
	s.declare(t, "menu", map[string]any{"kind": "content"})
	if _, _, err := db.AppendEntry(ctx, s.a.database, s.shopID, "menu", db.KindEntries, []byte(`{"a":1}`), db.Actor{}, false, 100); !errors.Is(err, db.ErrKindChanged) {
		t.Fatalf("entry into page info: %v", err)
	}
	if _, err := db.AppendCollectionItemByID(ctx, s.a.database, s.shopID, "menu", []byte(`{"a":1}`), db.Actor{}); !errors.Is(err, db.ErrKindChanged) {
		t.Fatalf("legacy append into page info: %v", err)
	}
	// An undeclared name still takes legacy appends.
	if _, err := db.AppendCollectionItemByID(ctx, s.a.database, s.shopID, "guestbook", []byte(`{"a":1}`), db.Actor{}); err != nil {
		t.Fatalf("legacy append: %v", err)
	}
}

// M1: page info is one document; a restore never makes a second live one.
func TestPageInfoRestoreKeepsOneDocument(t *testing.T) {
	s := newKindsSite(t, false)
	s.declare(t, "menu", map[string]any{"kind": "content"})
	data := "/v1/sites/shop/data/menu"
	coll := "/v1/sites/shop/collections/menu"
	if r := s.owner(t, "PUT", data, map[string]any{"v": 1}); r.status != 200 {
		t.Fatalf("put: %d %s", r.status, r.body)
	}
	if r := s.owner(t, "DELETE", coll, map[string]string{"confirm": "menu"}); r.status != 200 {
		t.Fatalf("clear: %d %s", r.status, r.body)
	}
	if r := s.owner(t, "PUT", data, map[string]any{"v": 2}); r.status != 200 {
		t.Fatalf("put 2: %d %s", r.status, r.body)
	}
	wantCode(t, "restore all beside a document", s.owner(t, "POST", coll+"/deleted/restore", map[string]bool{"all": true}), 409, "one_document")
	var oldID int64
	if err := s.a.database.QueryRow(`SELECT id FROM collection_items WHERE site_id = $1 AND collection = 'menu' AND deleted_at IS NOT NULL`, s.shopID).Scan(&oldID); err != nil {
		t.Fatal(err)
	}
	wantCode(t, "restore one beside a document", s.owner(t, "POST", coll+"/items/"+strconv.FormatInt(oldID, 10)+"/restore", nil), 409, "one_document")
	var clearID int64
	if err := s.a.database.QueryRow(`SELECT id FROM data_history WHERE site_id = $1 AND name = 'menu' AND op = 'clear'`, s.shopID).Scan(&clearID); err != nil {
		t.Fatal(err)
	}
	wantCode(t, "undo the clear beside a document", s.owner(t, "POST", coll+"/history/"+strconv.FormatInt(clearID, 10)+"/restore", nil), 409, "one_document")
	var live int
	_ = s.a.database.QueryRow(`SELECT count(*) FROM collection_items WHERE site_id = $1 AND collection = 'menu' AND deleted_at IS NULL`, s.shopID).Scan(&live)
	if live != 1 {
		t.Fatalf("live page info rows: %d", live)
	}
	// With the current one cleared, two old documents come back one at a time.
	if r := s.owner(t, "DELETE", coll, map[string]string{"confirm": "menu"}); r.status != 200 {
		t.Fatalf("clear 2: %d %s", r.status, r.body)
	}
	wantCode(t, "restore two at once", s.owner(t, "POST", coll+"/deleted/restore", map[string]bool{"all": true}), 409, "one_document")
	if r := s.owner(t, "POST", coll+"/items/"+strconv.FormatInt(oldID, 10)+"/restore", nil); r.status != 200 {
		t.Fatalf("restore one: %d %s", r.status, r.body)
	}
	got := s.a.at(t, "GET", s.dom, data, nil, nil).json(t)
	if d, _ := got["data"].(map[string]any); d["v"] != float64(1) {
		t.Fatalf("restored document: %v", got)
	}
}

// L1: ids and cursors past 2^31 are ordinary numbers, not a 500.
func TestBigIDsAndCursors(t *testing.T) {
	s := newKindsSite(t, true)
	ctx := context.Background()
	const big = int64(3000000000)
	d := s.a.database
	if _, err := db.ListCollectionItemsByID(ctx, d, s.shopID, "guestbook", 10, big, false); err != nil {
		t.Fatalf("list before: %v", err)
	}
	if _, err := db.ListHistory(ctx, d, s.shopID, db.HistoryList, "guestbook", 10, big); err != nil {
		t.Fatalf("history before: %v", err)
	}
	if n, err := db.UndeleteItems(ctx, d, s.shopID, "guestbook", big, db.Actor{}, 0, false, 0, 0); err != nil || n != 0 {
		t.Fatalf("undelete: %d %v", n, err)
	}
	if _, err := db.ListDeletedItems(ctx, d, s.shopID, "guestbook", 10, db.DeletedCursor{At: time.Now(), ID: big}); err != nil {
		t.Fatalf("deleted cursor: %v", err)
	}
	if n, err := db.PurgeDeletedItems(ctx, d, s.shopID, "guestbook", big); err != nil || n != 0 {
		t.Fatalf("purge: %d %v", n, err)
	}
	vicID, _ := s.a.userID(t, s.vic)
	if _, err := db.ListOwnEntries(ctx, d, s.shopID, "guestbook", vicID, 10, big); err != nil {
		t.Fatalf("own entries before: %v", err)
	}
	if _, err := db.ListItemHistory(ctx, d, s.shopID, "guestbook", big, 10, big); err != nil {
		t.Fatalf("item history before: %v", err)
	}
	// Over HTTP: the public list, and a restore or purge of an id nobody has.
	if r := s.a.at(t, "GET", s.dom, "/v1/sites/shop/collections/guestbook?before=3000000000", nil, nil); r.status != 200 {
		t.Fatalf("public list before: %d %s", r.status, r.body)
	}
	wantCode(t, "restore big id", s.owner(t, "POST", "/v1/sites/shop/collections/guestbook/items/3000000000/restore", nil), 404, "not_found")
	wantCode(t, "purge big id", s.owner(t, "DELETE", "/v1/sites/shop/collections/guestbook/deleted/3000000000", nil), 404, "")
}

func TestJSONStorable(t *testing.T) {
	for body, want := range map[string]bool{
		`{"a":"x"}`:                 true,
		`{"a":"x\u0000y"}`:          false,
		`{"a\u0000":1}`:             false,
		`{"a":"\\u0000"}`:           true, // an escaped backslash, then text
		`{"a":"\\\u0000"}`:          false,
		`{"a":"\ud83d\ude00"}`:      true,
		`{"a":"\ud800"}`:            false,
		`{"a":"\ud800x"}`:           false,
		`{"a":"\udc00"}`:            false,
		`{"a":"\u00e9 \u20ac"}`:     true,
		"{\"a\":\"\xff\"}":          false,
		`["\ud800\ud800\udc00"]`:    false,
		`{"a":"\"\\","b":"\u0001"}`: true,
	} {
		if got := jsonStorable([]byte(body)); got != want {
			t.Errorf("%s: %v, want %v", body, got, want)
		}
	}
}

// L2: text Postgres cannot store is refused with 400 invalid_json on every
// saved-data write, never a 500.
func TestSavedDataRefusesUnstorableText(t *testing.T) {
	s := newKindsSite(t, true)
	nul := `{"msg":"a\u0000b"}`
	bad := "{\"msg\":\"\xff\"}"
	half := `{"msg":"\ud800"}`
	check := func(what string, r resp) {
		t.Helper()
		wantCode(t, what, r, 400, "invalid_json")
	}
	for _, body := range []string{nul, bad, half} {
		check("state put", s.visitor(t, "PUT", "/v1/sites/shop/state", body))
		check("list add", s.visitor(t, "POST", "/v1/sites/shop/collections/guestbook", body))
	}
	check("state patch", s.visitor(t, "PATCH", "/v1/sites/shop/state", `{"ops":[{"op":"set","path":"a","value":"x\u0000"}]}`))
	check("state patch key", s.visitor(t, "PATCH", "/v1/sites/shop/state", `{"ops":[{"op":"set","path":"a\u0000","value":1}]}`))

	s.declare(t, "menu", map[string]any{"kind": "content"})
	check("page info", s.owner(t, "PUT", "/v1/sites/shop/data/menu", nul))

	s.declare(t, "rsvps", map[string]any{"kind": "entries"})
	check("private entry", s.as(t, s.vicCooky, "POST", "/v1/sites/shop/data/rsvps", nul))
	id := idOf(t, s.as(t, s.vicCooky, "POST", "/v1/sites/shop/data/rsvps", map[string]any{"name": "Vic"}))
	check("entry edit", s.as(t, s.vicCooky, "PATCH", "/v1/sites/shop/data/rsvps/items/"+id, nul))
	check("owner edit", s.ownerWrite(t, "PATCH", "/v1/sites/shop/collections/rsvps/items/"+id, nul))

	s.declare(t, "todo", map[string]any{"kind": "board"})
	check("board add", s.as(t, s.vicCooky, "POST", "/v1/sites/shop/data/todo", nul))
	bid := idOf(t, s.as(t, s.vicCooky, "POST", "/v1/sites/shop/data/todo", map[string]any{"text": "milk"}))
	check("board edit", s.as(t, s.vicCooky, "PATCH", "/v1/sites/shop/data/todo/items/"+bid, nul))

	s.declare(t, "prefs", map[string]any{"kind": "mine"})
	check("personal put", s.as(t, s.vicCooky, "PUT", "/v1/sites/shop/data/prefs", nul))
	check("personal patch", s.as(t, s.vicCooky, "PATCH", "/v1/sites/shop/data/prefs", `{"ops":[{"op":"set","path":"a","value":"\u0000"}]}`))

	// Ordinary text, escapes included, still saves.
	if r := s.visitor(t, "POST", "/v1/sites/shop/collections/guestbook", `{"msg":"caf\u00e9 \ud83d\ude00 \\u0000"}`); r.status != 201 {
		t.Fatalf("ordinary text: %d %s", r.status, r.body)
	}
}

// L3: an entry that committed just after a digest read (its created_at is
// before that digest's time) is counted by the next one.
func TestDigestCountsByID(t *testing.T) {
	s := newKindsSite(t, true)
	ctx := context.Background()
	s.declare(t, "orders", map[string]any{"kind": "entries", "notify": "each"})
	idOf(t, s.as(t, s.vicCooky, "POST", "/v1/sites/shop/data/orders", map[string]any{"item": "tea"}))
	back := func(q string) {
		t.Helper()
		if _, err := s.a.database.Exec(q, s.shopID); err != nil {
			t.Fatal(err)
		}
	}
	back(`UPDATE collection_settings SET declared_at = now() - interval '3 hours', updated_at = now() - interval '3 hours' WHERE site_id = $1`)
	back(`UPDATE collection_items SET created_at = now() - interval '2 hours' WHERE site_id = $1`)
	dueHere := func() []db.NotifyDue {
		t.Helper()
		due, err := db.DueNotifications(ctx, s.a.database, 10*time.Minute, 24*time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		var mine []db.NotifyDue
		for _, d := range due {
			if d.SiteID == s.shopID {
				mine = append(mine, d)
			}
		}
		return mine
	}
	first := dueHere()
	if len(first) != 1 || first[0].Count != 1 {
		t.Fatalf("first digest: %+v", first)
	}
	if ok, err := db.ClaimNotification(ctx, s.a.database, first[0]); !ok || err != nil {
		t.Fatalf("claim: %v %v", ok, err)
	}
	// The late entry: its transaction began before that digest ran.
	late := idOf(t, s.as(t, s.wesCooky, "POST", "/v1/sites/shop/data/orders", map[string]any{"item": "cake"}))
	back(`UPDATE collection_settings SET notify_sent_at = notify_sent_at - interval '1 hour' WHERE site_id = $1`)
	if _, err := s.a.database.Exec(`UPDATE collection_items SET created_at = (SELECT notify_sent_at - interval '1 minute' FROM collection_settings WHERE site_id = $1 AND collection = 'orders') WHERE id = $2`, s.shopID, late); err != nil {
		t.Fatal(err)
	}
	next := dueHere()
	if len(next) != 1 || next[0].Count != 1 || strconv.FormatInt(next[0].LastID, 10) != late {
		t.Fatalf("next digest misses the late entry: %+v", next)
	}
	if ok, err := db.ClaimNotification(ctx, s.a.database, next[0]); !ok || err != nil {
		t.Fatalf("claim 2: %v %v", ok, err)
	}
	back(`UPDATE collection_settings SET notify_sent_at = notify_sent_at - interval '1 hour' WHERE site_id = $1`)
	if again := dueHere(); len(again) != 0 {
		t.Fatalf("counted twice: %+v", again)
	}
}
