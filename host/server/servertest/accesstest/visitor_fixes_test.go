package accesstest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/access/accesshttp"
	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/host/data/datahttp"
	"github.com/vsriram/simple-host/host/handler"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/server/servertest"
	"github.com/vsriram/simple-host/host/web"
)

func visitorJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestVisitorDiscoveryOmitsPrivateResources(t *testing.T) {
	f := servertest.NewVisitorFixture(t)
	f.Must(t, "PUT", "/resources/secretdb", map[string]any{"kind": "sqlite"}, f.Key, 201)
	f.Must(t, "POST", "/sqlite/secretdb/schema", map[string]any{"sql": "CREATE TABLE sensitive_customers(id INTEGER PRIMARY KEY, email TEXT)"}, f.Key, 200)
	f.Must(t, "PUT", "/resources/secretkv", map[string]any{"kind": "kv"}, f.Key, 201)
	f.Must(t, "PUT", "/resources/mixed", `{"kind":"sqlite","rules":`+string(model.OwnerOnlyRules)+`,"tables":{"visible":{"rules":`+servertest.RulesPublic+`}}}`, f.Key, 201)
	f.Must(t, "POST", "/sqlite/mixed/schema", map[string]any{"sql": "CREATE TABLE visible(id INTEGER PRIMARY KEY)"}, f.Key, 200)
	f.Must(t, "POST", "/sqlite/mixed/schema", map[string]any{"sql": "CREATE TABLE secret_table(id INTEGER PRIMARY KEY)"}, f.Key, 200)
	f.Named(t)
	args := map[string]any{"address": "https://" + f.Host}
	check := func() {
		raw := visitorJSON(t, f.Call(t, "visitor_read_site", args, ""))
		for _, secret := range []string{"secretdb", "sensitive_customers", "secretkv", "secret_table"} {
			if strings.Contains(raw, secret) {
				t.Fatalf("disclosed %s: %s", secret, raw)
			}
		}
		if !strings.Contains(raw, "visible") {
			t.Fatal("per-table permission was ignored")
		}
	}
	check()
	// An inaccessible database must not even be opened during discovery.
	path := f.A.Data.StorageSQLPath(datahttp.StorageCall{OwnerID: f.OwnerID, SiteName: "shop", ResourceName: "secretdb"})
	if err := os.WriteFile(path, []byte("not a SQLite database"), 0600); err != nil {
		t.Fatal(err)
	}
	check()
}

func TestVisitorStorageHistorySeparated(t *testing.T) {
	f := servertest.NewVisitorFixture(t)
	f.Must(t, "PUT", "/resources/notes", servertest.StorageRules("kv", servertest.RulesWall), f.Key, 201)
	const email = "legacy-person@example.com"
	_, err := f.A.Database.Exec(`INSERT INTO data_history(site_id,kind,name,op,actor_kind,actor_email) VALUES($1,'list','notes','edit','visitor',$2)`, f.Sid, email)
	if err != nil {
		t.Fatal(err)
	}
	args := f.Args("kv", "notes")
	args["key"] = "note"
	args["value"] = map[string]any{"text": email, "by": email}
	f.Call(t, "visitor_add_entry", args, "")
	f.Call(t, "visitor_read_site", f.Args("kv", "notes"), "")
	raw := visitorJSON(t, f.Call(t, "visitor_read_site", map[string]any{"address": "https://" + f.Host}, ""))
	if strings.Contains(raw, email) {
		t.Fatal("visitor result leaks email")
	}
	owner := f
	owner.Token = f.A.Connect(t, f.Owner, f.A.RegisterClient(t, servertest.TestRedirect), servertest.TestRedirect)["access_token"].(string)
	text, _, bad := servertest.ToolResultOf(t, f.A.Rpc(t, owner.Token, "tools/call", map[string]any{"name": "storage_list_resources", "arguments": map[string]any{"site": "shop"}}))
	if bad || strings.Contains(text, email) {
		t.Fatalf("storage list: %s", text)
	}
	var listRows int
	if err := f.A.Database.QueryRow(`SELECT count(*) FROM data_history WHERE site_id=$1 AND kind='list' AND actor_email=$2`, f.Sid, email).Scan(&listRows); err != nil || listRows != 1 {
		t.Fatalf("collection history changed: %d %v", listRows, err)
	}
	var op, app string
	if err := f.A.Database.QueryRow(`SELECT op, actor_app FROM data_history WHERE site_id=$1 AND kind='storage' AND name='notes'`, f.Sid).Scan(&op, &app); err != nil || op != "add kv" || app != "ChatGPT" {
		t.Fatalf("storage history: %q %q %v", op, app, err)
	}
}

func TestVisitorKVIdentifiersRoundTrip(t *testing.T) {
	f := servertest.NewVisitorFixture(t)
	f.Must(t, "PUT", "/resources/notes", servertest.StorageRules("kv", servertest.RulesPersonal), f.Key, 201)
	keys := []string{"00000000-1111-2222-3333-444444444444", "00000000-1111-2222-3333-555555555555"}
	for _, key := range keys {
		args := f.Args("kv", "notes")
		args["key"] = key
		args["value"] = "mine"
		f.Call(t, "visitor_add_entry", args, "")
	}
	args := f.Args("kv", "notes")
	args["limit"] = 1
	get := func() map[string]any {
		return f.Call(t, "visitor_read_site", args, "")["visitor_data"].(map[string]any)["entries"].(map[string]any)
	}
	first := get()
	key := first["items"].([]any)[0].(map[string]any)["key"]
	if key != keys[0] {
		t.Fatalf("UUID key changed: %v", key)
	}
	args["after"] = first["next_after"]
	second := get()
	if second["items"].([]any)[0].(map[string]any)["key"] != keys[1] {
		t.Fatal("pagination failed")
	}
	change := f.Args("kv", "notes")
	change["key"] = key
	change["action"] = "edit"
	change["value"] = "edited"
	f.Call(t, "visitor_change_entry", change, "")
	change["action"] = "delete"
	delete(change, "value")
	f.Call(t, "visitor_change_entry", change, "")
	// An actual other person's account ID is omitted, even when used as a key.
	f.Must(t, "PUT", "/resources/publicnotes", servertest.StorageRules("kv", servertest.RulesPublic), f.Key, 201)
	for _, key := range []string{f.Ids["alice"], "zz-end"} {
		f.Must(t, "PUT", "/kv/publicnotes/keys/"+key, map[string]any{"value": "visible"}, f.Key, 200)
	}
	args = f.Args("kv", "publicnotes")
	args["limit"] = 1
	first = get()
	if len(first["items"].([]any)) != 0 {
		t.Fatal("other person's account key exposed")
	}
	args["after"] = first["next_after"]
	if get()["items"].([]any)[0].(map[string]any)["key"] != "zz-end" {
		t.Fatal("hidden account key broke pagination")
	}
	// The cursor is bound to its resource, and cannot select an entry elsewhere.
	args["resource"] = "notes"
	f.Call(t, "visitor_read_site", args, "invalid pagination cursor")
	// The caller's own account key remains addressable.
	f.Must(t, "PUT", "/kv/notes/keys/"+f.Ids["bob"], map[string]any{"value": "own-key"}, f.Callers["bob"], 200)
	args = f.Args("kv", "notes")
	args["key"] = f.Ids["bob"]
	text, out, bad := servertest.ToolResultOf(t, f.A.Rpc(t, f.Token, "tools/call", map[string]any{"name": "visitor_read_site", "arguments": args}))
	if bad || out["visitor_data"].(map[string]any)["entries"].(map[string]any)["key"] != f.Ids["bob"] {
		t.Fatalf("own key lost: %s", text)
	}
}

func TestVisitorSchemaKeepsIdentityColumnNames(t *testing.T) {
	// Metadata keeps identity-like field names, but cannot reintroduce raw emails.
	metadata := accesshttp.VisitorPrivateData(accesshttp.VisitorStructure{Value: map[string]string{"email": "TEXT", "label": "private@example.com"}}).(map[string]any)
	if metadata["email"] != "TEXT" || strings.Contains(visitorJSON(t, metadata), "private@example.com") {
		t.Fatalf("metadata privacy: %+v", metadata)
	}

	f := servertest.NewVisitorFixture(t)
	f.Must(t, "PUT", "/resources/contacts", servertest.StorageRules("sqlite", servertest.RulesPublic), f.Key, 201)
	f.Must(t, "POST", "/sqlite/contacts/schema", map[string]any{"sql": "CREATE TABLE entries(id INTEGER PRIMARY KEY, email TEXT NOT NULL, user_id TEXT, by TEXT)"}, f.Key, 200)
	f.Must(t, "POST", "/sqlite/contacts/execute", map[string]any{"sql": "INSERT INTO entries VALUES(1,'private@example.com','private-user','private-author')"}, f.Key, 200)
	args := f.Args("sqlite", "contacts")
	args["table"] = "entries"
	out := f.Call(t, "visitor_read_site", args, "")["visitor_data"].(map[string]any)
	schema := out["resources"].([]any)[0].(map[string]any)["columns"].(map[string]any)
	for _, name := range []string{"email", "user_id", "by"} {
		if schema[name] != "TEXT" {
			t.Fatalf("schema column removed: %s %+v", name, schema)
		}
	}
	entries := out["entries"].(map[string]any)
	if len(entries["columns"].([]any)) != 4 || len(entries["rows"].([]any)[0].([]any)) != 4 {
		t.Fatal("column/row alignment changed")
	}
	if strings.Contains(visitorJSON(t, out), "private-") || strings.Contains(visitorJSON(t, out), "private@example.com") {
		t.Fatal("identity value leaked")
	}
}

func TestVisitorPasscodeExemptResources(t *testing.T) {
	f := servertest.NewVisitorFixture(t)
	if err := f.A.Access.SetPasscodeKey(servertest.TestPasscodeKey); err != nil {
		t.Fatal(err)
	}
	servertest.WithPasscodeLimits(t, func(l *config.Limits) { l.SitePasscodes = true }, &f.A.Sites.Limits, &f.A.Access.Limits, &f.A.Data.Limits)
	for _, kind := range []string{"kv", "sqlite"} {
		f.Must(t, "PUT", "/resources/"+kind, `{"kind":"`+kind+`","site_passcode":"off","rules":`+servertest.RulesPersonal+`}`, f.Key, 201)
	}
	f.Must(t, "POST", "/sqlite/sqlite/schema", map[string]any{"sql": "CREATE TABLE entries(id INTEGER PRIMARY KEY,text TEXT)"}, f.Key, 200)
	f.Must(t, "PUT", "/resources/inherited", servertest.StorageRules("kv", servertest.RulesPersonal), f.Key, 201)
	if res := f.A.At(t, "PUT", servertest.PcSiteDomain, "/v1/sites/shop/lock", map[string]any{"passcode": "123456"}, f.Key); res.Status != 200 {
		t.Fatalf("lock: %s", res.Body)
	}
	for _, kind := range []string{"kv", "sqlite"} {
		args := f.Args(kind, kind)
		args["key"] = "one"
		args["table"] = "entries"
		args["value"] = map[string]any{"text": "open"}
		added := f.Call(t, "visitor_add_entry", args, "")["visitor_data"].(map[string]any)
		delete(args, "value")
		f.Call(t, "visitor_read_site", args, "")
		args["value"] = map[string]any{"text": "changed"}
		args["action"] = "edit"
		if kind == "sqlite" {
			args["id"] = added["last_insert_id"]
		}
		f.Call(t, "visitor_change_entry", args, "")
		args["page"] = "index.html"
		delete(args, "action")
		delete(args, "value")
		f.Call(t, "visitor_read_site", args, "site_locked")
	}
	f.Call(t, "visitor_read_site", f.Args("kv", "inherited"), "site_locked")
}

func TestVisitorNumbersRemainExact(t *testing.T) {
	f := servertest.NewVisitorFixture(t)
	f.Must(t, "PUT", "/resources/numbers", servertest.StorageRules("kv", servertest.RulesPublic), f.Key, 201)
	const exact = "9007199254740993"
	servertest.SeedStorageKV(t, f.A.Data, f.Sid, "numbers", "big", `{"nested":[`+exact+`]}`, "")
	response := f.A.Rpc(t, f.Token, "tools/call", map[string]any{"name": "visitor_read_site", "arguments": f.Args("kv", "numbers")})
	text, _, bad := servertest.ToolResultOf(t, response)
	if bad || !strings.Contains(text, exact) {
		t.Fatalf("rounded text: %s", text)
	}
	// Invoke and sanitization also preserve exact numbers in structured results.
	r := httptest.NewRequest("GET", "https://example.com", nil)
	data, err := handler.VisitorInvoke(r, "GET", nil, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"nested":[`+exact+`]}`) })
	if err != nil || !strings.Contains(visitorJSON(t, accesshttp.VisitorResult(data, false)), exact) {
		t.Fatalf("rounded structured data: %v %v", data, err)
	}
}

func TestVisitorDiscoveryLimit(t *testing.T) {
	f := servertest.NewVisitorFixture(t)
	f.Must(t, "PUT", "/resources/notes", servertest.StorageRules("kv", servertest.RulesPublic), f.Key, 201)
	for _, key := range []string{"one", "two"} {
		f.Must(t, "PUT", "/kv/notes/keys/"+key, map[string]any{"value": key}, f.Key, 200)
	}
	args := map[string]any{"address": "https://" + f.Host, "limit": 1}
	out := f.Call(t, "visitor_read_site", args, "")["visitor_data"].(map[string]any)
	entries := out["resources"].([]any)[0].(map[string]any)["entries"].(map[string]any)
	if len(entries["items"].([]any)) != 1 || entries["next_after"] == "" {
		t.Fatalf("limit ignored: %+v", entries)
	}
	for _, limit := range []any{0, 501, 1.5, "no"} {
		args["limit"] = limit
		f.Call(t, "visitor_read_site", args, "limit")
	}
}

func TestVisitorPageTruncationAndUppercaseText(t *testing.T) {
	f := servertest.NewVisitorFixture(t)
	root := filepath.Join(f.A.Sites.Disk.SiteDir(f.OwnerID, "shop"), "current")
	if err := os.WriteFile(filepath.Join(root, "about.html"), []byte("<main>About the shop</main>"), 0600); err != nil {
		t.Fatal(err)
	}
	out := f.Call(t, "visitor_read_site", map[string]any{"address": "https://" + f.Host, "page": "about"}, "")["visitor_data"].(map[string]any)
	if out["page_text"] != "About the shop" {
		t.Fatalf("about: %v", out["page_text"])
	}
	for _, tc := range []struct {
		name, text string
		truncated  bool
	}{
		{"upper.TXT", "literal <tag> stays </tag>", false},
		{`page\one.txt`, "page with a backslash in its name", false},
		{"large.txt", strings.Repeat("x", (256<<10)+1), true},
		{"exact.txt", strings.Repeat("x", 256<<10), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(filepath.Join(root, tc.name), []byte(tc.text), 0600); err != nil {
				t.Fatal(err)
			}
			out := f.Call(t, "visitor_read_site", map[string]any{"address": "https://" + f.Host, "page": tc.name}, "")["visitor_data"].(map[string]any)
			if out["truncated"] != tc.truncated {
				t.Fatalf("truncated=%v", out["truncated"])
			}
			text := out["page_text"].(string)
			if tc.truncated {
				if !strings.Contains(text, "Page text truncated at 256 KiB") {
					t.Fatal("missing truncation notice")
				}
			} else if text != tc.text {
				t.Fatal("plain text changed")
			}
		})
	}
}

func TestVisitorConnectionLastSite(t *testing.T) {
	f := servertest.NewVisitorFixture(t)
	key, undo, err := accountsrepo.IssueInternalKey(f.Ids["bob"])
	if err != nil {
		t.Fatal(err)
	}
	defer undo()
	connections := func() string {
		res := f.A.At(t, "GET", servertest.PcSiteDomain, "/v1/me/connections", nil, map[string]string{"X-API-Key": key})
		if res.Status != 200 {
			t.Fatalf("connections: %s", res.Body)
		}
		return string(res.Body)
	}
	if strings.Contains(connections(), "https://"+f.Host) {
		t.Fatal("site recorded before a call")
	}
	f.Call(t, "visitor_read_site", map[string]any{"address": "https://" + f.Host}, "")
	if !strings.Contains(connections(), `"last_visitor_site":"https://`+f.Host+`/"`) {
		t.Fatalf("last site missing: %s", connections())
	}
	// Other activity on an older grant must not replace the latest visitor site.
	_, err = f.A.Database.Exec(`INSERT INTO oauth_grants(user_id,client_id,scope,resource,last_used_at,last_visitor_site)
 SELECT user_id,client_id,scope,resource,now()+interval '1 day',jsonb_build_object('address','https://older.example/','at',now()-interval '1 day')
 FROM oauth_grants WHERE user_id=$1 LIMIT 1`, f.Ids["bob"])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(connections(), "older.example") {
		t.Fatal("non-visitor activity displaced the last visitor site")
	}
	// A denied call must not overwrite successful activity.
	f.Call(t, "visitor_read_site", map[string]any{"address": "https://unknown.invalid"}, "site_not_found")
	if !strings.Contains(connections(), f.Host) {
		t.Fatal("denied call changed last site")
	}
	page, err := web.EmbeddedTree.ReadFile("static/showcase.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(page), "Last used on ' + esc(c.last_visitor_site)") {
		t.Fatal("connected-app row does not show escaped site address")
	}
}
