package accesstest

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/host/handler"
	"github.com/vsriram/simple-host/host/server/servertest"
	sitesrepo "github.com/vsriram/simple-host/host/sites/repo"
)

func TestVisitorStructuralAccountSubstrings(t *testing.T) {
	f := servertest.NewVisitorFixture(t)
	f.Must(t, "PUT", "/resources/notes", servertest.StorageRules("kv", servertest.RulesPublic), f.Key, 201)
	other := "author_" + strings.ToUpper(f.Ids["alice"]) + "_suffix"
	ordinary := "author_01234567-89ab-cdef-0123-456789abcdef"
	own := "author_" + f.Ids["bob"]
	for _, key := range []string{other, ordinary, own} {
		f.Must(t, "PUT", "/kv/notes/keys/"+key, map[string]any{"value": key}, f.Key, 200)
	}
	call := func(args map[string]any) map[string]any {
		t.Helper()
		text, out, bad := servertest.ToolResultOf(t, f.A.Rpc(t, f.Token, "tools/call", map[string]any{"name": "visitor_read_site", "arguments": args}))
		if bad || strings.Contains(strings.ToLower(text), f.Ids["alice"]) || strings.Contains(strings.ToLower(visitorJSON(t, out)), f.Ids["alice"]) {
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
	f.Must(t, "PUT", "/resources/columns", servertest.StorageRules("sqlite", servertest.RulesPublic), f.Key, 201)
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

func TestVisitorWriteNumbersAndIDsExact(t *testing.T) {
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
				f := servertest.NewVisitorFixture(t)
				f.Must(t, "PUT", "/resources/numbers", servertest.StorageRules(kind, servertest.RulesPersonal), f.Key, 201)
				if kind == "sqlite" {
					f.Must(t, "POST", "/sqlite/numbers/schema", map[string]any{"sql": "CREATE TABLE entries(id INTEGER PRIMARY KEY, n INTEGER)"}, f.Key, 200)
					f.Must(t, "POST", "/sqlite/numbers/execute", map[string]any{"sql": "INSERT INTO entries(id,n,visitor_id) VALUES(" + adjacent + ",7,?)", "params": []any{f.Ids["bob"]}}, f.Key, 200)
				}
				args := f.Args(kind, "numbers")
				args["key"] = "big"
				args["table"] = "entries"
				call := func(name string, want string) string {
					t.Helper()
					params := map[string]any{"name": name, "arguments": args}
					if wrapped {
						params = map[string]any{"name": "run_tool", "arguments": params}
					}
					text, _, bad := servertest.ToolResultOf(t, f.A.Rpc(t, f.Token, "tools/call", params))
					if bad || want != "" && !strings.Contains(text, want) {
						t.Fatalf("%s: bad=%v %s", name, bad, text)
					}
					return text
				}
				verify := func(want string) {
					t.Helper()
					var got string
					var err error
					switch kind {
					case "kv":
						got = strings.TrimSuffix(strings.TrimPrefix(servertest.ReadStorageKVValue(t, f.A.Data, servertest.StorageKVFixtureCall(t, f.A.Data, f.Sid, "numbers"), "big"), `{"n": `), "}")
					case "sqlite":
						res := f.Must(t, "POST", "/sqlite/numbers/query", map[string]any{"sql": "SELECT CAST(n AS TEXT) FROM entries WHERE id=" + exact}, f.Key, 200)
						got = res.JSON(t)["rows"].([]any)[0].([]any)[0].(string)
					}
					if err != nil || got != want {
						t.Fatalf("stored %q want %s: %v", got, want, err)
					}
				}
				args["value"] = map[string]any{"n": json.Number(exact)}
				call("visitor_add_entry", "")
				verify(exact)
				if kind != "kv" {
					args["id"] = json.Number(exact)
				}
				args["action"] = "edit"
				args["value"] = map[string]any{"n": json.Number(changed)}
				call("visitor_change_entry", "")
				verify(changed)
				args["action"] = "delete"
				delete(args, "value")
				call("visitor_change_entry", "")
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

func TestVisitorEntryIDRejectsInexactOrInvalid(t *testing.T) {
	for _, value := range []any{json.Number("1.5"), json.Number("9223372036854775808"), json.Number("0"), json.Number("-1"), "9007199254740993", float64(9007199254740992)} {
		if _, err := handler.VisitorEntryID(value); err == nil {
			t.Fatalf("accepted %v", value)
		}
	}
	for _, value := range []string{"9007199254740993", "9223372036854775807"} {
		got, err := handler.VisitorEntryID(json.Number(value))
		if err != nil || visitorJSON(t, got) != value {
			t.Fatalf("ID rounded: %v %v", got, err)
		}
	}
}

func TestVisitorResourceExistenceGated(t *testing.T) {
	f := servertest.NewVisitorFixture(t)
	f.Must(t, "PUT", "/resources/exists", servertest.StorageRules("kv", servertest.RulesPersonal), f.Key, 201)
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
				text, _, bad := servertest.ToolResultOf(t, f.A.Rpc(t, f.Token, "tools/call", map[string]any{"name": name, "arguments": args}))
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
	if err := f.A.Access.SetPasscodeKey(servertest.TestPasscodeKey); err != nil {
		t.Fatal(err)
	}
	servertest.WithPasscodeLimits(t, func(l *config.Limits) {
		l.SitePasscodes = true
		l.RatePasscodeSite.Burst = 100
		l.RatePasscodeIP.Burst = 100
	}, &f.A.Sites.Limits, &f.A.Access.Limits, &f.A.Data.Limits, &f.A.Access.Passcode.Limits)
	if res := f.A.At(t, "PUT", servertest.PcSiteDomain, "/v1/sites/shop/lock", map[string]any{"passcode": "123456"}, f.Key); res.Status != 200 {
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
