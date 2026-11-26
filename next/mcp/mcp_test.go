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
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
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
			"X-API-Key", key)
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
	if len(list) != 51 { // 47 for the model, 4 for the passcode screen; the operator sees 53 (admin_overview, set_hold)
		t.Fatalf("%d tools, want 51", len(list))
	}
	for _, x := range list {
		if x := x.(map[string]any); x["name"] == "require_passcode" && !strings.Contains(text(x), "ui://simple-host/passcode") {
			t.Fatalf("require_passcode names no passcode screen: %v", x)
		}
	}
	if n := len(call("resources/list", nil)["resources"].([]any)); n != 4 {
		t.Fatalf("%d screens, want 4", n)
	}
	screen := call("resources/read", map[string]any{"uri": "ui://simple-host/passcode"})
	if s := text(screen); !strings.Contains(s, "Set passcode") || strings.Contains(s, "__") {
		t.Fatalf("passcode screen: %v", screen)
	}

	// The AI asks for a passcode: the screen opens, nothing is locked yet.
	if res := tool(call, "require_passcode", map[string]any{"site": "blog"}); res["isError"] == true || page() != 200 {
		t.Fatalf("asking for the screen: %v, page %d", res, page())
	}
	// The screen sets it, sealed: no result carries the code.
	private, _ := ecdh.P256().GenerateKey(rand.Reader)
	session := base64.RawURLEncoding.EncodeToString(private.PublicKey().Bytes())
	begin := tool(call, "passcode_screen_open", map[string]any{"site": "blog", "client_key": session})["structuredContent"].(map[string]any)
	serverKey, _ := base64.RawURLEncoding.DecodeString(begin["server_key"].(string))
	public, _ := ecdh.P256().NewPublicKey(serverKey)
	shared, _ := private.ECDH(public)
	key, _ := hkdf.Key(sha256.New, shared, nil, "simple-host passcode screen v1", 32)
	block, _ := aes.NewCipher(key)
	aead, _ := cipher.NewGCM(block)
	const code = "tulip-garden-7"
	nonce := make([]byte, 12)
	sealed := aead.Seal(nil, nonce, []byte(code), []byte("set|blog|"+session))
	res := tool(call, "passcode_screen_set", map[string]any{"site": "blog", "session": session,
		"nonce": base64.RawURLEncoding.EncodeToString(nonce), "ciphertext": base64.RawURLEncoding.EncodeToString(sealed)})
	if res["isError"] == true || strings.Contains(text(res), code) || page() != 401 {
		t.Fatalf("set from the screen: %v, page %d", res, page())
	}
	// Shown again only to the screen, sealed; get_site never carries it.
	res = tool(call, "passcode_screen_show", map[string]any{"site": "blog", "session": session})
	box := res["structuredContent"].(map[string]any)["sealed"].(map[string]any)
	n, _ := base64.RawURLEncoding.DecodeString(box["nonce"].(string))
	c, _ := base64.RawURLEncoding.DecodeString(box["ciphertext"].(string))
	if shown, err := aead.Open(nil, n, c, []byte("show|blog|"+session)); err != nil || string(shown) != code || strings.Contains(text(res), code) {
		t.Fatalf("show: %v %q", res, shown)
	}
	if res = tool(call, "get_site", map[string]any{"site": "https://blog--nina.simple-host.site/"}); strings.Contains(text(res), code) {
		t.Fatalf("get_site: %v", res)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		mu.Lock()
		all := strings.Join(sent, "\n")
		n := len(sent)
		mu.Unlock()
		if n >= 3 {
			if strings.Contains(all, code) || !strings.Contains(all, `"resource_name":"require_passcode"`) {
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
		if r := testkit.Call(t, k.Browser(), method, "https://simple-host.app/v1/sites/club"+path, body, "X-API-Key", alice.Key); r.Status != 200 {
			t.Fatalf("%s %s: %d %s", method, path, r.Status, r.Body)
		}
	}
	owner("POST", "/storage/sqlite/club/execute", map[string]any{"sql": "CREATE TABLE posts (id INTEGER PRIMARY KEY, body TEXT, visitor_id TEXT, created_at TEXT)"})
	owner("PUT", "/storage/club/rules", `{"rules": {"posts": {"version": 1, "policies": [{"name": "read", "command": "read", "using": "true"}, {"name": "add", "command": "add", "check": "auth.signed_in"}]}}}`)
	owner("PATCH", "", map[string]any{"viewers": []string{bob.Email}})

	addr := "https://club--alice.simple-host.site/"
	rowsPath := "storage/sqlite/club/tables/posts/rows"
	data := func(key, name string, args map[string]any) map[string]any {
		a := map[string]any{"site": addr, "namespace": "club", "table": "posts"}
		for k, v := range args {
			a[k] = v
		}
		return tool(ai(t, k, key), name, a)
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
		code, _, _ := strings.Cut(res["content"].([]any)[0].(map[string]any)["text"].(string), ": ")
		return code
	}

	// Bob is named: his AI reads and adds, as himself.
	if res := data(bob.Key, "add_row", map[string]any{"row": map[string]any{"body": "hi from bob's AI"}}); res["isError"] == true {
		t.Fatalf("add: %v", res)
	}
	if res := data(bob.Key, "list_rows", nil); !strings.Contains(text(res), "hi from bob's AI") ||
		strings.Contains(text(res), bob.ID) || !strings.Contains(text(res), `"you"`) || !strings.Contains(text(res), `"mine":[true]`) {
		t.Fatalf("read: %v", res)
	}
	// The same refusals as his browser: no rule lets him delete.
	code := refused(data(bob.Key, "delete_row", map[string]any{"id": 1}))
	if r := browser(bob, "DELETE", rowsPath+"/1", nil); r.Status != 403 || !strings.Contains(r.Body, `"`+code+`"`) {
		t.Fatalf("browser delete: %d %s, AI got %s", r.Status, r.Body, code)
	}
	// Carol is not named: the same "ask for access" both ways.
	code = refused(data(carol.Key, "list_rows", nil))
	if r := browser(carol, "GET", rowsPath, nil); code != "ask_access" || r.Status != 403 || !strings.Contains(r.Body, code) {
		t.Fatalf("carol: AI %s, browser %d %s", code, r.Status, r.Body)
	}
	// get_site by address: Bob sees the address, not the owner's settings.
	res := tool(ai(t, k, bob.Key), "get_site", map[string]any{"site": addr})
	if s := text(res); !strings.Contains(s, addr) || strings.Contains(s, "viewers") {
		t.Fatalf("get_site for bob: %s", s)
	}
}

// The operator's tools are listed and answered for the operator only.
func TestOperatorToolsAreForTheOperatorOnly(t *testing.T) {
	k := testkit.Start(t, mcp.Mount, api.Mount)
	user, op := k.Person(t, "plain"), k.Person(t, "boss")
	k.Exec(t, `UPDATE people SET operator = true WHERE id = $1`, op.ID)
	names := func(call func(string, any) map[string]any) string {
		var out []string
		for _, x := range call("tools/list", nil)["tools"].([]any) {
			out = append(out, x.(map[string]any)["name"].(string))
		}
		return strings.Join(out, " ")
	}
	u, o := names(ai(t, k, user.Key)), names(ai(t, k, op.Key))
	if strings.Contains(u, "admin_overview") || strings.Contains(u, "set_hold") {
		t.Fatalf("ordinary user's list has operator tools: %s", u)
	}
	if !strings.Contains(o, "admin_overview") || !strings.Contains(o, "set_hold") {
		t.Fatalf("operator's list lacks them: %s", o)
	}
	r := testkit.Call(t, k.Browser(), "POST", "https://simple-host.app/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "admin_overview", "arguments": map[string]any{}}}, "X-API-Key", user.Key)
	if !strings.Contains(r.Body, "No tool named admin_overview") {
		t.Fatalf("ordinary user's call: %s", r.Body)
	}
}

// A person in stored data shows by one label on the site, wherever the id
// lands in an answer, and the label works as the id in a call.
func TestVisitorLabels(t *testing.T) {
	k := testkit.Start(t, mcp.Mount, api.Mount, rows.Mount, datasql.Mount)
	alice, bob, cara := k.Person(t, "alice"), k.Person(t, "bob"), k.Person(t, "cara")
	k.Publish(t, alice, "party", map[string]string{"index.html": "party"})
	call := ai(t, k, alice.Key)
	run := func(name string, args map[string]any) map[string]any {
		a := map[string]any{"site": "party", "namespace": "party"}
		for k, v := range args {
			a[k] = v
		}
		res := tool(call, name, a)
		if res["isError"] == true || uuid.MatchString(strings.ToLower(text(res))) {
			t.Fatalf("%s %v: %s", name, args, text(res))
		}
		sc, _ := res["structuredContent"].(map[string]any)
		return sc
	}
	for _, sql := range []string{"CREATE TABLE bookings (id INTEGER PRIMARY KEY, name TEXT, visitor_id TEXT)", "CREATE TABLE guests (id INTEGER PRIMARY KEY, visitor_id TEXT)"} {
		run("run_sql", map[string]any{"sql": sql})
	}
	for _, p := range [][]any{{"Bob", bob.ID}, {"Cara", cara.ID}, {"Bob", bob.ID}, {"Ann", alice.ID}} {
		run("run_sql", map[string]any{"sql": "INSERT INTO bookings (name, visitor_id) VALUES (?, ?)", "params": p})
		run("run_sql", map[string]any{"sql": "INSERT INTO guests (visitor_id) VALUES (?)", "params": p[1:]})
	}
	cells := func(sql string, params ...any) []any {
		var out []any
		for _, r := range run("query_sql", map[string]any{"sql": sql, "params": params})["rows"].([]any) {
			out = append(out, r.([]any)...)
		}
		return out
	}
	label := regexp.MustCompile(`^visitor-[0-9a-f]{8}$`)
	bobs, caras := cells("SELECT visitor_id AS who FROM bookings WHERE name = 'Bob' LIMIT 1")[0], cells("SELECT visitor_id FROM bookings WHERE name = 'Cara'")[0]
	if !label.MatchString(fmt.Sprint(bobs)) || bobs == caras {
		t.Fatalf("bob %v, cara %v", bobs, caras)
	}
	for _, c := range []struct {
		sql  string
		want []any
	}{
		{"SELECT b.visitor_id, g.visitor_id FROM bookings b JOIN guests g ON g.visitor_id = b.visitor_id WHERE b.id = 2 AND g.id = 2", []any{caras, caras}},
		{"SELECT * FROM bookings b JOIN guests g ON g.visitor_id = b.visitor_id WHERE b.id = 1 AND g.id = 1", []any{1.0, "Bob", bobs, 1.0, bobs}},
		{"SELECT upper(visitor_id), visitor_id || '' FROM bookings WHERE name = 'Bob' LIMIT 1", []any{bobs, bobs}},
		{"SELECT group_concat(visitor_id) FROM (SELECT visitor_id FROM bookings ORDER BY id)", []any{fmt.Sprintf("%s,%s,%s,you", bobs, caras, bobs)}},
		{"SELECT json_object('v', visitor_id) FROM bookings WHERE id = 4", []any{`{"v":"you"}`}},
		{"SELECT name FROM bookings WHERE visitor_id = '" + fmt.Sprint(caras) + "'", []any{"Cara"}},
		{"SELECT name FROM bookings WHERE visitor_id = 'you'", []any{"Ann"}},
		{"SELECT name FROM bookings WHERE visitor_id = 'visitor-00000000'", nil},
	} {
		if got := cells(c.sql); fmt.Sprint(got) != fmt.Sprint(c.want) {
			t.Errorf("%s: got %v, want %v", c.sql, got, c.want)
		}
	}
	if got := cells("SELECT count(*) FROM bookings WHERE visitor_id = ?", bobs); fmt.Sprint(got) != "[2]" {
		t.Errorf("params by label: %v", got)
	}
	// Pages of list_rows: the same person has the same label on every page.
	var who []any
	after := ""
	for range 4 {
		page := run("list_rows", map[string]any{"table": "bookings", "limit": 1, "after": after})
		who = append(who, page["rows"].([]any)[0].([]any)[2])
		after, _ = page["next_after"].(string)
	}
	if fmt.Sprint(who) != fmt.Sprint([]any{bobs, caras, bobs, "you"}) {
		t.Errorf("pages: %v", who)
	}
	if got := run("list_rows", map[string]any{"table": "bookings", "where": map[string]any{"visitor_id": caras}})["rows"].([]any); len(got) != 1 || got[0].([]any)[1] != "Cara" {
		t.Errorf("where by label: %v", got)
	}
}
