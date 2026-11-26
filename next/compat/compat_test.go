package compat_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
	"simplehost/sites/manage"
	"simplehost/sites/publish"
	"simplehost/sites/serve"
	"simplehost/sites/uploads"
	"simplehost/web"
)

const platform = "https://simple-host.app"

// start is the service with compat in front, as main serves it, and a
// client that reaches it at any address.
func start(t *testing.T) (*testkit.Kit, *http.Client) {
	k := testkit.Start(t, publish.Mount, manage.Mount, serve.Mount, rows.Mount, values.Mount, datasql.Mount, datarules.Mount, recipes.Mount, api.Mount, mcp.Mount, web.Mount, uploads.Mount, files.Mount)
	srv := httptest.NewServer(compat.Wrap(k.App, k.App))
	t.Cleanup(srv.Close)
	return k, &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		r = r.Clone(r.Context())
		r.Header.Set("X-Forwarded-Proto", "https")
		r.Host, r.URL.Scheme, r.URL.Host = r.URL.Host, "http", strings.TrimPrefix(srv.URL, "http://")
		return http.DefaultTransport.RoundTrip(r)
	})}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// A script publishing the old way gets the old fields, the notice headers,
// keeps its other files with ?keep=1, and the operator sees it counted.
func TestOldPublishIsTranslatedAndCounted(t *testing.T) {
	k, b := start(t)
	bob := k.Person(t, "bob")
	r := testkit.Call(t, b, "POST", platform+"/v1/sites/blog/files", map[string]any{"files": map[string]string{"index.html": "hi", "about.html": "me"}},
		"X-API-Key", bob.Key, "Content-Type", "application/json")
	var out map[string]any
	json.Unmarshal([]byte(r.Body), &out)
	if r.Status != 201 || !strings.Contains(out["site_url"].(string), "blog") || out["active_version"] != 1.0 {
		t.Fatalf("publish: %d %s", r.Status, r.Body)
	}
	if r.Header.Get("Deprecation") != "true" || r.Header.Get("Sunset") == "" || !strings.Contains(r.Header.Get("Link"), "/llms.txt") {
		t.Fatalf("notice headers: %v", r.Header)
	}
	r = testkit.Call(t, b, "PUT", platform+"/v1/sites/blog/files?keep=1", map[string]any{"files": map[string]string{"index.html": "hello"}},
		"X-API-Key", bob.Key, "Content-Type", "application/json")
	if r.Status != 200 || !strings.Contains(r.Body, `"active_version":2`) {
		t.Fatalf("keep: %d %s", r.Status, r.Body)
	}
	if r := testkit.Call(t, b, "GET", platform+"/v1/sites/blog/files/about.html", nil, "X-API-Key", bob.Key); r.Status != 200 || r.Body != "me" {
		t.Fatalf("kept file: %d %s", r.Status, r.Body)
	}
	op := k.Person(t, "op")
	k.Exec(t, `UPDATE people SET operator = true WHERE id = $1`, op.ID)
	r = testkit.Call(t, b, "GET", platform+"/v1/admin/compat", nil, "X-API-Key", op.Key)
	if r.Status != 200 || !strings.Contains(r.Body, `"handle":"bob"`) || !strings.Contains(r.Body, `"name":"PUT /v1/sites/{site}/files","calls":1`) {
		t.Fatalf("count: %d %s", r.Status, r.Body)
	}
}

// An installed old skill that reads itself again gets the new skill, and
// learns it is out of date once the notice is on.
func TestOldSkillGetsTheNewSkill(t *testing.T) {
	t.Setenv("SH_COMPAT_NOTICE", "on")
	_, b := start(t)
	r := testkit.Call(t, b, "GET", platform+"/v1/skills/website-deploy/SKILL.md", nil, "X-Skill-Version", "0.27.119")
	if r.Status != 200 || !strings.Contains(r.Body, "name: simple-host") {
		t.Fatalf("skill: %d %s", r.Status, r.Body)
	}
	r = testkit.Call(t, b, "GET", platform+"/v1/skills", nil, "X-Skill-Version", "0.27.119")
	if !strings.Contains(r.Header.Get("X-Skill-Notice"), "/skills.zip") || !strings.Contains(r.Body, `"_notice"`) {
		t.Fatalf("notice: %v %s", r.Header, r.Body)
	}
}

// An AI app holding the old tool list publishes with the old names; with
// the notice on, only its first answer says to reconnect.
func TestOldToolNamesStillWork(t *testing.T) {
	t.Setenv("SH_COMPAT_NOTICE", "on")
	k, _ := start(t)
	nina := k.Person(t, "nina")
	b := k.Browser()
	call := func(name string, args map[string]any) string {
		t.Helper()
		r := testkit.Call(t, b, "POST", platform+"/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call",
			"params": map[string]any{"name": name, "arguments": args}}, "X-API-Key", nina.Key)
		return r.Body
	}
	if s := call("create_site", map[string]any{"site": "cafe", "files": map[string]any{"index.html": "menu"}}); !strings.Contains(s, "Simple Host has new tools") || strings.Contains(s, `"isError":true`) {
		t.Fatalf("create_site: %s", s)
	}
	if s := call("create_site", map[string]any{"site": "cafe", "files": map[string]any{"index.html": "menu"}}); !strings.Contains(s, "site_exists") {
		t.Fatalf("create_site again: %s", s)
	}
	if s := call("run_tool", map[string]any{"name": "get_site", "arguments": map[string]any{"site": "cafe"}}); !strings.Contains(s, "cafe--nina") || strings.Contains(s, "new tools") {
		t.Fatalf("run_tool: %s", s)
	}
	if s := call("rename_site", map[string]any{"site": "cafe"}); !strings.Contains(s, "was removed") {
		t.Fatalf("dropped tool: %s", s)
	}
}

// Old arguments translate: presets become rules, the old access words,
// several paths at once, old recipe topics, params as JSON text; a draft
// (publish: false) is refused, not published.
func TestOldArgumentsTranslate(t *testing.T) {
	k, _ := start(t)
	ann := k.Person(t, "ann")
	b := k.Browser()
	call := func(name string, args map[string]any) string {
		t.Helper()
		return testkit.Call(t, b, "POST", platform+"/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call",
			"params": map[string]any{"name": name, "arguments": args}}, "X-API-Key", ann.Key).Body
	}
	k.Publish(t, ann, "shop", map[string]string{"index.html": "hi", "a.txt": "aaa"})
	for _, c := range []struct {
		name string
		args map[string]any
		want string
	}{
		{"storage_set_resource", map[string]any{"site": "shop", "name": "menu", "body": map[string]any{"kind": "kv", "preset": "public"}}, `using\":\"true`},
		{"storage_set_resource", map[string]any{"site": "shop", "name": "orders", "body": map[string]any{"kind": "sqlite", "preset": "records"}}, "rules_per_table"},
		{"set_site_access", map[string]any{"site": "shop", "access": "specific"}, `access\":\"viewers`},
		{"read_site_file", map[string]any{"site": "shop", "paths": []any{"index.html", "a.txt"}}, `"text":"aaa"`},
		{"get_page_recipe", map[string]any{"topic": "menu"}, "Public"},
		{"storage_sql_query", map[string]any{"site": "shop", "name": "menu", "sql": "SELECT ?", "params": `[7]`}, `[[7]]`},
		{"update_site", map[string]any{"site": "shop", "files": map[string]any{"index.html": "draft"}, "publish": false}, "Nothing was published"},
		{"create_upload_link", map[string]any{"site": "shop"}, `"photos_url":"https://simple-host.app/upload/up_`},
	} {
		if s := call(c.name, c.args); !strings.Contains(s, c.want) || strings.Contains(s, "new tools") {
			t.Fatalf("%s: want %s in %s", c.name, c.want, s)
		}
	}
	k.Publish(t, ann, "cafe", map[string]string{"index.html": "menu"}) // shop is viewers-only by now
	call("create_upload_link", map[string]any{"site": "cafe"})
	if r := testkit.Call(t, b, "GET", "https://cafe--ann.simple-host.site/v1/sites/cafe/storage/files/photos/objects", nil); r.Status != 200 {
		t.Fatalf("visitors cannot see the photos: %d %s", r.Status, r.Body)
	}
	if r := testkit.Call(t, b, "GET", platform+"/v1/sites/shop/files/index.html", nil, "X-API-Key", ann.Key); r.Body != "hi" {
		t.Fatalf("draft went live: %s", r.Body)
	}
}

// An old page's push call gets a plain refusal, not a 500 or a notice.
func TestOldPagePushIsRefusedPlainly(t *testing.T) {
	k, b := start(t)
	ann := k.Person(t, "ann")
	k.Publish(t, ann, "shop", map[string]string{"index.html": "hi"})
	r := testkit.Call(t, b, "POST", "https://shop--ann.simple-host.site/v1/sites/shop/push/subscribe", map[string]any{"endpoint": "x"}, "X-SH-CSRF", "1")
	if r.Status != 410 || !strings.Contains(r.Body, `"code":"not_available"`) || r.Header.Get("Deprecation") != "" {
		t.Fatalf("push: %d %v %s", r.Status, r.Header, r.Body)
	}
}

// After the sunset, compat steps aside: old addresses get the service's
// "changed" refusal and old tool names are unknown.
func TestAfterSunsetTheChangedRefusal(t *testing.T) {
	t.Setenv("SH_COMPAT_UNTIL", "2020-01-01")
	k, b := start(t)
	bob := k.Person(t, "bob")
	r := testkit.Call(t, b, "POST", platform+"/v1/sites/blog/files", map[string]any{"files": map[string]string{"index.html": "hi"}}, "X-API-Key", bob.Key)
	if r.Status != 405 || !strings.Contains(r.Body, `"code":"changed"`) || r.Header.Get("Deprecation") != "" {
		t.Fatalf("after sunset: %d %s", r.Status, r.Body)
	}
}

// The layer is throwaway: two weeks past the sunset in the repository, this
// fails until the package is deleted.
func TestDeleteNextCompatTwoWeeksAfterSunset(t *testing.T) {
	t.Setenv("SH_COMPAT_UNTIL", "")
	if time.Now().After(compat.Until().Add(14 * 24 * time.Hour)) {
		t.Fatal("delete next/compat: the sunset was " + compat.Until().Add(-24*time.Hour).Format(time.DateOnly) + " (see next/compat/README.md)")
	}
}

// Yesterday's data (method + path) and passcode_screen (action) reach the
// single-purpose tools they were split into.
func TestSplitToolsKeepTheirOldNames(t *testing.T) {
	k, _ := start(t)
	ann := k.Person(t, "ann")
	k.Publish(t, ann, "shop", map[string]string{"index.html": "hi"})
	b := k.Browser()
	call := func(name string, args map[string]any) string {
		t.Helper()
		r := testkit.Call(t, b, "POST", platform+"/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call",
			"params": map[string]any{"name": name, "arguments": args}}, "X-API-Key", ann.Key)
		return r.Body
	}
	call("run_sql", map[string]any{"site": "shop", "namespace": "orders", "sql": "CREATE TABLE orders (id INTEGER PRIMARY KEY, item TEXT)"})
	rows := "storage/sqlite/orders/tables/orders/rows"
	if s := call("data", map[string]any{"site": "shop", "method": "POST", "path": rows, "body": map[string]any{"item": "tea"}}); !strings.Contains(s, `"last_insert_id":1`) {
		t.Fatalf("data POST rows: %s", s)
	}
	if s := call("data", map[string]any{"site": "shop", "path": rows + "?where.item=tea"}); !strings.Contains(s, "tea") || strings.Contains(s, "isError") {
		t.Fatalf("data GET rows: %s", s)
	}
	if s := call("data", map[string]any{"site": "shop", "method": "PUT", "path": "storage/kv/orders/keys/today", "body": map[string]any{"value": "soup"}}); !strings.Contains(s, `"value":"soup"`) {
		t.Fatalf("data PUT value: %s", s)
	}
	if s := call("data", map[string]any{"site": "shop", "method": "PUT", "path": "storage/files/orders/objects/a.txt", "body_base64": "aGk=", "type": "text/plain"}); !strings.Contains(s, `"bytes":2`) {
		t.Fatalf("data PUT file: %s", s)
	}
	if s := call("data", map[string]any{"site": "shop", "method": "DELETE", "path": rows + "/1"}); !strings.Contains(s, `"deleted":true`) {
		t.Fatalf("data DELETE row: %s", s)
	}
	if s := call("data", map[string]any{"site": "shop", "path": "elsewhere/x"}); !strings.Contains(s, "bad_path") {
		t.Fatalf("data elsewhere: %s", s)
	}
	if s := call("passcode_screen", map[string]any{"site": "shop", "action": "begin"}); !strings.Contains(s, "server_key") || !strings.Contains(s, `"passcode_protected":false`) {
		t.Fatalf("passcode_screen begin: %s", s)
	}
	// Yesterday's set_site_access and change_site reach the access and site tools.
	if s := call("set_site_access", map[string]any{"site": "shop", "access": "viewers", "viewers": []any{"viv@example.com"}, "sign_everyone_out": true}); !strings.Contains(s, `\"access\":\"viewers\"`) || strings.Contains(s, "isError") {
		t.Fatalf("set_site_access viewers: %s", s)
	}
	if s := call("set_site_access", map[string]any{"site": "shop", "viewers_remove": []any{"viv@example.com"}}); !strings.Contains(s, `\"viewer_count\":0`) {
		t.Fatalf("set_site_access viewers_remove: %s", s)
	}
	if s := call("set_site_access", map[string]any{"site": "shop", "access": "passcode", "passcode": "in-the-chat-1"}); !strings.Contains(s, "passcode screen") || strings.Contains(s, "in-the-chat-1") {
		t.Fatalf("set_site_access passcode: %s", s)
	}
	if s := call("set_site_access", map[string]any{"site": "shop", "access": "open"}); !strings.Contains(s, `\"access\":\"open\"`) {
		t.Fatalf("set_site_access open: %s", s)
	}
	if s := call("change_site", map[string]any{"site": "shop", "offline": true, "listed": true}); !strings.Contains(s, `\"offline\":true`) || !strings.Contains(s, `\"listed\":true`) {
		t.Fatalf("change_site offline listed: %s", s)
	}
	if s := call("change_site", map[string]any{"site": "shop", "offline": false, "live_version": 1}); !strings.Contains(s, `\"offline\":false`) || strings.Contains(s, "isError") {
		t.Fatalf("change_site online rollback: %s", s)
	}
}
