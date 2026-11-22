package datatest

import (
	"strings"
	"testing"
	"time"
)

// An own (records) table with a child table: the server stamps visitor_id
// and created_at, and a child row may only reference a parent the visitor
// may read.
func TestStorageOrderChangeReferences(t *testing.T) {
	s := newShop(t)
	ah, bh := s.Callers["alice"], s.Callers["bob"]
	s.Must(t, "PUT", "/resources/orders", StorageRules("sqlite", RulesRecords), s.Key, 201)
	schema := func(sql string) {
		t.Helper()
		s.Must(t, "POST", "/sqlite/orders/schema", map[string]string{"sql": sql}, s.Key, 200)
	}
	schema("CREATE TABLE orders (id INTEGER PRIMARY KEY,item TEXT,status TEXT DEFAULT 'placed',created_at TEXT)")
	schema("CREATE TABLE order_changes (id INTEGER PRIMARY KEY,order_id INTEGER NOT NULL REFERENCES orders(id),kind TEXT,details TEXT,created_at TEXT)")
	rows := "/sqlite/orders/tables/"
	before := time.Now().UTC().Add(-time.Second)
	aid := s.Must(t, "POST", rows+"orders/rows", map[string]any{"item": "Alice tea", "visitor_id": "forged", "created_at": "1900-01-01"}, ah, 200).JSON(t)["last_insert_id"]
	bid := s.Must(t, "POST", rows+"orders/rows", map[string]any{"item": "Bob tea"}, bh, 200).JSON(t)["last_insert_id"]
	s.Must(t, "POST", rows+"orders/rows", map[string]any{"item": "x"}, s.Callers["anon"], 401)
	s.Must(t, "GET", rows+"orders/rows", nil, s.Callers["anon"], 401)
	// Another visitor's order and a missing one answer alike.
	for _, id := range []any{bid, 999999} {
		if r := s.Must(t, "POST", rows+"order_changes/rows", map[string]any{"order_id": id, "kind": "note"}, ah, 404); r.Code() != "invalid_reference" {
			t.Fatalf("reference code: %s", r.Body)
		}
	}
	s.Must(t, "POST", rows+"order_changes/rows", map[string]any{"order_id": aid, "kind": "note", "details": "no sugar"}, ah, 200)
	s.Must(t, "POST", rows+"order_changes/rows", map[string]any{"order_id": bid, "kind": "cancel"}, bh, 200)
	data := s.Must(t, "GET", rows+"orders/rows", nil, ah, 200).JSON(t)
	cols, values := data["columns"].([]any), data["rows"].([]any)
	if len(values) != 1 {
		t.Fatalf("alice sees %d orders", len(values))
	}
	row := values[0].([]any)
	stamp, err := time.Parse(time.RFC3339Nano, row[colIndex(cols, "created_at")].(string))
	if row[colIndex(cols, "visitor_id")] != s.AliceID || err != nil || stamp.Before(before) {
		t.Fatalf("stamps: %v", row)
	}
	if r := s.Must(t, "GET", rows+"order_changes/rows", nil, bh, 200); len(r.JSON(t)["rows"].([]any)) != 1 || strings.Contains(string(r.Body), "no sugar") {
		t.Fatalf("bob history: %s", r.Body)
	}
	if r := s.Must(t, "GET", rows+"order_changes/rows", nil, s.Key, 200); len(r.JSON(t)["rows"].([]any)) != 2 {
		t.Fatalf("owner history: %s", r.Body)
	}
	// A default and a composite key are checked on the inserted values.
	schema("CREATE TABLE default_changes (id INTEGER PRIMARY KEY,order_id INTEGER DEFAULT 2 REFERENCES orders,details TEXT)")
	s.Must(t, "POST", rows+"default_changes/rows", map[string]string{"details": "foreign default"}, ah, 404)
	s.Must(t, "POST", rows+"default_changes/rows", map[string]any{"order_id": nil}, ah, 200)
	schema("CREATE TABLE pairs (id INTEGER PRIMARY KEY,code TEXT,UNIQUE(id,code))")
	pair := s.Must(t, "POST", rows+"pairs/rows", map[string]string{"code": "B"}, bh, 200).JSON(t)["last_insert_id"]
	schema("CREATE TABLE pair_changes (id INTEGER PRIMARY KEY,parent_id INTEGER,code TEXT,FOREIGN KEY(parent_id,code) REFERENCES pairs(id,code))")
	s.Must(t, "POST", rows+"pair_changes/rows", map[string]any{"parent_id": pair, "code": "B"}, ah, 404)
	s.Must(t, "POST", rows+"pair_changes/rows", map[string]any{"parent_id": pair, "code": "B"}, bh, 200)
}
