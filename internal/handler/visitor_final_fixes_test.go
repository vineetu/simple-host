package handler

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/internal/config"
	"github.com/vsriram/simple-host/internal/db"
)

func TestVisitorStructuralAccountSubstrings(t *testing.T) {
	f := newVisitorFixture(t)
	f.must(t, "PUT", "/resources/notes", map[string]any{"kind": "kv", "preset": "public"}, f.key, 201)
	other := "author_" + strings.ToUpper(f.ids["alice"]) + "_suffix"
	ordinary := "author_01234567-89ab-cdef-0123-456789abcdef"
	own := "author_" + f.ids["bob"]
	for _, key := range []string{other, ordinary, own} {
		f.must(t, "PUT", "/kv/notes/keys/"+key, map[string]any{"value": key}, f.key, 200)
	}
	call := func(args map[string]any) map[string]any {
		t.Helper()
		text, out, bad := toolResultOf(t, f.a.rpc(t, f.token, "tools/call", map[string]any{"name": "visitor_read_site", "arguments": args}))
		if bad || strings.Contains(strings.ToLower(text), f.ids["alice"]) || strings.Contains(strings.ToLower(visitorJSON(t, out)), f.ids["alice"]) {
			t.Fatalf("identity escaped: %s", text)
		}
		return out["visitor_data"].(map[string]any)
	}
	entries := call(f.args("kv", "notes"))["entries"].(map[string]any)["items"].([]any)
	keys := map[string]bool{}
	for _, entry := range entries {
		keys[entry.(map[string]any)["key"].(string)] = true
	}
	if len(entries) != 2 || !keys[ordinary] || !keys[own] {
		t.Fatalf("wrong entries: %+v", entries)
	}
	args := f.args("kv", "notes")
	args["key"] = other
	if call(args)["entries"] != nil {
		t.Fatal("single entry with account key not omitted")
	}
	f.must(t, "PUT", "/resources/columns", map[string]any{"kind": "sqlite", "preset": "public"}, f.key, 201)
	f.must(t, "POST", "/sqlite/columns/schema", map[string]any{"sql": `CREATE TABLE entries(id INTEGER PRIMARY KEY, email TEXT, "` + other + `" TEXT, "` + ordinary + `" INTEGER, "` + own + `" TEXT)`}, f.key, 200)
	args = f.args("sqlite", "columns")
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
		for _, kind := range []string{"kv", "sqlite", "collection"} {
			t.Run(mode+"/"+kind, func(t *testing.T) {
				f := newVisitorFixture(t)
				if kind == "collection" {
					if _, err := f.a.database.Exec(`UPDATE sites SET legacy_data=true WHERE id=$1`, f.sid); err != nil {
						t.Fatal(err)
					}
					res := f.a.at(t, "PUT", pcSiteDomain, "/v1/sites/shop/data/numbers/kind", map[string]any{"kind": "entries", "visibility": "private"}, f.key)
					if res.status != 200 {
						t.Fatalf("declare: %s", res.body)
					}
				} else {
					f.must(t, "PUT", "/resources/numbers", map[string]any{"kind": kind, "preset": "personal"}, f.key, 201)
				}
				if kind == "sqlite" {
					f.must(t, "POST", "/sqlite/numbers/schema", map[string]any{"sql": "CREATE TABLE entries(id INTEGER PRIMARY KEY, n INTEGER)"}, f.key, 200)
					f.must(t, "POST", "/sqlite/numbers/execute", map[string]any{"sql": "INSERT INTO entries(id,n,visitor_id) VALUES(" + adjacent + ",7,?)", "params": []any{f.ids["bob"]}}, f.key, 200)
				}
				args := f.args(kind, "numbers")
				args["key"] = "big"
				args["table"] = "entries"
				call := func(name string, want string) string {
					t.Helper()
					params := map[string]any{"name": name, "arguments": args}
					if wrapped {
						params = map[string]any{"name": "run_tool", "arguments": params}
					}
					text, _, bad := toolResultOf(t, f.a.rpc(t, f.token, "tools/call", params))
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
						err = f.a.database.QueryRow(`SELECT value->>'n' FROM site_storage_kv WHERE site_id=$1 AND resource_name='numbers' AND key='big'`, f.sid).Scan(&got)
					case "collection":
						err = f.a.database.QueryRow(`SELECT data->>'n' FROM collection_items WHERE site_id=$1 AND collection='numbers'`, f.sid).Scan(&got)
					case "sqlite":
						res := f.must(t, "POST", "/sqlite/numbers/query", map[string]any{"sql": "SELECT CAST(n AS TEXT) FROM entries WHERE id=" + exact}, f.key, 200)
						got = res.json(t)["rows"].([]any)[0].([]any)[0].(string)
					}
					if err != nil || got != want {
						t.Fatalf("stored %q want %s: %v", got, want, err)
					}
				}
				args["value"] = map[string]any{"n": json.Number(exact)}
				call("visitor_add_entry", "")
				verify(exact)
				if kind == "collection" {
					if _, err := f.a.database.Exec(`UPDATE collection_items SET id=$1 WHERE site_id=$2 AND collection='numbers'`, exact, f.sid); err != nil {
						t.Fatal(err)
					}
				}
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
					res := f.must(t, "POST", "/sqlite/numbers/query", map[string]any{"sql": "SELECT CAST(id AS TEXT),n FROM entries"}, f.key, 200)
					rows := res.json(t)["rows"].([]any)
					if len(rows) != 1 || rows[0].([]any)[0] != adjacent || rows[0].([]any)[1] != float64(7) {
						t.Fatalf("wrong row changed: %s", res.body)
					}
				}
			})
		}
	}
}

func TestVisitorEntryIDRejectsInexactOrInvalid(t *testing.T) {
	for _, value := range []any{json.Number("1.5"), json.Number("9223372036854775808"), json.Number("0"), json.Number("-1"), "9007199254740993", float64(9007199254740992)} {
		if _, err := visitorEntryID(value); err == nil {
			t.Fatalf("accepted %v", value)
		}
	}
	for _, value := range []string{"9007199254740993", "9223372036854775807"} {
		got, err := visitorEntryID(json.Number(value))
		if err != nil || visitorJSON(t, got) != value {
			t.Fatalf("ID rounded: %v %v", got, err)
		}
	}
}

func TestVisitorResourceExistenceGated(t *testing.T) {
	f := newVisitorFixture(t)
	f.must(t, "PUT", "/resources/exists", map[string]any{"kind": "kv", "preset": "personal"}, f.key, 201)
	f.named(t)
	if _, err := f.a.database.Exec(`DELETE FROM site_viewers WHERE site_id=$1`, f.sid); err != nil {
		t.Fatal(err)
	}
	check := func(want, passcode string) {
		t.Helper()
		var first string
		for _, resource := range []string{"exists", "missing"} {
			for _, name := range []string{"visitor_read_site", "visitor_add_entry", "visitor_change_entry"} {
				args := f.args("kv", resource)
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
				text, _, bad := toolResultOf(t, f.a.rpc(t, f.token, "tools/call", map[string]any{"name": name, "arguments": args}))
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
	if err := db.SetSiteOffline(context.Background(), f.a.database, f.sid, true); err != nil {
		t.Fatal(err)
	}
	check("site_offline", "")
	if err := db.SetSiteOffline(context.Background(), f.a.database, f.sid, false); err != nil {
		t.Fatal(err)
	}
	if _, err := f.a.database.Exec(`UPDATE sites SET access='anyone' WHERE id=$1`, f.sid); err != nil {
		t.Fatal(err)
	}
	if err := f.a.sites.SetPasscodeKey(testPasscodeKey); err != nil {
		t.Fatal(err)
	}
	withPasscodeLimits(t, func(l *config.Limits) {
		l.SitePasscodes = true
		l.RatePasscodeSite.Burst = 100
		l.RatePasscodeIP.Burst = 100
	})
	if res := f.a.at(t, "PUT", pcSiteDomain, "/v1/sites/shop/lock", map[string]any{"passcode": "123456"}, f.key); res.status != 200 {
		t.Fatalf("lock: %s", res.body)
	}
	check("site_locked", "")
	check("site_locked", "wrong")
	args := f.args("kv", "exists")
	args["passcode"] = "123456"
	f.call(t, "visitor_read_site", args, "")
	args["resource"] = "missing"
	f.call(t, "visitor_read_site", args, "not found")
}
