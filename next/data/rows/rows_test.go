package rows_test

import (
	"context"
	"strings"
	"testing"

	"simplehost/core/testkit"
	"simplehost/data/rows"
	datarules "simplehost/data/rules"
	datasql "simplehost/data/sql"
	"simplehost/data/values"
)

// SPEC test 20 (F15): a page adds and reads rows and values. The owner reads
// and fixes them with SQL.
func TestPageAndOwnerShareTheData(t *testing.T) {
	k := testkit.Start(t, rows.Mount, values.Mount, datasql.Mount, datarules.Mount)
	bob, ann := k.Person(t, "bob"), k.Person(t, "ann")
	k.Publish(t, bob, "shop", map[string]string{"index.html": "shop"})
	s, _ := k.App.Sites.Get(context.Background(), bob.ID, "shop")
	site := k.App.Sites.URL(s) + "v1/sites/shop/storage/"
	owner := func(method, path string, body any) testkit.Res {
		return testkit.Call(t, k.Browser(), method, "https://simple-host.app/v1/sites/shop/storage/"+path, body, "X-API-Key", bob.Key)
	}
	want := func(r testkit.Res, status int, has string) {
		t.Helper()
		if r.Status != status || !strings.Contains(r.Body, has) {
			t.Fatalf("got %d %s, want %d with %q", r.Status, r.Body, status, has)
		}
	}

	want(owner("POST", "sqlite/orders/execute", map[string]any{"sql": "CREATE TABLE orders (id INTEGER PRIMARY KEY, item TEXT, qty INTEGER, visitor_id TEXT, created_at TEXT)"}), 200, "changes")
	mine := `{"version": 1, "policies": [{"name": "add", "command": "add", "check": "auth.signed_in"}, {"name": "own", "command": "all", "using": "visitor_id == auth.id"}]}`
	want(owner("PUT", "orders/rules", `{"rules": {"orders": `+mine+`, "_values": `+mine+`}}`), 200, "orders")

	page := k.Browser()
	k.SignIn(t, page, ann, k.App.Sites.URL(s))
	csrf := []string{"X-SH-CSRF", "1"}
	want(testkit.Call(t, page, "POST", site+"sqlite/orders/tables/orders/rows", map[string]any{"item": "tea", "qty": 1}, csrf...), 200, `"last_insert_id":1`)
	want(testkit.Call(t, page, "GET", site+"sqlite/orders/tables/orders/rows", nil), 200, `"tea"`)
	want(testkit.Call(t, page, "PUT", site+"kv/orders/keys/theme", map[string]any{"value": "dark"}, csrf...), 200, `"dark"`)
	want(testkit.Call(t, page, "GET", site+"kv/orders/keys/theme", nil), 200, `"value":"dark"`)

	// The owner sees who added the row (an opaque id) and the value, then fixes the row.
	want(owner("POST", "sqlite/orders/query", map[string]any{"sql": "SELECT item, visitor_id FROM orders"}), 200, ann.ID)
	want(owner("POST", "sqlite/orders/query", map[string]any{"sql": "SELECT value FROM _values WHERE key = ?", "params": []any{"theme"}}), 200, `dark`)
	want(owner("POST", "sqlite/orders/execute", map[string]any{"sql": "UPDATE orders SET qty = ? WHERE item = 'tea'", "params": []any{3}}), 200, `"changes":1`)
	want(testkit.Call(t, page, "GET", site+"sqlite/orders/tables/orders/rows/1", nil), 200, `"tea",3`)
}
