package mcp_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"simplehost/api"
	"simplehost/core/testkit"
	"simplehost/data/rows"
	datarules "simplehost/data/rules"
	datasql "simplehost/data/sql"
	"simplehost/mcp"
	"simplehost/sites/serve"
)

// ai is a person's AI app: MCP requests with their credential.
func ai(t *testing.T, k *testkit.Kit, key string) func(method string, params any) map[string]any {
	b := k.Browser()
	return func(method string, params any) map[string]any {
		t.Helper()
		r := testkit.Call(t, b, "POST", "https://simple-host.app/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params},
			"Authorization", "Bearer "+key)
		var out struct {
			Result map[string]any
			Error  map[string]any
		}
		if json.Unmarshal([]byte(r.Body), &out); r.Status != 200 || out.Error != nil {
			t.Fatalf("%s: %d %s", method, r.Status, r.Body)
		}
		return out.Result
	}
}

func tool(call func(string, any) map[string]any, name string, args map[string]any) map[string]any {
	return call("tools/call", map[string]any{"name": name, "arguments": args})
}

func text(res any) string { b, _ := json.Marshal(res); return string(b) }

// SPEC test 27 (F19): the in-chat screen sets the passcode without it
// appearing in the tool result (nor in what AgentCat receives).
func TestPasscodeScreenKeepsTheCodeOutOfTheChat(t *testing.T) {
	var mu sync.Mutex
	var sent []string
	agentcat := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		sent = append(sent, string(b))
		mu.Unlock()
	}))
	defer agentcat.Close()
	k := testkit.Start(t, mcp.Mount, api.Mount, serve.Mount)
	k.App.Config.AgentCatURL, k.App.Config.AgentCatProject = agentcat.URL, "test"
	nina := k.Person(t, "nina")
	k.Publish(t, nina, "blog", map[string]string{"index.html": "hello"})
	call := ai(t, k, nina.Key)
	page := func() int {
		return testkit.Call(t, k.Browser(), "GET", "https://blog--nina.simple-host.site/", nil).Status
	}

	if v := call("initialize", map[string]any{"protocolVersion": "2025-06-18"}); v["serverInfo"] == nil {
		t.Fatalf("initialize: %v", v)
	}
	list := call("tools/list", nil)["tools"].([]any)
	if len(list) != 26 {
		t.Fatalf("%d tools, want 26", len(list))
	}
	for _, x := range list {
		if x := x.(map[string]any); x["name"] == "set_site_access" && !strings.Contains(text(x), "ui://simple-host/passcode") {
			t.Fatalf("set_site_access names no passcode screen: %v", x)
		}
	}
	screen := call("resources/read", map[string]any{"uri": "ui://simple-host/passcode"})
	if !strings.Contains(text(screen), "Set passcode") {
		t.Fatalf("passcode screen: %v", screen)
	}

	// The AI asks for a passcode: the screen opens, nothing is locked yet.
	if res := tool(call, "set_site_access", map[string]any{"site": "blog", "access": "passcode"}); res["isError"] == true || page() != 200 {
		t.Fatalf("asking for the screen: %v, page %d", res, page())
	}
	// The screen sets it: the result never carries the code.
	const code = "tulip-garden-7"
	res := tool(call, "set_site_access", map[string]any{"site": "blog", "access": "passcode", "passcode": code})
	if res["isError"] == true || strings.Contains(text(res), code) || page() != 401 {
		t.Fatalf("set from the screen: %v, page %d", res, page())
	}
	// Shown again only to the screen, in _meta.
	res = tool(call, "get_site", map[string]any{"site": "https://blog--nina.simple-host.site/"})
	meta, _ := res["_meta"].(map[string]any)
	if strings.Contains(text(res["content"]), code) || strings.Contains(text(res["structuredContent"]), code) || meta["passcode"] != code {
		t.Fatalf("get_site: %v", res)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		mu.Lock()
		all := strings.Join(sent, "\n")
		n := len(sent)
		mu.Unlock()
		if n >= 3 {
			if strings.Contains(all, code) || !strings.Contains(all, `"resource_name":"set_site_access"`) {
				t.Fatalf("AgentCat got: %s", all)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("AgentCat got %d calls", n)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// SPEC test 33 (F24): a person's AI reads and adds to a site shared with
// them, with the same refusals as their browser.
func TestMyAIUsesASiteSharedWithMe(t *testing.T) {
	k := testkit.Start(t, mcp.Mount, api.Mount, serve.Mount, rows.Mount, datasql.Mount, datarules.Mount)
	alice, bob, carol := k.Person(t, "alice"), k.Person(t, "bob"), k.Person(t, "carol")
	k.Publish(t, alice, "club", map[string]string{"index.html": "club"})
	owner := func(method, path string, body any) {
		if r := testkit.Call(t, k.Browser(), method, "https://simple-host.app/v1/sites/club"+path, body, "Authorization", "Bearer "+alice.Key); r.Status != 200 {
			t.Fatalf("%s %s: %d %s", method, path, r.Status, r.Body)
		}
	}
	owner("POST", "/storage/sqlite/club/execute", map[string]any{"sql": "CREATE TABLE posts (id INTEGER PRIMARY KEY, body TEXT, visitor_id TEXT, created_at TEXT)"})
	owner("PUT", "/storage/club/rules", `{"rules": {"posts": {"version": 1, "policies": [{"name": "read", "command": "read", "using": "true"}, {"name": "add", "command": "add", "check": "auth.signed_in"}]}}}`)
	owner("PATCH", "", map[string]any{"viewers": []string{bob.Email}})

	addr := "https://club--alice.simple-host.site/"
	rowsPath := "storage/sqlite/club/tables/posts/rows"
	data := func(key, method, path string, body any) map[string]any {
		return tool(ai(t, k, key), "data", map[string]any{"site": addr, "method": method, "path": path, "body": body})
	}
	browser := func(who testkit.Who, method, path string, body any) testkit.Res {
		b := k.Browser()
		k.SignIn(t, b, who, addr)
		return testkit.Call(t, b, method, addr+"v1/sites/club/"+path, body, "X-SH-CSRF", "1")
	}
	refused := func(res map[string]any) string {
		if res["isError"] != true {
			t.Fatalf("not refused: %v", res)
		}
		return res["structuredContent"].(map[string]any)["code"].(string)
	}

	// Bob is named: his AI reads and adds, as himself.
	if res := data(bob.Key, "POST", rowsPath, map[string]any{"body": "hi from bob's AI"}); res["isError"] == true {
		t.Fatalf("add: %v", res)
	}
	if res := data(bob.Key, "GET", rowsPath, nil); !strings.Contains(text(res), "hi from bob's AI") || !strings.Contains(text(res), bob.ID) {
		t.Fatalf("read: %v", res)
	}
	// The same refusals as his browser: no rule lets him delete.
	code := refused(data(bob.Key, "DELETE", rowsPath+"/1", nil))
	if r := browser(bob, "DELETE", rowsPath+"/1", nil); r.Status != 403 || !strings.Contains(r.Body, `"`+code+`"`) {
		t.Fatalf("browser delete: %d %s, AI got %s", r.Status, r.Body, code)
	}
	// Carol is not named: the same "ask for access" both ways.
	code = refused(data(carol.Key, "GET", rowsPath, nil))
	if r := browser(carol, "GET", rowsPath, nil); code != "ask_access" || r.Status != 403 || !strings.Contains(r.Body, code) {
		t.Fatalf("carol: AI %s, browser %d %s", code, r.Status, r.Body)
	}
	// get_site by address: Bob sees the address, not the owner's settings.
	res := tool(ai(t, k, bob.Key), "get_site", map[string]any{"site": addr})
	if s := text(res); !strings.Contains(s, addr) || strings.Contains(s, "viewers") {
		t.Fatalf("get_site for bob: %s", s)
	}
}
