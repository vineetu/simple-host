package mcp_test

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"

	"simplehost/access/connector"
	"simplehost/access/keys"
	"simplehost/access/signin"
	"simplehost/access/teams"
	"simplehost/access/visitors"
	"simplehost/api"
	"simplehost/core/testkit"
	"simplehost/data/files"
	"simplehost/data/recipes"
	"simplehost/data/rows"
	datarules "simplehost/data/rules"
	datasql "simplehost/data/sql"
	"simplehost/data/values"
	"simplehost/mcp"
	"simplehost/ops/admin"
	"simplehost/ops/erase"
	"simplehost/ops/export"
	"simplehost/ops/holds"
	"simplehost/ops/notify"
	"simplehost/sites/domains"
	"simplehost/sites/locks"
	"simplehost/sites/manage"
	"simplehost/sites/publicpage"
	"simplehost/sites/publish"
	"simplehost/sites/serve"
	"simplehost/sites/stats"
	"simplehost/sites/uploads"
)

// seen is one route a tool call replayed.
type seen struct {
	method, pattern string
	body            string
}

// The hint rule (tools.go), as the routes show it.
func reads(s seen) bool {
	return s.method == "GET" || s.method == "POST" && (strings.HasSuffix(s.pattern, "/query") || strings.HasSuffix(s.pattern, "/schema"))
}
func destroys(s seen) bool {
	return s.method == "PUT" || s.method == "PATCH" || s.method == "DELETE" || s.method == "POST" && strings.HasSuffix(s.pattern, "/execute")
}
func adds(s seen) bool      { return !reads(s) && !destroys(s) } // a POST that makes something new
func publishes(s seen) bool { return s.method == "PUT" && s.pattern == "PUT /v1/sites/{site}" }
func reachesOut(s seen) bool {
	var body map[string]any
	json.Unmarshal([]byte(s.body), &body)
	_, domain := body["domain"]
	_, short := body["short_name"]
	_, listed := body["listed"]
	switch {
	case s.pattern == "POST /v1/teams/{team}/invites": // an email to any address
		return true
	case publishes(s), strings.HasSuffix(s.pattern, "/rules"): // public pages, public data
		return true
	case s.pattern == "POST /v1/sites/{site}/upload-links":
		return body["public"] == true
	case s.pattern == "PATCH /v1/owners/{owner}":
		return domain
	case s.pattern == "PATCH /v1/sites/{site}":
		return domain || short || listed || body["access"] == "open"
	}
	return false
}

// Every tool, called on the test service: its structuredContent matches its
// outputSchema, and its hints follow the rule in tools.go for the routes it
// really replays (each case runs twice: a write that sends something
// different the second time is not idempotent).
func TestEveryToolsOutputAndHints(t *testing.T) {
	k := testkit.Start(t, signin.Mount, keys.Mount, teams.Mount, connector.Mount, visitors.Mount, domains.Mount, locks.Mount,
		publish.Mount, uploads.Mount, manage.Mount, serve.Mount, stats.Mount, publicpage.Mount, rows.Mount, values.Mount, files.Mount,
		datasql.Mount, datarules.Mount, recipes.Mount, api.Mount, mcp.Mount, notify.Mount, export.Mount, erase.Mount, admin.Mount, holds.Mount)
	ann, ben, zed, op := k.Person(t, "ann"), k.Person(t, "ben"), k.Person(t, "zed"), k.Person(t, "boss")
	k.Exec(t, `UPDATE people SET operator = true WHERE id = $1`, op.ID)
	k.Publish(t, ann, "shop", map[string]string{"index.html": "<h1>Shop</h1>"})
	k.Publish(t, zed, "spare", map[string]string{"index.html": "spare"})
	b := k.Browser()
	call := func(key, method string, params any) (map[string]any, bool) {
		r := testkit.Call(t, b, "POST", "https://simple-host.app/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params}, "X-API-Key", key)
		var out struct{ Result map[string]any }
		json.Unmarshal([]byte(r.Body), &out)
		return out.Result, r.Status == 200 && out.Result != nil
	}
	listed := map[string]map[string]any{}
	for _, key := range []string{ann.Key, op.Key} {
		res, _ := call(key, "tools/list", nil)
		for _, x := range res["tools"].([]any) {
			x := x.(map[string]any)
			listed[x["name"].(string)] = x
		}
	}

	// The passcode screen's sealed session.
	private, _ := ecdh.P256().GenerateKey(rand.Reader)
	session := base64.RawURLEncoding.EncodeToString(private.PublicKey().Bytes())
	open, _ := call(ann.Key, "tools/call", map[string]any{"name": "passcode_screen_open", "arguments": map[string]any{"site": "shop", "client_key": session}})
	serverKey, _ := base64.RawURLEncoding.DecodeString(open["structuredContent"].(map[string]any)["server_key"].(string))
	public, _ := ecdh.P256().NewPublicKey(serverKey)
	shared, _ := private.ECDH(public)
	aesKey, _ := hkdf.Key(sha256.New, shared, nil, "simple-host passcode screen v1", 32)
	block, _ := aes.NewCipher(aesKey)
	aead, _ := cipher.NewGCM(block)
	nonce := make([]byte, 12)
	sealed := aead.Seal(nil, nonce, []byte("tulip-garden-7"), []byte("set|shop|"+session))

	shop := func(more ...any) map[string]any {
		a := map[string]any{"site": "shop"}
		for i := 0; i+1 < len(more); i += 2 {
			a[more[i].(string)] = more[i+1]
		}
		return a
	}
	orders := func(more ...any) map[string]any { return shop(append([]any{"namespace", "orders"}, more...)...) }
	rules := map[string]any{"orders": map[string]any{"version": 1, "policies": []any{map[string]any{"name": "read", "command": "read", "using": "true"}}}}
	type c struct {
		key, name string
		args      map[string]any
	}
	cases := []c{
		{ann.Key, "who_am_i", nil},
		{ann.Key, "who_am_i", map[string]any{"include_email": true}},
		{ann.Key, "update_account", map[string]any{"display_name": "Ann"}},
		{ann.Key, "update_account", map[string]any{"domain": ""}},
		{ann.Key, "create_team", map[string]any{"handle": "crew"}},
		{ann.Key, "invite_to_team", map[string]any{"team": "crew", "email": ben.Email}},
		{ben.Key, "accept_invite", map[string]any{"team": "crew"}},
		{ann.Key, "remove_from_team", map[string]any{"team": "crew", "email": ben.Email}},
		{ann.Key, "delete_team", map[string]any{"team": "crew"}},
		{ann.Key, "list_sites", nil},
		{ann.Key, "get_site", shop()},
		{ann.Key, "publish_site", shop("files", map[string]any{"index.html": "<h1>Shop 2</h1>"})},
		{ann.Key, "create_upload_link", shop()},
		{ann.Key, "read_site_file", shop("path", "index.html")},
		{ann.Key, "change_site", shop("offline", false)},
		{ann.Key, "change_site", shop("listed", true)},
		{ann.Key, "set_site_access", shop("access", "viewers", "viewers", []any{ben.Email})},
		{ann.Key, "set_site_access", shop("access", "open")},
		{ann.Key, "set_site_access", shop("access", "passcode")},
		{ann.Key, "passcode_screen_open", shop()},
		{ann.Key, "passcode_screen_set", shop("session", session, "nonce", base64.RawURLEncoding.EncodeToString(nonce), "ciphertext", base64.RawURLEncoding.EncodeToString(sealed))},
		{ann.Key, "passcode_screen_show", shop("session", session)},
		{ann.Key, "passcode_screen_generate", shop("session", session)},
		{ann.Key, "set_site_address", shop("short_name", "shopx")},
		{ann.Key, "set_site_address", shop("short_name", "")},
		{ann.Key, "site_stats", shop()},
		{ann.Key, "set_notifications", shop("notifications", []any{})},
		{ann.Key, "run_sql", orders("sql", "CREATE TABLE IF NOT EXISTS orders (id INTEGER PRIMARY KEY, item TEXT)")},
		{ann.Key, "list_tables", orders()},
		{ann.Key, "add_row", orders("table", "orders", "row", map[string]any{"item": "tea"})},
		{ann.Key, "query_sql", orders("sql", "SELECT * FROM orders WHERE item = ?", "params", []any{"tea"})},
		{ann.Key, "list_rows", orders("table", "orders", "where", map[string]any{"item": "tea"})},
		{ann.Key, "read_row", orders("table", "orders", "id", 1)},
		{ann.Key, "change_row", orders("table", "orders", "id", 1, "row", map[string]any{"item": "coffee"})},
		{ann.Key, "delete_row", orders("table", "orders", "id", 1)},
		{ann.Key, "set_value", orders("key", "today", "value", map[string]any{"soup": "leek"})},
		{ann.Key, "list_values", orders()},
		{ann.Key, "read_value", orders("key", "today")},
		{ann.Key, "delete_value", orders("key", "today")},
		{ann.Key, "save_file", orders("path", "notes/a.txt", "content_base64", "aGk=", "content_type", "text/plain")},
		{ann.Key, "list_files", orders()},
		{ann.Key, "delete_file", orders("path", "notes/a.txt")},
		{ann.Key, "set_data_rules", orders("rules", rules)},
		{ann.Key, "get_recipe", map[string]any{"topic": "inbox", "site": "shop"}},
		{ann.Key, "download_my_data", nil},
		{op.Key, "admin_overview", nil},
		{op.Key, "set_hold", map[string]any{"site": "spare", "owner": "zed", "taken_down": false}},
		{ann.Key, "delete_site", shop()},
		{zed.Key, "delete_account", nil},
	}

	type facts struct{ called, readOnly, destructive, openWorld, idempotent bool }
	got := map[string]*facts{}
	for _, tc := range cases {
		var runs [2][]seen
		for run := range 2 {
			mcp.Observe(func(method, pattern string, body any) {
				b, _ := json.Marshal(body)
				runs[run] = append(runs[run], seen{method, pattern, string(b)})
			})
			res, ok := call(tc.key, "tools/call", map[string]any{"name": tc.name, "arguments": tc.args})
			mcp.Observe(nil)
			if run == 1 {
				continue
			}
			if !ok || res["isError"] == true {
				t.Errorf("%s %v: %v", tc.name, tc.args, res)
				continue
			}
			schema, _ := listed[tc.name]["outputSchema"].(map[string]any)
			if schema == nil {
				t.Errorf("%s declares no outputSchema", tc.name)
				continue
			}
			sc, ok := res["structuredContent"].(map[string]any)
			if !ok {
				t.Errorf("%s %v: no structuredContent", tc.name, tc.args)
				continue
			}
			if errs := testkit.Validate(schema, sc, "structuredContent"); len(errs) > 0 {
				b, _ := json.Marshal(sc)
				t.Errorf("%s %v does not match its outputSchema:\n  %s\n  got %s", tc.name, tc.args, strings.Join(errs, "\n  "), b)
			}
		}
		f := got[tc.name]
		if f == nil {
			f = &facts{readOnly: true, idempotent: true}
			got[tc.name] = f
		}
		writes := map[string]string{}
		for _, s := range runs[0] {
			f.called = true
			f.readOnly = f.readOnly && reads(s)
			f.destructive = f.destructive || destroys(s)
			f.openWorld = f.openWorld || reachesOut(s)
			if adds(s) || publishes(s) || destroys(s) && s.method == "POST" {
				f.idempotent = false
			}
			if s.method == "PUT" || s.method == "PATCH" || s.method == "DELETE" {
				writes[s.pattern] = s.body
			}
		}
		for _, s := range runs[1] { // the same write again, with something new in it
			if w, ok := writes[s.pattern]; ok && w != s.body {
				f.idempotent = false
			}
		}
	}
	names := make([]string, 0, len(listed))
	for name := range listed {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		f := got[name]
		if f == nil || !f.called {
			t.Errorf("%s: no case calls it, so its output and hints are unchecked", name)
			continue
		}
		hints := listed[name]["annotations"].(map[string]any)
		want := map[string]bool{"readOnlyHint": f.readOnly, "destructiveHint": f.destructive, "openWorldHint": f.openWorld, "idempotentHint": f.idempotent}
		for hint, w := range want {
			if g, ok := hints[hint].(bool); !ok || g != w {
				t.Errorf("%s %s is %v; the rule in tools.go says %v for the routes it calls", name, hint, hints[hint], w)
			}
		}
		if fmt.Sprint(listed[name]["title"]) == "" {
			t.Errorf("%s has no title", name)
		}
	}
}
