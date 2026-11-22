package servertest

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/config"

	appstorage "github.com/vsriram/simple-host/host/data"
	"github.com/vsriram/simple-host/host/handler"
	"github.com/vsriram/simple-host/host/model"
	sitesrepo "github.com/vsriram/simple-host/host/sites/repo"
)

func rdJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// Tool discovery names only what the caller may see: a resource or table the
// rules deny is absent (even when its file is corrupt, so it is never
// opened), and a missing resource is refused with the same text as a denied
// one at every site state.
func TestRDVisitorDiscoveryHidesPrivateResources(t *testing.T) {
	f := NewVisitorFixture(t)
	f.Must(t, "PUT", "/resources/secretdb", map[string]any{"kind": "sqlite"}, f.Key, 201)
	f.Must(t, "POST", "/sqlite/secretdb/schema", map[string]any{"sql": "CREATE TABLE sensitive_customers(id INTEGER PRIMARY KEY, email TEXT)"}, f.Key, 200)
	f.Must(t, "PUT", "/resources/secretkv", map[string]any{"kind": "kv"}, f.Key, 201)
	f.Must(t, "PUT", "/resources/mixed", `{"kind":"sqlite","rules":`+string(model.OwnerOnlyRules)+`,"tables":{"visible":{"rules":`+RulesPublic+`}}}`, f.Key, 201)
	f.Must(t, "POST", "/sqlite/mixed/schema", map[string]any{"sql": "CREATE TABLE visible(id INTEGER PRIMARY KEY)"}, f.Key, 200)
	f.Must(t, "POST", "/sqlite/mixed/schema", map[string]any{"sql": "CREATE TABLE secret_table(id INTEGER PRIMARY KEY)"}, f.Key, 200)
	f.Named(t)
	args := map[string]any{"address": "https://" + f.Host}
	check := func() {
		raw := rdJSON(t, f.Call(t, "visitor_read_site", args, ""))
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
	path := f.A.Data.StorageSQLPath(appstorage.Call{OwnerID: f.OwnerID, SiteName: "shop", ResourceName: "secretdb"})
	if err := os.WriteFile(path, []byte("not a SQLite database"), 0600); err != nil {
		t.Fatal(err)
	}
	check()
}

func TestRDVisitorResourceExistenceGated(t *testing.T) {
	f := NewVisitorFixture(t)
	f.Must(t, "PUT", "/resources/exists", StorageRules("kv", RulesPersonal), f.Key, 201)
	f.Named(t)
	if _, err := f.A.Database.Exec(`DELETE FROM site_viewers WHERE site_id=$1`, f.Sid); err != nil {
		t.Fatal(err)
	}
	check := func(want, passcode string) {
		t.Helper()
		var first string
		for _, resource := range []string{"exists", "missing"} {
			for _, name := range []string{"visitor_read_site", "visitor_add_entry", "visitor_change_entry"} {
				args := f.Args("kv", resource)
				args["key"] = "one"
				if name != "visitor_read_site" {
					args["value"] = 1
				}
				if name == "visitor_change_entry" {
					args["action"] = "edit"
				}
				if passcode != "" {
					args["passcode"] = passcode
				}
				text, _, bad := ToolResultOf(t, f.A.Rpc(t, f.Token, "tools/call", map[string]any{"name": name, "arguments": args}))
				if !bad || !strings.Contains(text, want) {
					t.Fatalf("%s %s: %s", name, resource, text)
				}
				if first == "" {
					first = text
				} else if text != first {
					t.Fatalf("resource existence disclosed: %q != %q", text, first)
				}
			}
		}
	}
	check("site_private", "")
	if err := sitesrepo.SetSiteOffline(context.Background(), f.A.Database, f.Sid, true); err != nil {
		t.Fatal(err)
	}
	check("site_offline", "")
	if err := sitesrepo.SetSiteOffline(context.Background(), f.A.Database, f.Sid, false); err != nil {
		t.Fatal(err)
	}
	if _, err := f.A.Database.Exec(`UPDATE sites SET access='anyone' WHERE id=$1`, f.Sid); err != nil {
		t.Fatal(err)
	}
	if err := f.A.Access.SetPasscodeKey(TestPasscodeKey); err != nil {
		t.Fatal(err)
	}
	WithPasscodeLimits(t, func(l *config.Limits) {
		l.SitePasscodes = true
		l.RatePasscodeSite.Burst = 100
		l.RatePasscodeIP.Burst = 100
	}, &f.A.Sites.Limits, &f.A.Access.Limits, &f.A.Data.Limits, &f.A.Access.Passcode.Limits)
	if res := f.A.At(t, "PUT", PcSiteDomain, "/v1/sites/shop/lock", map[string]any{"passcode": "123456"}, f.Key); res.Status != 200 {
		t.Fatalf("lock: %s", res.Body)
	}
	check("site_locked", "")
	check("site_locked", "wrong")
	args := f.Args("kv", "exists")
	args["passcode"] = "123456"
	f.Call(t, "visitor_read_site", args, "")
	args["resource"] = "missing"
	f.Call(t, "visitor_read_site", args, "not found")
}

// Another person's account id is hidden wherever it appears inside a key or a
// column name, and the hidden entry neither shifts pagination nor hides a
// legitimate id-shaped key; the caller's own id stays addressable.
func TestRDVisitorStructuralAccountSubstrings(t *testing.T) {
	f := NewVisitorFixture(t)
	f.Must(t, "PUT", "/resources/notes", StorageRules("kv", RulesPublic), f.Key, 201)
	other := "author_" + strings.ToUpper(f.Ids["alice"]) + "_suffix"
	ordinary := "author_01234567-89ab-cdef-0123-456789abcdef"
	own := "author_" + f.Ids["bob"]
	for _, key := range []string{other, ordinary, own} {
		f.Must(t, "PUT", "/kv/notes/keys/"+key, map[string]any{"value": key}, f.Key, 200)
	}
	call := func(args map[string]any) map[string]any {
		t.Helper()
		text, out, bad := ToolResultOf(t, f.A.Rpc(t, f.Token, "tools/call", map[string]any{"name": "visitor_read_site", "arguments": args}))
		if bad || strings.Contains(strings.ToLower(text), f.Ids["alice"]) || strings.Contains(strings.ToLower(rdJSON(t, out)), f.Ids["alice"]) {
			t.Fatalf("identity escaped: %s", text)
		}
		return out["visitor_data"].(map[string]any)
	}
	entries := call(f.Args("kv", "notes"))["entries"].(map[string]any)["items"].([]any)
	keys := map[string]bool{}
	for _, entry := range entries {
		keys[entry.(map[string]any)["key"].(string)] = true
	}
	if len(entries) != 2 || !keys[ordinary] || !keys[own] {
		t.Fatalf("wrong entries: %+v", entries)
	}
	args := f.Args("kv", "notes")
	args["key"] = other
	if call(args)["entries"] != nil {
		t.Fatal("single entry with account key not omitted")
	}
	f.Must(t, "PUT", "/resources/columns", StorageRules("sqlite", RulesPublic), f.Key, 201)
	f.Must(t, "POST", "/sqlite/columns/schema", map[string]any{"sql": `CREATE TABLE entries(id INTEGER PRIMARY KEY, email TEXT, "` + other + `" TEXT, "` + ordinary + `" INTEGER, "` + own + `" TEXT)`}, f.Key, 200)
	args = f.Args("sqlite", "columns")
	args["table"] = "entries"
	out := call(args)
	schema := out["resources"].([]any)[0].(map[string]any)["columns"].(map[string]any)
	if schema["author_[account omitted]_suffix"] != "TEXT" || schema[ordinary] != "INTEGER" || schema[own] != "TEXT" || schema["email"] != "TEXT" {
		t.Fatalf("schema changed: %+v", schema)
	}
	cols := out["entries"].(map[string]any)["columns"].([]any)
	if len(cols) != 5 || cols[2] != "author_[account omitted]_suffix" {
		t.Fatalf("row columns: %+v", cols)
	}
}

func TestRDVisitorKVIdentifiersRoundTrip(t *testing.T) {
	f := NewVisitorFixture(t)
	f.Must(t, "PUT", "/resources/notes", StorageRules("kv", RulesPersonal), f.Key, 201)
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
	if get()["items"].([]any)[0].(map[string]any)["key"] != keys[1] {
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
	// Another person's account id used as a key is omitted without breaking paging.
	f.Must(t, "PUT", "/resources/publicnotes", StorageRules("kv", RulesPublic), f.Key, 201)
	for _, k := range []string{f.Ids["alice"], "zz-end"} {
		f.Must(t, "PUT", "/kv/publicnotes/keys/"+k, map[string]any{"value": "visible"}, f.Key, 200)
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
	// A cursor is bound to its resource.
	args["resource"] = "notes"
	f.Call(t, "visitor_read_site", args, "invalid pagination cursor")
	// The caller's own account id stays addressable.
	f.Must(t, "PUT", "/kv/notes/keys/"+f.Ids["bob"], map[string]any{"value": "own-key"}, f.Callers["bob"], 200)
	args = f.Args("kv", "notes")
	args["key"] = f.Ids["bob"]
	text, out, bad := ToolResultOf(t, f.A.Rpc(t, f.Token, "tools/call", map[string]any{"name": "visitor_read_site", "arguments": args}))
	if bad || out["visitor_data"].(map[string]any)["entries"].(map[string]any)["key"] != f.Ids["bob"] {
		t.Fatalf("own key lost: %s", text)
	}
}

// Numbers beyond 2^53 and row ids survive add, edit and delete exactly, both
// called directly and through run_tool, and an inexact or invalid id is refused.
func TestRDVisitorWriteNumbersAndIDsExact(t *testing.T) {
	const exact = "9007199254740993"
	const adjacent = "9007199254740992"
	const changed = "9007199254740995"
	for _, wrapped := range []bool{false, true} {
		mode := "direct"
		if wrapped {
			mode = "wrapped"
		}
		for _, kind := range []string{"kv", "sqlite"} {
			t.Run(mode+"/"+kind, func(t *testing.T) {
				f := NewVisitorFixture(t)
				f.Must(t, "PUT", "/resources/numbers", StorageRules(kind, RulesPersonal), f.Key, 201)
				if kind == "sqlite" {
					f.Must(t, "POST", "/sqlite/numbers/schema", map[string]any{"sql": "CREATE TABLE entries(id INTEGER PRIMARY KEY, n INTEGER)"}, f.Key, 200)
					f.Must(t, "POST", "/sqlite/numbers/execute", map[string]any{"sql": "INSERT INTO entries(id,n,visitor_id) VALUES(" + adjacent + ",7,?)", "params": []any{f.Ids["bob"]}}, f.Key, 200)
				}
				args := f.Args(kind, "numbers")
				args["key"] = "big"
				args["table"] = "entries"
				call := func(name string) {
					t.Helper()
					params := map[string]any{"name": name, "arguments": args}
					if wrapped {
						params = map[string]any{"name": "run_tool", "arguments": params}
					}
					text, _, bad := ToolResultOf(t, f.A.Rpc(t, f.Token, "tools/call", params))
					if bad {
						t.Fatalf("%s: %s", name, text)
					}
				}
				verify := func(want string) {
					t.Helper()
					var got string
					switch kind {
					case "kv":
						got = strings.TrimSuffix(strings.TrimPrefix(ReadStorageKVValue(t, f.A.Data, StorageKVFixtureCall(t, f.A.Data, f.Sid, "numbers"), "big"), `{"n": `), "}")
					case "sqlite":
						res := f.Must(t, "POST", "/sqlite/numbers/query", map[string]any{"sql": "SELECT CAST(n AS TEXT) FROM entries WHERE id=" + exact}, f.Key, 200)
						got = res.JSON(t)["rows"].([]any)[0].([]any)[0].(string)
					}
					if got != want {
						t.Fatalf("stored %q want %s", got, want)
					}
				}
				args["value"] = map[string]any{"n": json.Number(exact)}
				call("visitor_add_entry")
				verify(exact)
				if kind != "kv" {
					args["id"] = json.Number(exact)
				}
				args["action"] = "edit"
				args["value"] = map[string]any{"n": json.Number(changed)}
				call("visitor_change_entry")
				verify(changed)
				args["action"] = "delete"
				delete(args, "value")
				call("visitor_change_entry")
				if kind == "sqlite" {
					res := f.Must(t, "POST", "/sqlite/numbers/query", map[string]any{"sql": "SELECT CAST(id AS TEXT),n FROM entries"}, f.Key, 200)
					rows := res.JSON(t)["rows"].([]any)
					if len(rows) != 1 || rows[0].([]any)[0] != adjacent || rows[0].([]any)[1] != float64(7) {
						t.Fatalf("wrong row changed: %s", res.Body)
					}
				}
			})
		}
	}
}

func TestRDVisitorEntryIDRejectsInexactOrInvalid(t *testing.T) {
	for _, value := range []any{json.Number("1.5"), json.Number("9223372036854775808"), json.Number("0"), json.Number("-1"), "9007199254740993", float64(9007199254740992)} {
		if _, err := handler.VisitorEntryID(value); err == nil {
			t.Fatalf("accepted %v", value)
		}
	}
	for _, value := range []string{"9007199254740993", "9223372036854775807"} {
		got, err := handler.VisitorEntryID(json.Number(value))
		if err != nil || rdJSON(t, got) != value {
			t.Fatalf("ID rounded: %v %v", got, err)
		}
	}
}

// Resources marked site_passcode "off" stay open to visitor tools on a locked
// site (KV, SQLite; the tools do not reach files); the inherited resource and the page stay locked.
func TestRDVisitorPasscodeExemptResources(t *testing.T) {
	f := NewVisitorFixture(t)
	if err := f.A.Access.SetPasscodeKey(TestPasscodeKey); err != nil {
		t.Fatal(err)
	}
	WithPasscodeLimits(t, func(l *config.Limits) { l.SitePasscodes = true }, &f.A.Sites.Limits, &f.A.Access.Limits, &f.A.Data.Limits)
	for _, kind := range []string{"kv", "sqlite"} {
		f.Must(t, "PUT", "/resources/"+kind, `{"kind":"`+kind+`","site_passcode":"off","rules":`+RulesPersonal+`}`, f.Key, 201)
	}
	f.Must(t, "POST", "/sqlite/sqlite/schema", map[string]any{"sql": "CREATE TABLE entries(id INTEGER PRIMARY KEY,text TEXT)"}, f.Key, 200)
	f.Must(t, "PUT", "/resources/inherited", StorageRules("kv", RulesPersonal), f.Key, 201)
	if res := f.A.At(t, "PUT", PcSiteDomain, "/v1/sites/shop/lock", map[string]any{"passcode": "123456"}, f.Key); res.Status != 200 {
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

// A visitor's write lands in the storage audit history only (a legacy list
// history row is untouched, no address reaches the tool or the owner's
// resource list), and the app's "last used" site is the latest successful
// visitor call, never a denied call or other grant activity.
func TestRDVisitorHistoryAndLastSite(t *testing.T) {
	f := NewVisitorFixture(t)
	f.Must(t, "PUT", "/resources/notes", StorageRules("kv", RulesWall), f.Key, 201)
	const email = "legacy-person@example.com"
	if _, err := f.A.Database.Exec(`INSERT INTO data_history(site_id,kind,name,op,actor_kind,actor_email) VALUES($1,'list','notes','edit','visitor',$2)`, f.Sid, email); err != nil {
		t.Fatal(err)
	}
	args := f.Args("kv", "notes")
	args["key"] = "note"
	args["value"] = map[string]any{"text": email, "by": email}
	f.Call(t, "visitor_add_entry", args, "")
	f.Call(t, "visitor_read_site", f.Args("kv", "notes"), "")
	if raw := rdJSON(t, f.Call(t, "visitor_read_site", map[string]any{"address": "https://" + f.Host}, "")); strings.Contains(raw, email) {
		t.Fatal("visitor result leaks email")
	}
	var listRows int
	if err := f.A.Database.QueryRow(`SELECT count(*) FROM data_history WHERE site_id=$1 AND kind='list' AND actor_email=$2`, f.Sid, email).Scan(&listRows); err != nil || listRows != 1 {
		t.Fatalf("collection history changed: %d %v", listRows, err)
	}
	var op, app string
	if err := f.A.Database.QueryRow(`SELECT op, actor_app FROM data_history WHERE site_id=$1 AND kind='storage' AND name='notes'`, f.Sid).Scan(&op, &app); err != nil || op != "add kv" || app != "ChatGPT" {
		t.Fatalf("storage history: %q %q %v", op, app, err)
	}

	key, undo, err := accountsrepo.IssueInternalKey(f.Ids["bob"])
	if err != nil {
		t.Fatal(err)
	}
	defer undo()
	connections := func() string {
		res := f.A.At(t, "GET", PcSiteDomain, "/v1/me/connections", nil, map[string]string{"X-API-Key": key})
		if res.Status != 200 {
			t.Fatalf("connections: %s", res.Body)
		}
		return string(res.Body)
	}
	if !strings.Contains(connections(), `"last_visitor_site":"https://`+f.Host+`/"`) {
		t.Fatalf("last site missing: %s", connections())
	}
	// Activity on a newer grant that never made a visitor call must not displace it.
	if _, err = f.A.Database.Exec(`INSERT INTO oauth_grants(user_id,client_id,scope,resource,last_used_at,last_visitor_site)
 SELECT user_id,client_id,scope,resource,now()+interval '1 day',jsonb_build_object('address','https://older.example/','at',now()-interval '1 day')
 FROM oauth_grants WHERE user_id=$1 LIMIT 1`, f.Ids["bob"]); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(connections(), "older.example") {
		t.Fatal("non-visitor activity displaced the last visitor site")
	}
	f.Call(t, "visitor_read_site", map[string]any{"address": "https://unknown.invalid"}, "site_not_found")
	if !strings.Contains(connections(), f.Host) {
		t.Fatal("denied call changed last site")
	}
}
