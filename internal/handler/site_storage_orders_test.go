package handler

import (
	"strings"
	"testing"
	"time"
)

func TestStorageOrderChangeReferences(t *testing.T) {
	a, cert := newSiteApp(t, "canonical")
	owner := a.newPerson(t, "change-owner")
	alice := a.newPerson(t, "change-alice")
	bob := a.newPerson(t, "change-bob")
	a.deploy(t, owner, "shop")
	_, handle := a.userID(t, owner)
	markReady(t, cert, handle)
	host := "shop." + handle + "." + pcSiteDomain
	sid := a.siteID(t, owner, "shop")
	ah, bh := browser(host, a.session(t, alice, sid, host)), browser(host, a.session(t, bob, sid, host))
	key := map[string]string{"X-API-Key": owner.key}
	base := "/v1/sites/shop/storage"
	check := func(method, origin, path string, body any, headers map[string]string, status int) resp {
		t.Helper()
		r := a.at(t, method, origin, base+path, body, headers)
		if r.status != status {
			t.Fatalf("%s %s: want %d got %d %s", method, path, status, r.status, r.body)
		}
		return r
	}
	check("PUT", pcSiteDomain, "/resources/orders", map[string]string{"kind": "sqlite", "read": "own", "write": "signed-in", "write_mode": "add"}, key, 201)
	schema := func(sql string) {
		t.Helper()
		check("POST", pcSiteDomain, "/sqlite/orders/schema", map[string]string{"sql": sql}, key, 200)
	}
	schema("CREATE TABLE orders (id INTEGER PRIMARY KEY,item TEXT,status TEXT DEFAULT 'placed',created_at TEXT)")
	schema("CREATE TABLE order_changes (id INTEGER PRIMARY KEY,order_id INTEGER NOT NULL REFERENCES orders(id),kind TEXT CHECK(kind IN ('change','note','cancel_request')),details TEXT,created_at TEXT)")
	rows := "/sqlite/orders/tables/"
	before := time.Now().UTC().Add(-time.Second)
	aid := check("POST", host, rows+"orders/rows", map[string]any{"item": "Alice tea", "visitor_id": "forged", "created_at": "1900-01-01"}, ah, 200).json(t)["last_insert_id"]
	bid := check("POST", host, rows+"orders/rows", map[string]any{"item": "Bob tea"}, bh, 200).json(t)["last_insert_id"]
	for _, table := range []string{"orders", "order_changes"} {
		check("POST", host, rows+table+"/rows", map[string]any{"order_id": aid, "kind": "note"}, browser(host, ""), 401)
		check("GET", host, rows+table+"/rows", nil, nil, 401)
	}
	for _, attempt := range []struct {
		id     any
		status int
	}{{bid, 403}, {999999, 404}} {
		r := check("POST", host, rows+"order_changes/rows", map[string]any{"order_id": attempt.id, "kind": "note", "details": "forged"}, ah, attempt.status)
		if r.json(t)["code"] != "invalid_reference" {
			t.Fatalf("reference code: %s", r.body)
		}
	}
	for _, change := range []struct {
		id            any
		headers       map[string]string
		kind, details string
	}{
		{aid, ah, "change", "quantity: 3"}, {aid, ah, "note", "no sugar"}, {bid, bh, "cancel_request", "please cancel"},
	} {
		check("POST", host, rows+"order_changes/rows", map[string]any{"order_id": change.id, "kind": change.kind, "details": change.details, "visitor_id": "forged", "created_at": "1900-01-01"}, change.headers, 200)
	}
	aliceID, _ := a.userID(t, alice)
	for _, table := range []string{"orders", "order_changes"} {
		r := check("GET", host, rows+table+"/rows", nil, ah, 200)
		data := r.json(t)
		cols, values := data["columns"].([]any), data["rows"].([]any)
		if table == "orders" && len(values) != 1 || table == "order_changes" && len(values) != 2 {
			t.Fatalf("history count: %s", r.body)
		}
		for _, value := range values {
			row := value.([]any)
			for i, col := range cols {
				if col == "visitor_id" && row[i] != aliceID {
					t.Fatalf("identity: %s", r.body)
				}
				if col == "created_at" {
					stamp, err := time.Parse(time.RFC3339Nano, row[i].(string))
					if err != nil || stamp.Before(before) || stamp.After(time.Now().UTC()) {
						t.Fatalf("timestamp: %s", r.body)
					}
				}
			}
		}
		if strings.Contains(string(r.body), "forged") || strings.Contains(string(r.body), "1900") {
			t.Fatalf("client metadata persisted: %s", r.body)
		}
	}
	bobHistory := check("GET", host, rows+"order_changes/rows", nil, bh, 200)
	if len(bobHistory.json(t)["rows"].([]any)) != 1 || strings.Contains(string(bobHistory.body), "no sugar") {
		t.Fatalf("bob history: %s", bobHistory.body)
	}
	all := check("GET", pcSiteDomain, rows+"order_changes/rows", nil, key, 200)
	if len(all.json(t)["rows"].([]any)) != 3 {
		t.Fatalf("owner history/rollback: %s", all.body)
	}
	check("POST", pcSiteDomain, "/sqlite/orders/execute", map[string]any{"sql": "UPDATE orders SET status=? WHERE id=?", "params": []any{"packed", aid}}, key, 200)
	if r := check("GET", host, rows+"orders/rows", nil, ah, 200); !strings.Contains(string(r.body), "packed") {
		t.Fatalf("owner stage: %s", r.body)
	}
	// Defaults and implicit/composite keys use the actual inserted values too.
	schema("CREATE TABLE default_changes (id INTEGER PRIMARY KEY,order_id INTEGER DEFAULT 2 REFERENCES orders,details TEXT)")
	check("POST", host, rows+"default_changes/rows", map[string]string{"details": "foreign default"}, ah, 403)
	check("POST", host, rows+"default_changes/rows", map[string]any{"order_id": nil, "details": "optional link"}, ah, 200)
	schema("CREATE TABLE pairs (id INTEGER PRIMARY KEY,code TEXT,UNIQUE(id,code))")
	pair := check("POST", host, rows+"pairs/rows", map[string]string{"code": "B"}, bh, 200).json(t)["last_insert_id"]
	schema("CREATE TABLE pair_changes (id INTEGER PRIMARY KEY,parent_id INTEGER,code TEXT,FOREIGN KEY(parent_id,code) REFERENCES pairs(id,code))")
	check("POST", host, rows+"pair_changes/rows", map[string]any{"parent_id": pair, "code": "B"}, ah, 403)
	check("POST", host, rows+"pair_changes/rows", map[string]any{"parent_id": pair, "code": "missing"}, bh, 404)
	check("POST", host, rows+"pair_changes/rows", map[string]any{"parent_id": pair, "code": "B"}, bh, 200)
}
