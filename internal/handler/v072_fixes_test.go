package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/internal/auth"
	"github.com/vsriram/simple-host/internal/config"
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

// L4: erasing an account while a visitor's save on its site is under way
// never deadlocks: the erase takes the save's locks in the save's order.
func TestEraseAccountWaitsForVisitorSave(t *testing.T) {
	s := newKindsSite(t, true)
	ctx := context.Background()
	s.declare(t, "menu", map[string]any{"kind": "content"})
	if r := s.owner(t, "PUT", "/v1/sites/shop/data/menu", map[string]any{"soup": 1}); r.status != 200 {
		t.Fatalf("owner page info: %d %s", r.status, r.body)
	}
	s.declare(t, "todo", map[string]any{"kind": "board"})
	item := idOf(t, s.as(t, s.vicCooky, "POST", "/v1/sites/shop/data/todo", map[string]any{"text": "milk"}))

	// The visitor's save: the declaration row, then its item, as SavePersonal
	// and the board do; its size trigger then needs the site row.
	v, err := s.a.database.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Rollback()
	var one int
	if err := v.QueryRow(`SELECT 1 FROM collection_settings WHERE site_id = $1 AND collection = 'todo' FOR UPDATE`, s.shopID).Scan(&one); err != nil {
		t.Fatal(err)
	}
	if err := v.QueryRow(`SELECT 1 FROM collection_items WHERE id = $1 FOR UPDATE`, item).Scan(&one); err != nil {
		t.Fatal(err)
	}

	oliveID, _ := s.a.userID(t, s.olive)
	erased := make(chan error, 1)
	go func() {
		tx, err := s.a.database.BeginTx(ctx, nil)
		if err != nil {
			erased <- err
			return
		}
		defer tx.Rollback()
		acct, err := db.LockAccountForDelete(ctx, tx, oliveID)
		if err == nil {
			_, err = db.EraseAccount(ctx, tx, acct)
		}
		if err == nil {
			err = tx.Commit()
		}
		erased <- err
	}()
	time.Sleep(500 * time.Millisecond) // the erase is now waiting on the save
	if _, err := v.Exec(`UPDATE collection_items SET data = '{"text":"oat milk"}'::jsonb WHERE id = $1`, item); err != nil {
		t.Fatalf("the visitor's save: %v", err)
	}
	if err := v.Commit(); err != nil {
		t.Fatalf("the visitor's commit: %v", err)
	}
	select {
	case err := <-erased:
		if err != nil {
			t.Fatalf("erase: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("erase did not finish")
	}
	var n int
	_ = s.a.database.QueryRow(`SELECT count(*) FROM sites WHERE id = $1`, s.shopID).Scan(&n)
	if n != 0 {
		t.Fatal("site still there after the erase")
	}
}

// L5: a rollback whose commit fails leaves the served files as they were.
func TestRollbackServesOnlyWhatCommitted(t *testing.T) {
	a := newPrivateApp(t)
	ann := a.newPerson(t, "ann")
	key := map[string]string{"X-API-Key": ann.key}
	a.deploy(t, ann, "blog") // v1: <h1>blog</h1>
	if r := a.at(t, "PUT", "simple-host.test", "/v1/sites/blog/files", map[string]any{"files": map[string]string{"index.html": "v2"}}, key); r.status != 200 && r.status != 201 {
		t.Fatalf("v2: %d %s", r.status, r.body)
	}
	uid, _ := a.userID(t, ann)
	siteID := a.siteID(t, ann, "blog")
	served := func() string {
		t.Helper()
		b, err := os.ReadFile(a.sites.disk.SiteDir(uid, "blog") + "/current/index.html")
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	if served() != "v2" {
		t.Fatalf("before: %q", served())
	}
	// The commit fails: a deferred check on this site's row.
	for _, q := range []string{
		`CREATE OR REPLACE FUNCTION v072_fail_commit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'commit refused'; END $$`,
		`DROP TRIGGER IF EXISTS v072_fail_commit ON sites`,
		`CREATE CONSTRAINT TRIGGER v072_fail_commit AFTER UPDATE ON sites DEFERRABLE INITIALLY DEFERRED FOR EACH ROW WHEN (NEW.id = '` + siteID + `') EXECUTE FUNCTION v072_fail_commit()`,
	} {
		if _, err := a.database.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	dropped := false
	drop := func() {
		if !dropped {
			dropped = true
			_, _ = a.database.Exec(`DROP TRIGGER IF EXISTS v072_fail_commit ON sites`)
			_, _ = a.database.Exec(`DROP FUNCTION IF EXISTS v072_fail_commit()`)
		}
	}
	t.Cleanup(drop)
	if r := a.at(t, "PUT", "simple-host.test", "/v1/sites/blog/active-version", map[string]int{"version_number": 1}, key); r.status != 500 {
		t.Fatalf("rollback with a failing commit: %d %s", r.status, r.body)
	}
	var active int
	_ = a.database.QueryRow(`SELECT active_version FROM sites WHERE id = $1`, siteID).Scan(&active)
	if active != 2 || served() != "v2" {
		t.Fatalf("after a failed commit: active_version %d, serving %q", active, served())
	}
	drop()
	if r := a.at(t, "PUT", "simple-host.test", "/v1/sites/blog/active-version", map[string]int{"version_number": 1}, key); r.status != 200 {
		t.Fatalf("rollback: %d %s", r.status, r.body)
	}
	if !strings.Contains(served(), "blog") {
		t.Fatalf("after rollback: %q", served())
	}
}

// L6: when the per-day copies thinning keeps are over the cap by themselves,
// writes stop re-running the whole-history thin until it has grown again.
func TestThinSkipsWhenNothingCanGo(t *testing.T) {
	s := newKindsSite(t, true)
	a := s.a
	ctx := context.Background()
	s.setLimits(t, func(c *config.SavedData) { c.HistoryMaxMB = 1 })
	a.sites.thinLimiter = nil
	capBytes := int64(1) << 20
	blob := func(n int) string { return `{"x":"` + strings.Repeat("a", n) + `"}` }
	var items []int64
	for i := 0; i < 12; i++ { // 12 keepers of ~100 KB: over the cap on their own
		var id int64
		if err := a.database.QueryRow(`INSERT INTO collection_items (site_id, collection, data) VALUES ($1, 'big', '{}') RETURNING id`, s.shopID).Scan(&id); err != nil {
			t.Fatal(err)
		}
		items = append(items, id)
		if _, err := a.database.Exec(`INSERT INTO data_history (site_id, kind, name, item_id, op, prev, actor_kind) VALUES ($1, 'list', 'big', $2, 'edit', $3::jsonb, 'owner')`, s.shopID, id, blob(100<<10)); err != nil {
			t.Fatal(err)
		}
	}
	rows := func() (n int) {
		t.Helper()
		_ = a.database.QueryRow(`SELECT count(*) FROM data_history WHERE site_id = $1`, s.shopID).Scan(&n)
		return
	}
	req := httptest.NewRequest("GET", "/", nil)
	a.sites.boundHistory(req, s.shopID)
	if rows() != 12 {
		t.Fatalf("keepers deleted: %d", rows())
	}
	size, err := db.HistoryBytes(ctx, a.database, s.shopID)
	if err != nil || size <= capBytes {
		t.Fatalf("history size %d %v", size, err)
	}
	if !a.sites.thinPointless(s.shopID, size, capBytes) {
		t.Fatal("a thin that left the history over the cap is not remembered")
	}
	// A small later change of the same item the same day: not a keeper, but
	// the history has not grown by the margin, so no thin runs.
	add := func(n int) {
		t.Helper()
		if _, err := a.database.Exec(`INSERT INTO data_history (site_id, kind, name, item_id, op, prev, actor_kind) VALUES ($1, 'list', 'big', $2, 'edit', $3::jsonb, 'owner')`, s.shopID, items[0], blob(n)); err != nil {
			t.Fatal(err)
		}
	}
	add(1 << 10)
	a.sites.boundHistory(req, s.shopID)
	if rows() != 13 {
		t.Fatalf("thinned before the margin: %d rows", rows())
	}
	// Grown by the margin: the thin runs again and takes what it can.
	add(int(thinMargin(capBytes)))
	a.sites.boundHistory(req, s.shopID)
	if rows() != 12 {
		t.Fatalf("after the margin: %d rows", rows())
	}
	// The sweep forgets sites back under the cap.
	if _, err := a.database.Exec(`DELETE FROM data_history WHERE site_id = $1`, s.shopID); err != nil {
		t.Fatal(err)
	}
	a.sites.sweepSavedData(ctx)
	if _, ok := a.sites.thinStuck.Load(s.shopID); ok {
		t.Fatal("still remembered once under the cap")
	}
}

// Small-box trial B4: the owner's agent saves with its key and no page
// Origin, as llms.txt says, on the state and list routes too; anything else
// without one of the site's origins is 403 origin_not_allowed.
func TestOwnerKeyWritesWithoutOrigin(t *testing.T) {
	s := newKindsSite(t, true)
	a := s.a
	if r := s.owner(t, "PUT", "/v1/sites/shop/state", map[string]any{"n": 1}); r.status != 200 {
		t.Fatalf("owner key state put: %d %s", r.status, r.body)
	}
	if r := s.owner(t, "PATCH", "/v1/sites/shop/state", `{"ops":[{"op":"inc","path":"n"}]}`); r.status != 200 {
		t.Fatalf("owner key state patch: %d %s", r.status, r.body)
	}
	if r := s.owner(t, "POST", "/v1/sites/shop/collections/guestbook", map[string]any{"msg": "hi"}); r.status != 201 {
		t.Fatalf("owner key list add: %d %s", r.status, r.body)
	}
	var who string
	_ = a.database.QueryRow(`SELECT actor_kind FROM data_history WHERE site_id = $1 AND kind = 'state' ORDER BY id DESC LIMIT 1`, s.shopID).Scan(&who)
	if who != actorOwner {
		t.Fatalf("recorded as %q", who)
	}
	stranger := a.newPerson(t, "stranger")
	wantCode(t, "another account's key", a.at(t, "PUT", pcSiteDomain, "/v1/sites/shop/state", map[string]any{"x": 1}, map[string]string{"X-API-Key": stranger.key}), 404, "")
	wantCode(t, "a bad key", a.at(t, "PUT", pcSiteDomain, "/v1/sites/shop/state", map[string]any{"x": 1}, map[string]string{"X-API-Key": "sh_nope"}), 401, "invalid_api_key")
	wantCode(t, "no key, no page", a.at(t, "PUT", pcSiteDomain, "/v1/sites/shop/state", map[string]any{"x": 1}, nil), 403, "origin_not_allowed")
	wantCode(t, "a foreign page with the key", a.at(t, "PUT", pcSiteDomain, "/v1/sites/shop/state", map[string]any{"x": 1}, map[string]string{"X-API-Key": s.olive.key, "Origin": "https://evil.example"}), 403, "origin_not_allowed")
	wantCode(t, "a foreign page, list", a.at(t, "POST", pcSiteDomain, "/v1/sites/shop/collections/guestbook", map[string]any{"x": 1}, map[string]string{"Origin": "https://evil.example"}), 403, "origin_not_allowed")
}

// Small-box trial B6: a response has the same shape whether or not the
// caller sends X-Skill-Version: arrays stay bare (the notice is a header),
// and an object keeps its fields byte for byte, big numbers included.
func TestNoticeKeepsResponseShape(t *testing.T) {
	mw := NoticeMiddleware("1.2.3")
	serve := func(body string, hdr string) *httptest.ResponseRecorder {
		h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(body))
		}))
		req := httptest.NewRequest("GET", "/v1/sites", nil)
		if hdr != "" {
			req.Header.Set("X-Skill-Version", hdr)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	arr := `[{"id":1},{"id":2}]`
	for _, v := range []string{"", "0.1.0", "1.2.3"} {
		rec := serve(arr, v)
		if rec.Body.String() != arr {
			t.Fatalf("skill %q: array became %s", v, rec.Body.String())
		}
		if stale := v != "1.2.3"; (rec.Header().Get("X-Skill-Notice") != "") != stale {
			t.Fatalf("skill %q: notice header %q", v, rec.Header().Get("X-Skill-Notice"))
		}
	}
	obj := `{"n":12345678901234567890,"f":1.10,"s":"x"}`
	rec := serve(obj, "")
	var got map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("object: %v %s", err, rec.Body.String())
	}
	if string(got["n"]) != "12345678901234567890" || string(got["f"]) != "1.10" || got["_notice"] == nil {
		t.Fatalf("object changed: %s", rec.Body.String())
	}
	if rec := serve(`{}`, ""); !strings.HasPrefix(rec.Body.String(), `{"_notice":"`) || !json.Valid(rec.Body.Bytes()) {
		t.Fatalf("empty object: %s", rec.Body.String())
	}
	if rec := serve(obj, "1.2.3"); rec.Body.String() != obj {
		t.Fatalf("current skill: %s", rec.Body.String())
	}
}

// Small-box trial B7: where no visitor can sign in, the kinds that take saves
// only from signed-in visitors are refused with a clear code, and where no
// email is sent, Submissions emails are off.
func TestNoVisitorSignInRefusesSignInKinds(t *testing.T) {
	s := newKindsSite(t, true)
	t.Cleanup(func() { s.a.sites.SetVisitorSignIn(true, []string{"google"}) })
	s.a.sites.SetVisitorSignIn(false, nil)
	for _, k := range []string{"entries", "mine", "board"} {
		r := s.owner(t, "PUT", "/v1/sites/shop/data/x"+k+"/kind", map[string]any{"kind": k})
		wantCode(t, "declare "+k, r, 409, "visitor_sign_in_unavailable")
		if !strings.Contains(string(r.body), "RESEND_API_KEY") {
			t.Fatalf("says what to set up: %s", r.body)
		}
	}
	wantCode(t, "a private list", s.owner(t, "PUT", "/v1/sites/shop/collections/orders/privacy", map[string]bool{"private": true}), 409, "visitor_sign_in_unavailable")
	s.declare(t, "menu", map[string]any{"kind": "content"}) // page info needs nobody to sign in

	// Google sign-in, no email: Submissions work, with no emails.
	s.a.sites.SetVisitorSignIn(false, []string{"google"})
	out := s.declare(t, "rsvps", map[string]any{"kind": "entries"})
	if out["notify"] != "off" {
		t.Fatalf("no email, yet notify %v", out["notify"])
	}
	wantCode(t, "asking for emails", s.owner(t, "PUT", "/v1/sites/shop/data/rsvps/kind", map[string]any{"kind": "entries", "notify": "daily"}), 409, "email_unavailable")
}

// Small-box trial B5: a server that gives sites no address of their own says
// plainly that it makes no preview links.
func TestNoPreviewOnSharedOrigin(t *testing.T) {
	a := newPrivateApp(t)
	if !a.sites.SharedOrigin() {
		t.Skip("the test app gives sites their own addresses")
	}
	ann := a.newPerson(t, "ann")
	key := map[string]string{"X-API-Key": ann.key}
	a.deploy(t, ann, "blog")
	r := a.at(t, "PUT", "simple-host.test", "/v1/sites/blog/files?publish=false", map[string]any{"files": map[string]string{"index.html": "v2"}}, key)
	if r.status != 200 || r.json(t)["preview_url"] != nil || !strings.Contains(r.json(t)["note"].(string), "per-site address") {
		t.Fatalf("publish=false: %d %s", r.status, r.body)
	}
	p := a.at(t, "POST", "simple-host.test", "/v1/sites/blog/versions/2/preview-link", nil, key)
	wantCode(t, "preview link", p, 409, "preview_unavailable")
	if !strings.Contains(string(p.body), "per-site address") {
		t.Fatalf("says why: %s", p.body)
	}
}

// Small-box trial B7/B14: an install's llms.txt starts with what holds there,
// and its messages name its own contact, not the hosted support mailbox.
func TestInstanceNoteAndContact(t *testing.T) {
	prevHosts, prevNote, prevContact := instanceHosts, instanceNote, auth.SupportContact
	t.Cleanup(func() {
		instanceHosts, instanceNote = prevHosts, prevNote
		auth.SetSupportContact(prevContact, true)
	})
	SetInstanceHosts("ev.test", "sites.ev.test", "")
	auth.SetSupportContact("whoever runs this server", false)
	SetInstanceNote(InstanceFacts{SiteDomain: "ev.test", ContentHost: "sites.ev.test", SharedOrigin: true, Contact: auth.SupportContact})
	rec := httptest.NewRecorder()
	serveRewrittenAsset("llms.txt", instanceHosts, time.Now()).ServeHTTP(rec, httptest.NewRequest("GET", "/llms.txt", nil))
	body := rec.Body.String()
	for _, want := range []string{"THIS SERVER (ev.test)", "shares that one browser origin", "Never call SH.requireSignIn()",
		"visitor_sign_in_unavailable", "preview_unavailable", "sends no email", "ask whoever runs this server",
		"(one browser origin, shared by every site on this server)"} {
		if !strings.Contains(body, want) {
			t.Errorf("llms.txt lacks %q", want)
		}
	}
	if !strings.HasPrefix(body, "THIS SERVER") {
		t.Errorf("the note is not first: %.80s", body)
	}
	if got := exportReadme(30); strings.Contains(got, "support@simple-host.app") || !strings.Contains(got, "whoever runs this server") {
		t.Errorf("export README names the hosted mailbox")
	}
	if msg := auth.InvalidKeyMessage(); strings.Contains(msg, "/v1/auth") {
		t.Errorf("invalid key message sends to email sign-in: %s", msg)
	}
	// On simple-host.app itself: no note.
	SetInstanceHosts("simple-host.app", "", "")
	SetInstanceNote(InstanceFacts{SiteDomain: "simple-host.app"})
	if instanceNote != "" {
		t.Errorf("a note on the hosted service")
	}
}
