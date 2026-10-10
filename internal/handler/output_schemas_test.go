package handler

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/internal/config"
	"github.com/vsriram/simple-host/internal/mcp"
)

// Every connector tool's advertised outputSchema accepts what the tool really
// returns, driven end to end: OAuth token, tools/list over HTTP, then a
// successful tools/call of every tool against the real router and database.
// (The branch-by-branch check, including shapes this flow cannot reach, is
// TestEveryToolResultMatchesItsOutputSchema in internal/mcp.)
func TestOutputSchemasMatchRealResults(t *testing.T) {
	// Person hosts answer (serve), so a version preview has an address.
	a := newPersonApp(t, "serve")
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
	call("set_keep_versions", map[string]any{"site": "shop", "keep_versions": 0})
	call("list_versions", map[string]any{"site": "shop"})
	if s := call("update_site", map[string]any{"site": "shop", "files": map[string]any{"index.html": "<h1>shop v3</h1>"}, "publish": false}); s["unpublished_version"] != float64(3) || s["preview_url"] == nil || s["active_version"] != float64(2) {
		t.Fatalf("update_site publish false: %v", s)
	}
	if s := call("list_versions", map[string]any{"site": "shop"}); !strings.Contains(fmt.Sprint(s["versions"]), "not_yet_live:true") {
		t.Fatalf("list_versions: %v", s)
	}
	call("preview_version", map[string]any{"site": "shop", "version": 3})
	call("rollback_site", map[string]any{"site": "shop", "version": 1})
	call("set_visibility", map[string]any{"site": "shop", "visibility": "public"})
	call("set_home_page", map[string]any{"site": "shop"})
	call("set_home_page", map[string]any{"site": nil})
	call("set_bio", map[string]any{"bio": "My projects"})
	call("set_showcase_site", map[string]any{"site": "shop", "pinned": true, "order": 2})
	if s := call("set_site_offline", map[string]any{"site": "shop", "offline": true}); s["offline"] != true {
		t.Fatalf("set_site_offline: %v", s)
	}
	call("list_sites", map[string]any{})
	if s := call("set_site_offline", map[string]any{"site": "shop", "offline": false}); s["offline"] != false {
		t.Fatalf("set_site_offline back: %v", s)
	}
	// A passcode on the whole site: set, read back, sign everyone out, remove.
	if err := a.sites.SetPasscodeKey("q83vEjRWeJq83vEjRWeJq83vEjRWeJq83vEjRWeJq80="); err != nil {
		t.Fatal(err)
	}
	withPasscodeLimits(t, func(l *config.Limits) { l.SitePasscodes = true; l.PasscodeMinLength = 6 })
	if s := call("set_site_passcode", map[string]any{"site": "shop", "action": "set", "passcode": "482915"}); s["passcode_protected"] != true || s["passcode"] != "482915" {
		t.Fatalf("set_site_passcode set: %v", s)
	}
	if s := call("set_site_passcode", map[string]any{"site": "shop", "action": "read"}); s["passcode"] != "482915" || s["passcode_set_at"] == nil {
		t.Fatalf("set_site_passcode read: %v", s)
	}
	call("set_site_passcode", map[string]any{"site": "shop", "action": "sign_out_everyone"})
	if s := call("set_site_passcode", map[string]any{"site": "shop", "action": "remove"}); s["passcode_protected"] != false {
		t.Fatalf("set_site_passcode remove: %v", s)
	}
	if r := a.rpc(t, token, "tools/call", map[string]any{"name": "set_site_passcode", "arguments": map[string]any{"site": "shop", "action": "sign_out_everyone"}}); !strings.Contains(string(r.body), "no_passcode") {
		t.Fatalf("sign_out_everyone without a passcode: %s", r.body)
	}
	// Named viewers: grant (turns it on), read, revoke, open to anyone again.
	if s := call("grant_site_viewer", map[string]any{"site": "shop", "emails": []any{"mom@example.com", "dad@example.com"}}); s["access"] != "specific" {
		t.Fatalf("grant_site_viewer: %v", s)
	}
	call("list_site_viewers", map[string]any{"site": "shop"})
	call("revoke_site_viewer", map[string]any{"site": "shop", "email": "dad@example.com"})
	if s := call("set_site_access", map[string]any{"site": "shop", "access": "anyone"}); s["access"] != "anyone" {
		t.Fatalf("set_site_access: %v", s)
	}
	call("keep_site", map[string]any{"site": "shop", "keep": false})

	// Page code for the saved-data jobs the retired state/collection tools
	// used to cover; the dashboard still reads old sites' data directly.
	call("get_page_recipe", map[string]any{"topic": "records", "site": "shop"})

	// A free address is active at once; a custom domain waits for DNS.
	dom := "shop-" + handle + ".simple-host.test"
	if s := call("connect_domain", map[string]any{"site": "shop", "domain": dom}); s["status"] != "active" {
		t.Fatalf("free address not active: %v", s)
	}
	call("domain_status", map[string]any{"site": "shop"})
	if s := call("connect_domain", map[string]any{"site": "plain", "domain": "plain-" + handle + ".example.test"}); s["status"] != "pending" || s["dns_record"] == nil {
		t.Fatalf("custom domain not pending with a DNS record: %v", s)
	}
	call("domain_status", map[string]any{"site": "plain"})

	// Storage resources: every owner connector adapter forwards to the same
	// REST handlers and returns JSON/status without buffering file bytes.
	for _, resource := range []struct{ name, kind string }{{"kvstore", "kv"}, {"sqldb", "sqlite"}, {"filestore", "files"}} {
		call("storage_set_resource", map[string]any{"site": "shop", "name": resource.name, "body": map[string]any{"kind": resource.kind, "read": "owner", "write": "owner", "site_passcode": "inherit"}})
	}
	call("storage_list_resources", map[string]any{"site": "shop"})
	call("storage_get_usage", map[string]any{"site": "shop"})
	call("storage_put_kv", map[string]any{"site": "shop", "name": "kvstore", "key": "greeting", "value": "hello"})
	call("storage_get_kv", map[string]any{"site": "shop", "name": "kvstore", "key": "greeting"})
	call("storage_list_kv_keys", map[string]any{"site": "shop", "name": "kvstore", "prefix": "g", "limit": 10})
	call("storage_delete_kv", map[string]any{"site": "shop", "name": "kvstore", "key": "greeting"})
	call("storage_sql_schema", map[string]any{"site": "shop", "name": "sqldb", "sql": "CREATE TABLE entries(n INTEGER)"})
	call("storage_sql_execute", map[string]any{"site": "shop", "name": "sqldb", "sql": "INSERT INTO entries(n) VALUES(?)", "params": []any{1}})
	call("storage_sql_query", map[string]any{"site": "shop", "name": "sqldb", "sql": "SELECT n FROM entries"})
	call("storage_put_file", map[string]any{"site": "shop", "name": "filestore", "path": "note.txt", "content_base64": "aGk=", "content_type": "text/plain"})
	call("storage_list_file_objects", map[string]any{"site": "shop", "name": "filestore", "prefix": "n", "limit": 10})
	call("storage_file_download_link", map[string]any{"site": "shop", "name": "filestore", "path": "note.txt"})
	call("storage_delete_file", map[string]any{"site": "shop", "name": "filestore", "path": "note.txt"})
	call("storage_delete_resource", map[string]any{"site": "shop", "name": "filestore"})
	if s := call("storage_visitor_emails", map[string]any{"site": "shop", "visitor_ids": []any{"1b4e28ba-2fa1-11d2-883f-0016d3cca427"}}); !strings.Contains(fmt.Sprint(s["visitors"]), "found:false") {
		t.Fatalf("storage_visitor_emails: %v", s)
	}
	missingStorage := a.rpc(t, token, "tools/call", map[string]any{"name": "storage_get_kv", "arguments": map[string]any{"site": "shop", "name": "missing", "key": "x"}})
	missingText, _, missingErr := toolResultOf(t, missingStorage)
	if !missingErr || !strings.Contains(missingText, "HTTP 404") || !strings.Contains(missingText, "resource_not_found") {
		t.Fatalf("storage REST missing-resource error not preserved: %s", missingStorage.body)
	}
	vicToken := a.connect(t, vic, a.registerClient(t, testRedirect), testRedirect)["access_token"].(string)
	otherStorage := a.rpc(t, vicToken, "tools/call", map[string]any{"name": "storage_list_resources", "arguments": map[string]any{"site": "shop"}})
	otherText, _, otherErr := toolResultOf(t, otherStorage)
	if !otherErr || !strings.Contains(otherText, "HTTP 403") {
		t.Fatalf("other owner's connector reached storage resources: %s", otherStorage.body)
	}

	call("site_analytics", map[string]any{"site": "shop", "days": 7})
	call("export_site", map[string]any{"site": "shop"})
	call("list_sites", map[string]any{})
	if s := call("remove_domain", map[string]any{"site": "plain", "confirm_domain": "plain-" + handle + ".example.test"}); s["url"] == nil {
		t.Errorf("remove_domain gave no address: %v", s)
	}
	call("rename_site", map[string]any{"site": "plain", "new_name": "plain-two"})
	call("delete_site", map[string]any{"site": "plain-two", "confirm_name": "plain-two"})
	call("list_deleted_sites", map[string]any{})
	call("restore_site", map[string]any{"site": "plain-two"})

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
