package handler

import (
	"encoding/base64"
	"net/http"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/internal/mcp"
)

// Every connector tool's advertised outputSchema accepts what the tool really
// returns, driven end to end: OAuth token, tools/list over HTTP, then a
// successful tools/call of every tool against the real router and database.
// (The branch-by-branch check, including shapes this flow cannot reach, is
// TestEveryToolResultMatchesItsOutputSchema in internal/mcp.)
func TestOutputSchemasMatchRealResults(t *testing.T) {
	a := newPrivateApp(t)
	olive, vic := a.newPerson(t, "olive"), a.newPerson(t, "vic")
	token := a.connect(t, olive, a.registerClient(t, testRedirect), testRedirect)["access_token"].(string)
	_, handle := a.userID(t, olive)

	list := a.rpc(t, token, "tools/list", map[string]any{})
	if list.status != http.StatusOK {
		t.Fatalf("tools/list: %d %s", list.status, list.body)
	}
	schemas := map[string]any{}
	for _, raw := range list.json(t)["result"].(map[string]any)["tools"].([]any) {
		tool := raw.(map[string]any)
		name := tool["name"].(string)
		schema, ok := tool["outputSchema"]
		if !ok {
			t.Errorf("%s: no outputSchema in tools/list", name)
			continue
		}
		if err := mcp.CheckOutputSchema(schema); err != nil {
			t.Errorf("%s: %v", name, err)
		}
		schemas[name] = schema
	}
	if len(schemas) != len(mcp.Tools()) {
		t.Fatalf("%d tools listed with a schema, %d registered", len(schemas), len(mcp.Tools()))
	}

	called := map[string]bool{}
	call := func(name string, args map[string]any) map[string]any {
		t.Helper()
		r := a.rpc(t, token, "tools/call", map[string]any{"name": name, "arguments": args})
		text, structured, isErr := toolResultOf(t, r)
		if isErr || structured == nil {
			t.Fatalf("%s %v failed: %s", name, args, text)
		}
		if err := mcp.ValidateOutput(schemas[name], structured, nil); err != nil {
			t.Errorf("%s %v: structuredContent does not match outputSchema: %v\n%s", name, args, err, r.body)
		}
		called[name] = true
		return structured
	}

	png := base64.StdEncoding.EncodeToString([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"))
	call("who_am_i", map[string]any{})
	call("list_sites", map[string]any{})
	call("create_site", map[string]any{"site": "shop", "files": map[string]any{"index.html": "<h1>shop</h1>"}, "files_base64": map[string]any{"logo.png": png}})
	call("create_site", map[string]any{"site": "plain", "files": map[string]any{"index.html": "<h1>plain</h1>"}})
	call("get_site", map[string]any{"site": "shop"})
	call("read_site_file", map[string]any{"site": "shop", "path": "index.html"})
	if s := call("read_site_file", map[string]any{"site": "shop", "path": "logo.png"}); s["binary"] != true {
		t.Errorf("logo.png not reported as binary: %v", s)
	}
	call("update_site", map[string]any{"site": "shop", "files": map[string]any{"index.html": "<h1>shop v2</h1>"}})
	call("list_versions", map[string]any{"site": "shop"})
	call("rollback_site", map[string]any{"site": "shop", "version": 1})
	call("set_visibility", map[string]any{"site": "shop", "visibility": "public"})

	state := call("get_state", map[string]any{"site": "shop"})
	call("update_state", map[string]any{"site": "shop", "ops": []any{map[string]any{"op": "inc", "path": "count", "by": 1}}})
	state = call("get_state", map[string]any{"site": "shop"})
	call("update_state", map[string]any{"site": "shop", "replace": map[string]any{"count": 5}, "if_match": state["etag"]})

	call("add_to_collection", map[string]any{"site": "shop", "collection": "rsvps", "item": map[string]any{"name": "Ann"}})
	call("read_collection", map[string]any{"site": "shop", "collection": "rsvps"})

	// A free address is active at once; a custom domain waits for DNS.
	dom := handle + ".simple-host.test"
	if s := call("connect_domain", map[string]any{"site": "shop", "domain": dom}); s["status"] != "active" {
		t.Fatalf("free address not active: %v", s)
	}
	call("domain_status", map[string]any{"site": "shop"})
	if s := call("connect_domain", map[string]any{"site": "plain", "domain": "plain-" + handle + ".example.test"}); s["status"] != "pending" || s["dns_record"] == nil {
		t.Fatalf("custom domain not pending with a DNS record: %v", s)
	}
	call("domain_status", map[string]any{"site": "plain"})

	// A private collection, filled by a signed-in visitor on the site's domain.
	call("set_collection_privacy", map[string]any{"site": "shop", "collection": "orders", "private": true})
	cookie := a.session(t, vic, a.siteID(t, olive, "shop"), dom)
	if r := a.at(t, "POST", dom, "/v1/sites/shop/collections/orders", map[string]string{"item": "mug"}, browser(dom, cookie)); r.status != http.StatusCreated {
		t.Fatalf("visitor submit: %d %s", r.status, r.body)
	}
	orders := call("read_collection", map[string]any{"site": "shop", "collection": "orders", "limit": 10})
	items := orders["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("orders: %v", orders)
	}
	id := items[0].(map[string]any)["id"].(string)
	call("list_collections", map[string]any{"site": "shop"})
	call("update_collection_item", map[string]any{"site": "shop", "collection": "orders", "id": id, "fields": map[string]any{"status": "done"}})
	call("delete_collection_item", map[string]any{"site": "shop", "collection": "orders", "id": id, "confirm_id": id})
	call("set_collection_privacy", map[string]any{"site": "shop", "collection": "orders", "private": false})

	call("site_analytics", map[string]any{"site": "shop", "days": 7})
	call("list_sites", map[string]any{})
	call("rename_site", map[string]any{"site": "plain", "new_name": "plain-two"})
	call("delete_site", map[string]any{"site": "plain-two", "confirm_name": "plain-two"})

	var missing []string
	for name := range schemas {
		if !called[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		t.Errorf("tools never called: %s", strings.Join(missing, ", "))
	}
}
