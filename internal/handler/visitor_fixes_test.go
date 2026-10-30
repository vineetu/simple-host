package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/internal/config"
	"github.com/vsriram/simple-host/internal/db"
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
	f := newVisitorFixture(t)
	f.must(t, "PUT", "/resources/secretdb", map[string]any{"kind": "sqlite", "preset": "private"}, f.key, 201)
	f.must(t, "POST", "/sqlite/secretdb/schema", map[string]any{"sql": "CREATE TABLE sensitive_customers(id INTEGER PRIMARY KEY, email TEXT)"}, f.key, 200)
	f.must(t, "PUT", "/resources/secretkv", map[string]any{"kind": "kv", "preset": "private"}, f.key, 201)
	f.must(t, "PUT", "/resources/mixed", map[string]any{"kind": "sqlite", "preset": "private", "tables": map[string]any{"visible": map[string]any{"preset": "public"}}}, f.key, 201)
	f.must(t, "POST", "/sqlite/mixed/schema", map[string]any{"sql": "CREATE TABLE visible(id INTEGER PRIMARY KEY)"}, f.key, 200)
	f.must(t, "POST", "/sqlite/mixed/schema", map[string]any{"sql": "CREATE TABLE secret_table(id INTEGER PRIMARY KEY)"}, f.key, 200)
	f.named(t)
	args := map[string]any{"address": "https://" + f.host}
	check := func() {
		raw := visitorJSON(t, f.call(t, "visitor_read_site", args, ""))
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
	path := f.a.sites.storageSQLPath(storageCall{ownerID: f.ownerID, siteName: "shop", resourceName: "secretdb"})
	if err := os.WriteFile(path, []byte("not a SQLite database"), 0600); err != nil {
		t.Fatal(err)
	}
	check()
}

func TestVisitorStorageHistorySeparated(t *testing.T) {
	f := newVisitorFixture(t)
	f.must(t, "PUT", "/resources/notes", map[string]any{"kind": "kv", "preset": "wall"}, f.key, 201)
	const email = "legacy-person@example.com"
	_, err := f.a.database.Exec(`INSERT INTO data_history(site_id,kind,name,op,actor_kind,actor_email) VALUES($1,'list','notes','edit','visitor',$2)`, f.sid, email)
	if err != nil {
		t.Fatal(err)
	}
	args := f.args("kv", "notes")
	args["key"] = "note"
	args["value"] = map[string]any{"text": email, "by": email}
	f.call(t, "visitor_add_entry", args, "")
	f.call(t, "visitor_read_site", f.args("kv", "notes"), "")
	raw := visitorJSON(t, f.call(t, "visitor_read_site", map[string]any{"address": "https://" + f.host}, ""))
	if strings.Contains(raw, email) {
		t.Fatal("visitor result leaks email")
	}
	owner := f
	owner.token = f.a.connect(t, f.owner, f.a.registerClient(t, testRedirect), testRedirect)["access_token"].(string)
	text, _, bad := toolResultOf(t, f.a.rpc(t, owner.token, "tools/call", map[string]any{"name": "storage_list_resources", "arguments": map[string]any{"site": "shop"}}))
	if bad || strings.Contains(text, email) || !strings.Contains(text, "via ChatGPT") {
		t.Fatalf("storage history: %s", text)
	}
	list, err := db.ListHistory(context.Background(), f.a.database, f.sid, db.HistoryList, "notes", 20, 0)
	if err != nil || len(list) != 1 || list[0].By != email {
		t.Fatalf("collection history changed: %+v %v", list, err)
	}
	storage, err := db.ListHistory(context.Background(), f.a.database, f.sid, db.HistoryStorage, "notes", 20, 0)
	if err != nil || len(storage) != 1 || storage[0].Op != "add kv" {
		t.Fatalf("storage history: %+v %v", storage, err)
	}
}

func TestVisitorRedactsEmbeddedAccountIDs(t *testing.T) {
	id := "01234567-89ab-cdef-0123-456789abcdef"
	for _, text := range []string{"author_" + id, id + "_suffix", "x" + strings.ToUpper(id) + "y"} {
		if got := visitorPrivateData(text).(string); strings.Contains(strings.ToLower(got), id) {
			t.Fatalf("identity survived: %s", got)
		}
	}
}

func TestVisitorKVIdentifiersRoundTrip(t *testing.T) {
	f := newVisitorFixture(t)
	f.must(t, "PUT", "/resources/notes", map[string]any{"kind": "kv", "preset": "personal"}, f.key, 201)
	keys := []string{"00000000-1111-2222-3333-444444444444", "00000000-1111-2222-3333-555555555555"}
	for _, key := range keys {
		args := f.args("kv", "notes")
		args["key"] = key
		args["value"] = "mine"
		f.call(t, "visitor_add_entry", args, "")
	}
	args := f.args("kv", "notes")
	args["limit"] = 1
	get := func() map[string]any {
		return f.call(t, "visitor_read_site", args, "")["visitor_data"].(map[string]any)["entries"].(map[string]any)
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
	change := f.args("kv", "notes")
	change["key"] = key
	change["action"] = "edit"
	change["value"] = "edited"
	f.call(t, "visitor_change_entry", change, "")
	change["action"] = "delete"
	delete(change, "value")
	f.call(t, "visitor_change_entry", change, "")
	// An actual other person's account ID is omitted, even when used as a key.
	f.must(t, "PUT", "/resources/publicnotes", map[string]any{"kind": "kv", "preset": "public"}, f.key, 201)
	for _, key := range []string{f.ids["alice"], "zz-end"} {
		f.must(t, "PUT", "/kv/publicnotes/keys/"+key, map[string]any{"value": "visible"}, f.key, 200)
	}
	args = f.args("kv", "publicnotes")
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
	f.call(t, "visitor_read_site", args, "invalid pagination cursor")
	// The caller's own account key remains addressable.
	f.must(t, "PUT", "/kv/notes/keys/"+f.ids["bob"], map[string]any{"value": "own-key"}, f.callers["bob"], 200)
	args = f.args("kv", "notes")
	args["key"] = f.ids["bob"]
	text, out, bad := toolResultOf(t, f.a.rpc(t, f.token, "tools/call", map[string]any{"name": "visitor_read_site", "arguments": args}))
	if bad || out["visitor_data"].(map[string]any)["entries"].(map[string]any)["key"] != f.ids["bob"] {
		t.Fatalf("own key lost: %s", text)
	}
}

func TestVisitorSchemaKeepsIdentityColumnNames(t *testing.T) {
	// Metadata keeps identity-like field names, but cannot reintroduce raw emails.
	metadata := visitorPrivateData(visitorStructure{map[string]string{"email": "TEXT", "label": "private@example.com"}}).(map[string]any)
	if metadata["email"] != "TEXT" || strings.Contains(visitorJSON(t, metadata), "private@example.com") {
		t.Fatalf("metadata privacy: %+v", metadata)
	}

	f := newVisitorFixture(t)
	f.must(t, "PUT", "/resources/contacts", map[string]any{"kind": "sqlite", "preset": "public"}, f.key, 201)
	f.must(t, "POST", "/sqlite/contacts/schema", map[string]any{"sql": "CREATE TABLE entries(id INTEGER PRIMARY KEY, email TEXT NOT NULL, user_id TEXT, by TEXT)"}, f.key, 200)
	f.must(t, "POST", "/sqlite/contacts/execute", map[string]any{"sql": "INSERT INTO entries VALUES(1,'private@example.com','private-user','private-author')"}, f.key, 200)
	args := f.args("sqlite", "contacts")
	args["table"] = "entries"
	out := f.call(t, "visitor_read_site", args, "")["visitor_data"].(map[string]any)
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

func TestVisitorOwnerLegacyLimitsAndSavers(t *testing.T) {
	f := newVisitorFixture(t)
	if _, err := f.a.database.Exec(`UPDATE sites SET legacy_data=true WHERE id=$1`, f.sid); err != nil {
		t.Fatal(err)
	}
	for _, visibility := range []string{"private", "public"} {
		res := f.a.at(t, "PUT", pcSiteDomain, "/v1/sites/shop/data/"+visibility+"/kind", map[string]any{"kind": "entries", "visibility": visibility}, f.key)
		if res.status != 200 {
			t.Fatalf("declare: %s", res.body)
		}
	}
	owner := f
	owner.token = f.a.connect(t, f.owner, f.a.registerClient(t, testRedirect), testRedirect)["access_token"].(string)
	f.a.sites.appendLimiter = newRateLimiter(1, 0)
	args := f.args("collection", "private")
	args["value"] = map[string]any{"message": "first"}
	owner.call(t, "visitor_add_entry", args, "")
	owner.call(t, "visitor_add_entry", args, "rate_limited")
	f.a.sites.appendLimiter = newRateLimiter(100, 0)
	if _, err := f.a.database.Exec(`UPDATE sites SET savers_mode='listed' WHERE id=$1`, f.sid); err != nil {
		t.Fatal(err)
	}
	owner.call(t, "visitor_add_entry", args, "not_allowed")
	args["resource"] = "public"
	owner.call(t, "visitor_add_entry", args, "not_allowed")
}

func TestVisitorPasscodeExemptResources(t *testing.T) {
	f := newVisitorFixture(t)
	if err := f.a.sites.SetPasscodeKey(testPasscodeKey); err != nil {
		t.Fatal(err)
	}
	withPasscodeLimits(t, func(l *config.Limits) { l.SitePasscodes = true })
	for _, kind := range []string{"kv", "sqlite"} {
		f.must(t, "PUT", "/resources/"+kind, map[string]any{"kind": kind, "preset": "personal", "site_passcode": "off"}, f.key, 201)
	}
	f.must(t, "POST", "/sqlite/sqlite/schema", map[string]any{"sql": "CREATE TABLE entries(id INTEGER PRIMARY KEY,text TEXT)"}, f.key, 200)
	f.must(t, "PUT", "/resources/inherited", map[string]any{"kind": "kv", "preset": "personal"}, f.key, 201)
	if res := f.a.at(t, "PUT", pcSiteDomain, "/v1/sites/shop/lock", map[string]any{"passcode": "123456"}, f.key); res.status != 200 {
		t.Fatalf("lock: %s", res.body)
	}
	for _, kind := range []string{"kv", "sqlite"} {
		args := f.args(kind, kind)
		args["key"] = "one"
		args["table"] = "entries"
		args["value"] = map[string]any{"text": "open"}
		added := f.call(t, "visitor_add_entry", args, "")["visitor_data"].(map[string]any)
		delete(args, "value")
		f.call(t, "visitor_read_site", args, "")
		args["value"] = map[string]any{"text": "changed"}
		args["action"] = "edit"
		if kind == "sqlite" {
			args["id"] = added["last_insert_id"]
		}
		f.call(t, "visitor_change_entry", args, "")
		args["page"] = "index.html"
		delete(args, "action")
		delete(args, "value")
		f.call(t, "visitor_read_site", args, "site_locked")
	}
	f.call(t, "visitor_read_site", f.args("kv", "inherited"), "site_locked")
}

func TestVisitorNumbersRemainExact(t *testing.T) {
	f := newVisitorFixture(t)
	f.must(t, "PUT", "/resources/numbers", map[string]any{"kind": "kv", "preset": "public"}, f.key, 201)
	const exact = "9007199254740993"
	if _, err := f.a.database.Exec(`INSERT INTO site_storage_kv(site_id,resource_name,key,value) VALUES($1,'numbers','big',$2::jsonb)`, f.sid, `{"nested":[`+exact+`]}`); err != nil {
		t.Fatal(err)
	}
	response := f.a.rpc(t, f.token, "tools/call", map[string]any{"name": "visitor_read_site", "arguments": f.args("kv", "numbers")})
	text, _, bad := toolResultOf(t, response)
	if bad || !strings.Contains(text, exact) {
		t.Fatalf("rounded text: %s", text)
	}
	// Invoke and sanitization also preserve exact numbers in structured results.
	r := httptest.NewRequest("GET", "https://example.com", nil)
	data, err := visitorInvoke(r, "GET", nil, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"nested":[`+exact+`]}`) })
	if err != nil || !strings.Contains(visitorJSON(t, visitorResult(data, false)), exact) {
		t.Fatalf("rounded structured data: %v %v", data, err)
	}
}

func TestVisitorDiscoveryLimit(t *testing.T) {
	f := newVisitorFixture(t)
	f.must(t, "PUT", "/resources/notes", map[string]any{"kind": "kv", "preset": "public"}, f.key, 201)
	for _, key := range []string{"one", "two"} {
		f.must(t, "PUT", "/kv/notes/keys/"+key, map[string]any{"value": key}, f.key, 200)
	}
	args := map[string]any{"address": "https://" + f.host, "limit": 1}
	out := f.call(t, "visitor_read_site", args, "")["visitor_data"].(map[string]any)
	entries := out["resources"].([]any)[0].(map[string]any)["entries"].(map[string]any)
	if len(entries["items"].([]any)) != 1 || entries["next_after"] == "" {
		t.Fatalf("limit ignored: %+v", entries)
	}
	for _, limit := range []any{0, 101, 1.5, "no"} {
		args["limit"] = limit
		f.call(t, "visitor_read_site", args, "limit")
	}
}

func TestVisitorPageTruncationAndUppercaseText(t *testing.T) {
	f := newVisitorFixture(t)
	root := filepath.Join(f.a.sites.disk.SiteDir(f.ownerID, "shop"), "current")
	for _, tc := range []struct {
		name, text string
		truncated  bool
	}{
		{"upper.TXT", "literal <tag> stays </tag>", false},
		{"large.txt", strings.Repeat("x", (256<<10)+1), true},
		{"exact.txt", strings.Repeat("x", 256<<10), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(filepath.Join(root, tc.name), []byte(tc.text), 0600); err != nil {
				t.Fatal(err)
			}
			out := f.call(t, "visitor_read_site", map[string]any{"address": "https://" + f.host, "page": tc.name}, "")["visitor_data"].(map[string]any)
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
	f := newVisitorFixture(t)
	key, undo, err := db.IssueInternalKey(f.ids["bob"])
	if err != nil {
		t.Fatal(err)
	}
	defer undo()
	connections := func() string {
		res := f.a.at(t, "GET", pcSiteDomain, "/v1/me/connections", nil, map[string]string{"X-API-Key": key})
		if res.status != 200 {
			t.Fatalf("connections: %s", res.body)
		}
		return string(res.body)
	}
	if strings.Contains(connections(), "https://"+f.host) {
		t.Fatal("site recorded before a call")
	}
	f.call(t, "visitor_read_site", map[string]any{"address": "https://" + f.host}, "")
	if !strings.Contains(connections(), `"last_visitor_site":"https://`+f.host+`/"`) {
		t.Fatalf("last site missing: %s", connections())
	}
	// Other activity on an older grant must not replace the latest visitor site.
	_, err = f.a.database.Exec(`INSERT INTO oauth_grants(user_id,client_id,scope,resource,last_used_at,last_visitor_site)
 SELECT user_id,client_id,scope,resource,now()+interval '1 day',jsonb_build_object('address','https://older.example/','at',now()-interval '1 day')
 FROM oauth_grants WHERE user_id=$1 LIMIT 1`, f.ids["bob"])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(connections(), "older.example") {
		t.Fatal("non-visitor activity displaced the last visitor site")
	}
	// A denied call must not overwrite successful activity.
	f.call(t, "visitor_read_site", map[string]any{"address": "https://unknown.invalid"}, "site_not_found")
	if !strings.Contains(connections(), f.host) {
		t.Fatal("denied call changed last site")
	}
	page, err := os.ReadFile("static/showcase.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(page), "Last used on ' + esc(c.last_visitor_site)") {
		t.Fatal("connected-app row does not show escaped site address")
	}
}
