package handler

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

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
