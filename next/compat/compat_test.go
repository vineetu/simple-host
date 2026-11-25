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
	"simplehost/mcp"
	"simplehost/sites/manage"
	"simplehost/sites/publish"
	"simplehost/sites/serve"
	"simplehost/web"
)

const platform = "https://simple-host.app"

// start is the service with compat in front, as main serves it, and a
// client that reaches it at any address.
func start(t *testing.T) (*testkit.Kit, *http.Client) {
	k := testkit.Start(t, publish.Mount, api.Mount, manage.Mount, serve.Mount, mcp.Mount, web.Mount)
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
// learns it is out of date.
func TestOldSkillGetsTheNewSkill(t *testing.T) {
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

// An AI app holding the old tool list publishes with the old names, and
// each answer says to reconnect.
func TestOldToolNamesStillWork(t *testing.T) {
	k, _ := start(t)
	nina := k.Person(t, "nina")
	b := k.Browser()
	call := func(name string, args map[string]any) string {
		t.Helper()
		r := testkit.Call(t, b, "POST", platform+"/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call",
			"params": map[string]any{"name": name, "arguments": args}}, "X-API-Key", nina.Key)
		return r.Body
	}
	if s := call("create_site", map[string]any{"site": "cafe", "files": map[string]any{"index.html": "menu"}}); !strings.Contains(s, "old tool list") || strings.Contains(s, `"isError":true`) {
		t.Fatalf("create_site: %s", s)
	}
	if s := call("run_tool", map[string]any{"name": "get_site", "arguments": map[string]any{"site": "cafe"}}); !strings.Contains(s, "cafe--nina") {
		t.Fatalf("run_tool: %s", s)
	}
	if s := call("rename_site", map[string]any{"site": "cafe"}); !strings.Contains(s, "was removed") {
		t.Fatalf("dropped tool: %s", s)
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
