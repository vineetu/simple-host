package compat_test

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"simplehost/access/connector"
	"simplehost/access/keys"
	"simplehost/access/signin"
	"simplehost/access/teams"
	"simplehost/access/visitors"
	"simplehost/api"
	"simplehost/compat"
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
	"simplehost/web"
)

// testdata/old-tools.json is the old service's tool list as AI apps hold it
// (name, inputSchema, outputSchema), dumped from the build that ran before
// the switch (simple-host 1b0d7385f). An app that holds a tool's
// outputSchema validates every successful answer against it, so each old
// name's structuredContent must match the old schema, and a refusal must be
// an error with no structuredContent (not validated).
func TestOldToolAnswersMatchTheOldOutputSchemas(t *testing.T) {
	raw, err := os.ReadFile("testdata/old-tools.json")
	if err != nil {
		t.Fatal(err)
	}
	var old []struct {
		Name         string         `json:"name"`
		OutputSchema map[string]any `json:"outputSchema"`
	}
	if err := json.Unmarshal(raw, &old); err != nil {
		t.Fatal(err)
	}
	schemas := map[string]map[string]any{}
	for _, o := range old {
		schemas[o.Name] = o.OutputSchema
	}

	// Every feature mounted, as main mounts them.
	k := testkit.Start(t, signin.Mount, keys.Mount, teams.Mount, connector.Mount, visitors.Mount, domains.Mount, locks.Mount,
		publish.Mount, uploads.Mount, manage.Mount, serve.Mount, stats.Mount, publicpage.Mount, rows.Mount, values.Mount, files.Mount,
		datasql.Mount, datarules.Mount, recipes.Mount, api.Mount, mcp.Mount, notify.Mount, export.Mount, erase.Mount, admin.Mount, holds.Mount, web.Mount)
	compat.Wrap(k.App, k.App) // makes its table
	ann, ben := k.Person(t, "ann"), k.Person(t, "ben")
	k.Publish(t, ann, "shop", map[string]string{"index.html": "<h1>Shop</h1>", "a.txt": "aaa"})
	k.Publish(t, ann, "shop", map[string]string{"index.html": "<h1>Shop 2</h1>", "a.txt": "aaa"})
	k.Publish(t, ann, "blog", map[string]string{"index.html": "hi"})
	b := k.Browser()
	call := func(name string, args map[string]any) map[string]any {
		t.Helper()
		r := testkit.Call(t, b, "POST", platform+"/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call",
			"params": map[string]any{"name": name, "arguments": args}}, "X-API-Key", ann.Key)
		var out struct {
			Result map[string]any
			Error  map[string]any
		}
		json.Unmarshal([]byte(r.Body), &out)
		if out.Result == nil {
			t.Fatalf("%s: no result: %s", name, r.Body)
		}
		return out.Result
	}

	type c struct {
		name string
		args map[string]any
		fail bool // refused by design: an error result
	}
	cases := []c{
		{"who_am_i", nil, false},
		{"create_site", map[string]any{"site": "cafe", "files": map[string]any{"index.html": "menu"}}, false},
		{"create_site", map[string]any{"site": "cafe", "files": map[string]any{"index.html": "menu"}}, true},
		{"update_site", map[string]any{"site": "cafe", "files": map[string]any{"about.html": "us"}, "keep_other_files": true}, false},
		{"update_site", map[string]any{"site": "cafe", "files": map[string]any{"index.html": "x"}, "publish": false}, true},
		{"list_sites", nil, false},
		{"list_sites", map[string]any{"limit": 2}, false},
		{"list_sites", map[string]any{"query": "cafe"}, false},
		{"list_sites", map[string]any{"order": "name", "offset": 1, "limit": 1}, false},
		{"get_site", map[string]any{"site": "shop"}, false},
		{"read_site_file", map[string]any{"site": "shop", "path": "index.html"}, false},
		{"read_site_file", map[string]any{"site": "shop", "paths": []any{"index.html", "a.txt"}}, false},
		{"read_site_file", map[string]any{"site": "shop", "path": "index.html", "version": 1}, true},
		{"list_versions", map[string]any{"site": "shop"}, false},
		{"rollback_site", map[string]any{"site": "shop", "version": 1}, false},
		{"set_visibility", map[string]any{"site": "shop", "visibility": "public"}, false},
		{"set_site_offline", map[string]any{"site": "shop", "offline": false}, false},
		{"site_analytics", map[string]any{"site": "shop", "days": 7}, false},
		{"get_page_recipe", map[string]any{"topic": "menu", "site": "shop"}, false},
		{"create_upload_link", map[string]any{"site": "shop"}, false},
		{"create_upload_link", map[string]any{"site": "shop", "archive": true}, false},
		{"storage_set_resource", map[string]any{"site": "shop", "name": "menu", "body": map[string]any{"kind": "kv", "preset": "public"}}, false},
		{"storage_put_kv", map[string]any{"site": "shop", "name": "menu", "key": "today", "value": map[string]any{"soup": "leek"}}, false},
		{"storage_get_kv", map[string]any{"site": "shop", "name": "menu", "key": "today"}, false},
		{"storage_list_kv_keys", map[string]any{"site": "shop", "name": "menu"}, false},
		{"storage_delete_kv", map[string]any{"site": "shop", "name": "menu", "key": "today"}, false},
		{"storage_sql_execute", map[string]any{"site": "shop", "name": "orders", "sql": "CREATE TABLE orders (id INTEGER PRIMARY KEY, item TEXT)"}, false},
		{"storage_sql_execute", map[string]any{"site": "shop", "name": "orders", "sql": "INSERT INTO orders (item) VALUES (?)", "params": []any{"tea"}}, false},
		{"storage_sql_query", map[string]any{"site": "shop", "name": "orders", "sql": "SELECT * FROM orders"}, false},
		{"storage_sql_schema", map[string]any{"site": "shop", "name": "orders", "sql": ""}, false},
		{"storage_put_file", map[string]any{"site": "shop", "name": "photos", "path": "a.txt", "content_base64": "aGk=", "content_type": "text/plain"}, false},
		{"storage_list_file_objects", map[string]any{"site": "shop", "name": "photos"}, false},
		{"storage_delete_file", map[string]any{"site": "shop", "name": "photos", "path": "a.txt"}, false},
		{"storage_list_resources", map[string]any{"site": "shop"}, false},
		{"storage_get_usage", map[string]any{"site": "shop"}, false},
		{"set_site_access", map[string]any{"site": "shop", "access": "specific"}, false},
		{"grant_site_viewer", map[string]any{"site": "shop", "emails": []any{"viv@example.com"}}, false},
		{"list_site_viewers", map[string]any{"site": "shop"}, false},
		{"revoke_site_viewer", map[string]any{"site": "shop", "email": "viv@example.com"}, false},
		{"set_site_access", map[string]any{"site": "shop", "access": "anyone"}, false},
		{"set_site_passcode", map[string]any{"site": "shop", "passcode": "lemon-tart-42"}, false},
		{"get_site_passcode", map[string]any{"site": "shop"}, false},
		{"sign_out_site_visitors", map[string]any{"site": "shop"}, false},
		{"remove_site_passcode", map[string]any{"site": "shop"}, true},
		{"remove_site_passcode", map[string]any{"site": "shop", "confirm": true}, false},
		{"connect_domain", map[string]any{"site": "shop", "domain": "shopx.simple-host.app"}, false},
		{"domain_status", map[string]any{"site": "shop"}, false},
		{"remove_domain", map[string]any{"site": "shop", "confirm_domain": "shopx.simple-host.app"}, false},
		{"connect_domain", map[string]any{"site": "shop", "domain": "shop.example.com"}, false},
		{"domain_status", map[string]any{"site": "shop", "domain": "shop.example.com"}, false},
		{"remove_domain", map[string]any{"site": "shop", "confirm_domain": "shop.example.com"}, false},
		{"set_home_page", map[string]any{"site": "shop"}, false},
		{"export_site", map[string]any{"site": "shop"}, false},
		{"list_teams", nil, false},
		{"create_team", map[string]any{"name": "crew"}, false},
		{"list_team_members", map[string]any{"team": "crew"}, false},
		{"add_team_member", map[string]any{"team": "crew", "email": ben.Email}, false},
		{"remove_team_member", map[string]any{"team": "crew", "person": ben.ID}, false},
		{"answer_team_invitation", map[string]any{"invitation": "nope", "answer": "decline"}, true},
		{"delete_team", map[string]any{"team": "crew"}, false},
		{"find_tools", map[string]any{"query": "domain"}, false},
		{"delete_site", map[string]any{"site": "blog", "confirm_name": "blog"}, false},
		{"list_deleted_sites", nil, false},
		{"restore_site", map[string]any{"site": "blog"}, false},
	}
	for _, name := range []string{"preview_version", "discard_version", "keep_site", "set_keep_versions", "add_site_editor", "list_site_editors",
		"remove_site_editor", "rename_site", "visitor_read_site", "visitor_add_entry", "visitor_change_entry", "storage_visitor_emails",
		"list_notifications", "remove_notification", "cancel_team_invitation", "set_bio", "set_showcase_site", "storage_file_download_link",
		"storage_delete_resource"} {
		cases = append(cases, c{name, map[string]any{"site": "shop"}, true})
	}

	// Names both tool lists have keep the current fields, with the old ones
	// added: the current list has no output schemas, its screens read these
	// answers, and the old apps take extra fields (live, who_am_i passed
	// with ten fields its old schema did not list). Only the old fields'
	// presence and types are checked for them.
	shared := map[string]bool{"who_am_i": true, "list_sites": true, "get_site": true, "read_site_file": true,
		"create_upload_link": true, "delete_site": true, "set_site_access": true, "create_team": true, "delete_team": true}
	covered := map[string]bool{}
	for _, tc := range cases {
		schema, ok := schemas[tc.name]
		if !ok {
			t.Fatalf("%s: not in the old tool list", tc.name)
		}
		covered[tc.name] = true
		res := call(tc.name, tc.args)
		if tc.name == "add_team_member" { // ben accepts, so he can be removed
			r := testkit.Call(t, b, "GET", platform+"/v1/me", nil, "X-API-Key", ben.Key)
			var me struct{ Invitations []struct{ ID string } }
			json.Unmarshal([]byte(r.Body), &me)
			if len(me.Invitations) != 1 {
				t.Fatalf("ben's invitations: %s", r.Body)
			}
			testkit.Call(t, b, "POST", platform+"/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call",
				"params": map[string]any{"name": "accept_invite", "arguments": map[string]any{"id": me.Invitations[0].ID}}}, "X-API-Key", ben.Key)
		}
		text := fmt.Sprint(res["content"])
		if isErr := res["isError"] == true; isErr != tc.fail {
			t.Errorf("%s %v: isError %v, want %v: %s", tc.name, tc.args, isErr, tc.fail, text)
			continue
		}
		if tc.fail {
			if res["structuredContent"] != nil {
				t.Errorf("%s: an error result carries structuredContent, which the app validates: %v", tc.name, res["structuredContent"])
			}
			continue
		}
		got, _ := res["structuredContent"].(map[string]any)
		if got == nil {
			t.Errorf("%s %v: no structuredContent, which the old schema requires: %s", tc.name, tc.args, text)
			continue
		}
		errs := validate(schema, got, "data")
		if shared[tc.name] {
			errs = slices.DeleteFunc(errs, func(e string) bool { return strings.Contains(e, "additional property") })
		}
		if len(errs) > 0 {
			b, _ := json.Marshal(got)
			t.Errorf("%s %v does not match the old outputSchema:\n  %s\n  got %s", tc.name, tc.args, strings.Join(errs, "\n  "), b)
		}
		if tc.name == "list_sites" {
			sites, _ := got["sites"].([]any)
			if l, ok := tc.args["limit"].(int); ok && len(sites) > l {
				t.Errorf("list_sites %v: %d sites, over the limit", tc.args, len(sites))
			}
			if tc.args["query"] == "cafe" && len(sites) != 1 {
				t.Errorf("list_sites %v: %d sites, want cafe only", tc.args, len(sites))
			}
			if tc.args["order"] == "name" && (len(sites) != 1 || sites[0].(map[string]any)["name"] != "cafe") {
				t.Errorf("list_sites %v: %v, want cafe (blog, cafe, shop by name)", tc.args, sites)
			}
		}
	}
	missing := []string{}
	for name := range schemas {
		if !covered[name] {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("old tools with an outputSchema not checked: %v", missing)
	}
}

// validate is enough JSON Schema for the old output schemas: type (or a
// list of types), enum, properties, required, additionalProperties, items.
// It returns every mismatch, in Ajv's terms.
func validate(schema map[string]any, v any, at string) []string {
	var errs []string
	if t, ok := schema["type"]; ok {
		types := []any{t}
		if list, ok := t.([]any); ok {
			types = list
		}
		match := false
		for _, t := range types {
			match = match || typeIs(t.(string), v)
		}
		if !match {
			return []string{fmt.Sprintf("%s must be %v (is %T)", at, t, v)}
		}
	}
	if enum, ok := schema["enum"].([]any); ok {
		in := false
		for _, e := range enum {
			in = in || reflect.DeepEqual(e, v)
		}
		if !in {
			errs = append(errs, fmt.Sprintf("%s must be one of %v (is %v)", at, enum, v))
		}
	}
	switch x := v.(type) {
	case map[string]any:
		props, _ := schema["properties"].(map[string]any)
		req, _ := schema["required"].([]any)
		for _, r := range req {
			if _, ok := x[r.(string)]; !ok {
				errs = append(errs, fmt.Sprintf("%s must have required property '%s'", at, r))
			}
		}
		keys := make([]string, 0, len(x))
		for key := range x {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if sub, ok := props[key].(map[string]any); ok {
				errs = append(errs, validate(sub, x[key], at+"/"+key)...)
				continue
			}
			switch extra := schema["additionalProperties"].(type) {
			case bool:
				if !extra {
					errs = append(errs, fmt.Sprintf("%s must NOT have additional property '%s'", at, key))
				}
			case map[string]any:
				errs = append(errs, validate(extra, x[key], at+"/"+key)...)
			}
		}
	case []any:
		if items, ok := schema["items"].(map[string]any); ok {
			for i, el := range x {
				errs = append(errs, validate(items, el, fmt.Sprintf("%s/%d", at, i))...)
			}
		}
	}
	return errs
}

func typeIs(t string, v any) bool {
	switch t {
	case "object":
		_, ok := v.(map[string]any)
		return ok
	case "array":
		_, ok := v.([]any)
		return ok
	case "string":
		_, ok := v.(string)
		return ok
	case "number":
		_, ok := v.(float64)
		return ok
	case "integer":
		f, ok := v.(float64)
		return ok && f == math.Trunc(f)
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "null":
		return v == nil
	}
	return false
}
