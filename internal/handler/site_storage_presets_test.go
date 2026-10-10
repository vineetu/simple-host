package handler

import (
	"fmt"
	"net/url"
	"strings"
	"testing"
)

// Storage access presets (2026-10-10): every preset, every action, on KV,
// files and SQLite, for a visitor, another visitor, someone signed out, and
// the owner signed in on the site, checked against a reference written
// independently of the server's decision code.

var presetWho = []string{"nobody", "owner", "own", "signed-in", "anyone"}

// presetRefAllowed: may caller do an action whose value is v, on an entry
// added by author ("" when the action does not touch one entry, or for the
// owner's rows)?
func presetRefAllowed(v, caller, author string) bool {
	switch caller {
	case "owner-site":
		return v != "nobody"
	case "anon":
		return v == "anyone"
	}
	switch v {
	case "anyone", "signed-in":
		return true
	case "own":
		return author == caller
	}
	return false
}

// presetRefValid applies R1 to R4 written out plainly.
func presetRefValid(m storageAccessMatrix) bool {
	rank := map[string]int{}
	for i, v := range presetWho {
		rank[v] = i
	}
	if m.Edit == "anyone" || m.Delete == "anyone" { // R1
		return false
	}
	if m.Add == "own" { // R2
		return false
	}
	if m.Add == "anyone" && (m.Read == "own" || m.Edit == "own" || m.Delete == "own") { // R3
		return false
	}
	return rank[m.Edit] <= rank[m.Read] && rank[m.Delete] <= rank[m.Read] // R4
}

type presetSite struct {
	a                *privateApp
	host, sid        string
	key              map[string]string
	ids              map[string]string // caller -> account id
	callers          map[string]map[string]string
	owner            person
	aliceID, ownerID string
}

func newPresetSite(t *testing.T) presetSite {
	t.Helper()
	a, cert := newSiteApp(t, "canonical")
	owner := a.newPerson(t, "preset-owner")
	alice := a.newPerson(t, "preset-alice")
	bob := a.newPerson(t, "preset-bob")
	a.deploy(t, owner, "shop")
	oid, handle := a.userID(t, owner)
	aid, _ := a.userID(t, alice)
	bid, _ := a.userID(t, bob)
	markReady(t, cert, handle)
	host := "shop." + handle + "." + pcSiteDomain
	sid := a.siteID(t, owner, "shop")
	// The grid sends hundreds of writes from one address.
	a.sites.storageIPLimiter = newRateLimiter(100000, 0)
	a.sites.storageVisitorLimiter = newRateLimiter(100000, 0)
	return presetSite{
		a: a, host: host, sid: sid, key: map[string]string{"X-API-Key": owner.key}, owner: owner,
		ids: map[string]string{"alice": aid, "bob": bid, "owner-site": oid},
		callers: map[string]map[string]string{
			"alice":      browser(host, a.session(t, alice, sid, host)),
			"bob":        browser(host, a.session(t, bob, sid, host)),
			"anon":       browser(host, ""),
			"owner-site": browser(host, a.session(t, owner, sid, host)),
		},
		aliceID: aid, ownerID: oid,
	}
}

func (s presetSite) do(t *testing.T, method, path string, body any, headers map[string]string) resp {
	t.Helper()
	host := s.host
	if headers["X-API-Key"] != "" {
		host = pcSiteDomain
	}
	return s.a.at(t, method, host, "/v1/sites/shop/storage"+path, body, headers)
}

func (s presetSite) must(t *testing.T, method, path string, body any, headers map[string]string, status int) resp {
	t.Helper()
	r := s.do(t, method, path, body, headers)
	if r.status != status {
		t.Fatalf("%s %s: want %d got %d %s", method, path, status, r.status, r.body)
	}
	return r
}

// seed adds one entry by alice through the owner's tools and the database,
// and returns its key, path or row id.
func (s presetSite) seed(t *testing.T, kind, name, label string) string {
	t.Helper()
	switch kind {
	case "kv":
		if _, err := s.a.database.Exec(`INSERT INTO site_storage_kv(site_id,resource_name,key,value,writer_id) VALUES($1,$2,$3,'"seed"',$4)`, s.sid, name, label, s.aliceID); err != nil {
			t.Fatal(err)
		}
		return label
	case "files":
		s.must(t, "PUT", "/files/"+name+"/objects/"+label, "seed", s.key, 200)
		if _, err := s.a.database.Exec(`UPDATE site_storage_files SET writer_id=$4 WHERE site_id=$1 AND resource_name=$2 AND path=$3`, s.sid, name, label, s.aliceID); err != nil {
			t.Fatal(err)
		}
		return label
	}
	r := s.must(t, "POST", "/sqlite/"+name+"/execute", map[string]any{"sql": "INSERT INTO items(v, visitor_id) VALUES(?, ?)", "params": []any{label, s.aliceID}}, s.key, 200)
	return fmt.Sprint(r.json(t)["last_insert_id"])
}

func presetPaths(kind, name, entry string) (one, list string) {
	switch kind {
	case "kv":
		return "/kv/" + name + "/keys/" + entry, "/kv/" + name + "/keys"
	case "files":
		return "/files/" + name + "/objects/" + entry, "/files/" + name + "/objects"
	}
	return "/sqlite/" + name + "/tables/items/rows/" + entry, "/sqlite/" + name + "/tables/items/rows"
}

func TestStoragePresetsEveryActionAndCaller(t *testing.T) {
	s := newPresetSite(t)
	for _, p := range storagePresets {
		for _, kind := range []string{"kv", "files", "sqlite"} {
			name := p.ID + "-" + kind
			put := s.must(t, "PUT", "/resources/"+name, map[string]string{"kind": kind, "preset": p.ID}, s.key, 201).json(t)
			if put["preset"] != p.ID {
				t.Fatalf("%s: preset %v", name, put["preset"])
			}
			if kind == "sqlite" {
				s.must(t, "POST", "/sqlite/"+name+"/schema", map[string]string{"sql": "CREATE TABLE items (id INTEGER PRIMARY KEY, v TEXT, created_at TEXT)"}, s.key, 200)
				if p.Matrix.recordsAuthor() {
					// The server adds visitor_id to tables that use own or
					// that visitors add to.
					cols := s.must(t, "POST", "/sqlite/"+name+"/query", map[string]string{"sql": "SELECT sql FROM sqlite_schema WHERE name='items'"}, s.key, 200)
					if !strings.Contains(string(cols.body), "visitor_id") {
						t.Fatalf("%s: no visitor_id: %s", name, cols.body)
					}
				} else {
					s.must(t, "POST", "/sqlite/"+name+"/schema", map[string]string{"sql": "ALTER TABLE items ADD COLUMN visitor_id TEXT"}, s.key, 200)
				}
			}
			for _, caller := range []string{"alice", "bob", "anon", "owner-site"} {
				h := s.callers[caller]
				label := "e-" + caller
				entry := s.seed(t, kind, name, label)
				one, list := presetPaths(kind, name, entry)
				where := name + " " + caller
				ok := func(r resp) bool { return r.status >= 200 && r.status < 300 }
				// read one
				r := s.do(t, "GET", one, nil, h)
				if want := presetRefAllowed(p.Matrix.Read, caller, "alice"); ok(r) != want {
					t.Fatalf("%s read one: want %v got %d %s", where, want, r.status, r.body)
				}
				// read list: own lists show only the caller's entries
				r = s.do(t, "GET", list, nil, h)
				if want := presetRefAllowed(p.Matrix.Read, caller, "") || p.Matrix.Read == "own" && caller != "anon"; ok(r) != want {
					t.Fatalf("%s read list: want %v got %d %s", where, want, r.status, r.body)
				}
				if ok(r) {
					seen := strings.Contains(string(r.body), label)
					if want := presetRefAllowed(p.Matrix.Read, caller, "alice"); seen != want {
						t.Fatalf("%s list shows alice's entry: want %v got %v %s", where, want, seen, r.body)
					}
					if caller != "alice" && caller != "owner-site" && strings.Contains(string(r.body), s.aliceID) {
						t.Fatalf("%s list leaks alice's visitor_id: %s", where, r.body)
					}
				}
				// add
				switch kind {
				case "kv":
					r = s.do(t, "PUT", "/kv/"+name+"/keys/new-"+caller, `{"value":1}`, h)
				case "files":
					r = s.do(t, "PUT", "/files/"+name+"/objects/new-"+caller, "new", h)
				default:
					r = s.do(t, "POST", list, `{"v":"new"}`, h)
				}
				if want := presetRefAllowed(p.Matrix.Add, caller, ""); ok(r) != want {
					t.Fatalf("%s add: want %v got %d %s", where, want, r.status, r.body)
				}
				// edit alice's entry
				switch kind {
				case "kv":
					r = s.do(t, "PUT", one, `{"value":"edited"}`, h)
				case "files":
					r = s.do(t, "PUT", one, "edited", h)
				default:
					r = s.do(t, "PATCH", one, `{"v":"edited"}`, h)
				}
				if want := presetRefAllowed(p.Matrix.Edit, caller, "alice"); ok(r) != want {
					t.Fatalf("%s edit: want %v got %d %s", where, want, r.status, r.body)
				}
				// delete alice's entry
				r = s.do(t, "DELETE", one, nil, h)
				if want := presetRefAllowed(p.Matrix.Delete, caller, "alice"); ok(r) != want {
					t.Fatalf("%s delete: want %v got %d %s", where, want, r.status, r.body)
				}
				if r.status == 200 {
					s.must(t, "GET", one, nil, s.key, 404)
				} else {
					s.must(t, "GET", one, nil, s.key, 200)
				}
			}
		}
	}
}

func TestStoragePresetsRulesOnSave(t *testing.T) {
	valid := 0
	for _, r := range presetWho {
		for _, a := range presetWho {
			for _, e := range presetWho {
				for _, d := range presetWho {
					m := storageAccessMatrix{r, a, e, d}
					code, _ := m.check()
					if (code == "") != presetRefValid(m) {
						t.Fatalf("%v: server %q, reference %v", m, code, presetRefValid(m))
					}
					if code == "" {
						valid++
					}
				}
			}
		}
	}
	if valid != 161 {
		t.Fatalf("valid matrices: %d, want 161", valid)
	}
	s := newPresetSite(t)
	// The server checks every one of the 625 when the owner saves it.
	n := 0
	for _, r := range presetWho {
		for _, a := range presetWho {
			for _, e := range presetWho {
				for _, d := range presetWho {
					m := storageAccessMatrix{r, a, e, d}
					res := s.do(t, "PUT", "/resources/m", map[string]any{"kind": "kv", "preset": "custom", "read": r, "add": a, "edit": e, "delete": d}, s.key)
					if presetRefValid(m) != (res.status == 200 || res.status == 201) {
						t.Fatalf("%v: %d %s", m, res.status, res.body)
					}
					if !presetRefValid(m) && res.json(t)["code"] != "invalid_access" {
						t.Fatalf("%v: %s", m, res.body)
					}
					n++
				}
			}
		}
	}
	// Overrides are checked too, and reported as custom with their base.
	s.must(t, "PUT", "/resources/inbox", map[string]string{"kind": "sqlite", "preset": "inbox", "add": "signed-in"}, s.key, 201)
	got := s.must(t, "GET", "/resources", nil, s.key, 200)
	if !strings.Contains(string(got.body), `"based_on":"inbox"`) {
		t.Fatalf("override: %s", got.body)
	}
	for _, body := range []map[string]any{
		{"kind": "kv", "preset": "wall", "edit": "anyone"},
		{"kind": "kv", "preset": "inbox", "read": "own"},
		{"kind": "kv", "preset": "records", "add": "own"},
		{"kind": "kv", "preset": "public", "delete": "signed-in", "read": "owner"},
		{"kind": "sqlite", "preset": "private", "tables": map[string]any{"t": map[string]string{"preset": "inbox", "edit": "own"}}},
		{"kind": "kv", "preset": "nonsense"},
		{"kind": "kv", "preset": "wall", "write": "signed-in"},
		{"kind": "kv", "tables": map[string]any{"t": map[string]string{"preset": "public"}}},
		{"kind": "sqlite", "tables": map[string]any{"bad name": map[string]string{"preset": "public"}}},
	} {
		if r := s.do(t, "PUT", "/resources/refused", body, s.key); r.status != 400 {
			t.Fatalf("%v: %d %s", body, r.status, r.body)
		}
	}
	// The older fields: write anyone with full is refused for new resources;
	// others translate as designed.
	for _, c := range []struct {
		read, write, mode, preset string
	}{
		{"anyone", "owner", "full", "public"},
		{"owner", "owner", "full", "private"},
		{"owner", "anyone", "add", "inbox"},
		{"own", "signed-in", "add", "records"},
		{"own", "anyone", "add", "records"},
		{"signed-in", "signed-in", "full", "custom"},
		{"anyone", "signed-in", "full", "custom"},
		{"own", "signed-in", "full", "personal"},
	} {
		r := s.must(t, "PUT", "/resources/old-"+c.read+"-"+c.write+"-"+c.mode, map[string]string{"kind": "kv", "read": c.read, "write": c.write, "write_mode": c.mode}, s.key, 201).json(t)
		if r["preset"] != c.preset {
			t.Fatalf("%v: %v", c, r)
		}
	}
	r := s.must(t, "PUT", "/resources/old-open", map[string]string{"kind": "kv", "read": "anyone", "write": "anyone"}, s.key, 400).json(t)
	if r["rule"] != "anonymous_change" {
		t.Fatalf("open full: %v", r)
	}
	// A legacy resource is labelled and keeps its rules.
	insertLegacyStorage(t, s.a.database, s.sid, "legacy-open", "kv", "anyone", "anyone", "full", "")
	list := s.must(t, "GET", "/resources", nil, s.key, 200)
	if !strings.Contains(string(list.body), `"preset":"legacy"`) {
		t.Fatalf("legacy label: %s", list.body)
	}
	s.must(t, "PUT", "/kv/legacy-open/keys/x", `{"value":1}`, s.callers["anon"], 200)
	s.must(t, "DELETE", "/kv/legacy-open/keys/x", nil, s.callers["anon"], 200)
}

func TestStoragePresetsSQLiteTablesAndStatements(t *testing.T) {
	s := newPresetSite(t)
	alice, bob, anon, own := s.callers["alice"], s.callers["bob"], s.callers["anon"], s.callers["owner-site"]
	s.must(t, "PUT", "/resources/shop", map[string]any{"kind": "sqlite", "preset": "private", "tables": map[string]any{
		"products": map[string]string{"preset": "public"},
		"orders":   map[string]string{"preset": "records"},
		"notes":    map[string]string{"preset": "personal"},
	}}, s.key, 201)
	for _, sql := range []string{
		"CREATE TABLE IF NOT EXISTS products (id INTEGER PRIMARY KEY, name TEXT, cents INTEGER)",
		"CREATE TABLE IF NOT EXISTS orders (id INTEGER PRIMARY KEY, product_id INTEGER REFERENCES products(id), qty INTEGER, status TEXT NOT NULL DEFAULT 'received', created_at TEXT, updated_at TEXT)",
		"CREATE TABLE IF NOT EXISTS notes (id INTEGER PRIMARY KEY, body TEXT, order_id INTEGER REFERENCES orders(id))",
		"CREATE TABLE IF NOT EXISTS costs (id INTEGER PRIMARY KEY, cents INTEGER)",
	} {
		s.must(t, "POST", "/sqlite/shop/schema", map[string]string{"sql": sql}, s.key, 200)
	}
	s.must(t, "POST", "/sqlite/shop/execute", map[string]string{"sql": "INSERT INTO products(id,name,cents) VALUES(1,'tea',300),(2,'coffee',400)"}, s.key, 200)
	s.must(t, "POST", "/sqlite/shop/execute", map[string]string{"sql": "INSERT INTO costs(cents) VALUES(120)"}, s.key, 200)
	// The database default (private) covers a table with no entry.
	s.must(t, "GET", "/sqlite/shop/tables/costs/rows", nil, alice, 403)
	s.must(t, "GET", "/sqlite/shop/tables/costs/rows", nil, own, 200)
	s.must(t, "GET", "/sqlite/shop/tables/products/rows", nil, anon, 200)
	// Visitors never send SQL on a database with a matrix.
	for _, h := range []map[string]string{alice, anon, own} {
		for _, route := range []string{"query", "execute"} {
			s.must(t, "POST", "/sqlite/shop/"+route, map[string]string{"sql": "SELECT 1"}, h, 403)
		}
		s.must(t, "POST", "/sqlite/shop/schema", map[string]string{"sql": "CREATE TABLE x(v)"}, h, 403)
	}
	// Records: each person adds and sees their own; the owner on the site
	// sees all and sets a status.
	a1 := fmt.Sprint(s.must(t, "POST", "/sqlite/shop/tables/orders/rows", `{"product_id":1,"qty":2}`, alice, 200).json(t)["last_insert_id"])
	b1 := fmt.Sprint(s.must(t, "POST", "/sqlite/shop/tables/orders/rows", `{"product_id":2,"qty":1,"visitor_id":"forged","created_at":"1999"}`, bob, 200).json(t)["last_insert_id"])
	stamped := s.must(t, "POST", "/sqlite/shop/query", map[string]string{"sql": "SELECT visitor_id, created_at FROM orders WHERE id=" + b1}, s.key, 200)
	if !strings.Contains(string(stamped.body), s.ids["bob"]) || strings.Contains(string(stamped.body), "forged") || strings.Contains(string(stamped.body), "1999") {
		t.Fatalf("server stamps: %s", stamped.body)
	}
	s.must(t, "POST", "/sqlite/shop/tables/orders/rows", `{"product_id":99,"qty":1}`, alice, 404)
	s.must(t, "POST", "/sqlite/shop/tables/orders/rows", `{"qty":1}`, anon, 401)
	s.must(t, "GET", "/sqlite/shop/tables/orders/rows/"+b1, nil, alice, 404)
	s.must(t, "GET", "/sqlite/shop/tables/orders/rows/"+a1, nil, alice, 200)
	mine := s.must(t, "GET", "/sqlite/shop/tables/orders/rows", nil, alice, 200)
	if len(mine.json(t)["rows"].([]any)) != 1 {
		t.Fatalf("alice sees: %s", mine.body)
	}
	s.must(t, "PATCH", "/sqlite/shop/tables/orders/rows/"+a1, `{"status":"shipped"}`, alice, 403)
	s.must(t, "DELETE", "/sqlite/shop/tables/orders/rows/"+a1, nil, alice, 403)
	all := s.must(t, "GET", "/sqlite/shop/tables/orders/rows?order=id", nil, own, 200)
	if len(all.json(t)["rows"].([]any)) != 2 || !strings.Contains(string(all.body), s.ids["bob"]) {
		t.Fatalf("owner on site sees: %s", all.body)
	}
	s.must(t, "PATCH", "/sqlite/shop/tables/orders/rows/"+b1, `{"status":"shipped"}`, own, 200)
	edited := s.must(t, "GET", "/sqlite/shop/tables/orders/rows/"+b1, nil, bob, 200)
	if !strings.Contains(string(edited.body), "shipped") {
		t.Fatalf("status: %s", edited.body)
	}
	// Server-owned columns are never edited; an edit cannot move a row.
	for _, body := range []string{`{"visitor_id":"x"}`, `{"id":5}`, `{"created_at":"x"}`, `{"updated_at":"x"}`, `{"rowid":5}`, `{}`, `{"nope":1}`, `{"status":{"x":1}}`} {
		if r := s.do(t, "PATCH", "/sqlite/shop/tables/orders/rows/"+b1, body, own); r.status != 400 {
			t.Fatalf("edit %s: %d %s", body, r.status, r.body)
		}
	}
	// Equality filters on real columns only.
	f := s.must(t, "GET", "/sqlite/shop/tables/orders/rows?where.status=shipped", nil, own, 200)
	if len(f.json(t)["rows"].([]any)) != 1 {
		t.Fatalf("filter: %s", f.body)
	}
	for _, q := range []string{"where.nope=1", "where.status=a&where.qty=1&where.id=1&where.product_id=1", "where.visitor_id=" + s.ids["bob"], "order=visitor_id", "where." + url.QueryEscape("status = status OR 1") + "=1"} {
		s.must(t, "GET", "/sqlite/shop/tables/orders/rows?"+q, nil, alice, 400)
	}
	// A visitor may reference only rows they may read.
	s.must(t, "POST", "/sqlite/shop/tables/notes/rows", `{"body":"mine","order_id":`+a1+`}`, alice, 200)
	s.must(t, "POST", "/sqlite/shop/tables/notes/rows", `{"body":"theirs","order_id":`+b1+`}`, alice, 404)
	n1 := fmt.Sprint(s.must(t, "POST", "/sqlite/shop/tables/notes/rows", `{"body":"x"}`, alice, 200).json(t)["last_insert_id"])
	s.must(t, "PATCH", "/sqlite/shop/tables/notes/rows/"+n1, `{"order_id":`+b1+`}`, alice, 404)
	s.must(t, "PATCH", "/sqlite/shop/tables/notes/rows/"+n1, `{"body":"y"}`, bob, 404)
	s.must(t, "DELETE", "/sqlite/shop/tables/notes/rows/"+n1, nil, bob, 404)
	s.must(t, "PATCH", "/sqlite/shop/tables/notes/rows/"+n1, `{"body":"y"}`, alice, 200)
	s.must(t, "DELETE", "/sqlite/shop/tables/notes/rows/"+n1, nil, alice, 200)
	// A trigger or cascade that writes elsewhere refuses the visitor's change.
	s.must(t, "POST", "/sqlite/shop/schema", map[string]string{"sql": "CREATE TRIGGER notes_spy AFTER UPDATE ON notes BEGIN UPDATE orders SET status='hacked'; END"}, s.key, 200)
	n2 := fmt.Sprint(s.must(t, "POST", "/sqlite/shop/tables/notes/rows", `{"body":"x"}`, alice, 200).json(t)["last_insert_id"])
	s.must(t, "PATCH", "/sqlite/shop/tables/notes/rows/"+n2, `{"body":"z"}`, alice, 403)
	check := s.must(t, "POST", "/sqlite/shop/query", map[string]string{"sql": "SELECT count(*) FROM orders WHERE status='hacked'"}, s.key, 200)
	if !strings.Contains(string(check.body), "[[0]]") {
		t.Fatalf("trigger ran: %s", check.body)
	}
	// Pagination stays inside the caller's rows.
	for i := 0; i < 3; i++ {
		s.must(t, "POST", "/sqlite/shop/tables/orders/rows", `{"product_id":1,"qty":1}`, alice, 200)
	}
	after, count := "", 0
	for i := 0; i < 10; i++ {
		page := s.must(t, "GET", "/sqlite/shop/tables/orders/rows?limit=1&after="+url.QueryEscape(after), nil, alice, 200).json(t)
		count += len(page["rows"].([]any))
		if after = page["next_after"].(string); after == "" {
			break
		}
	}
	if count != 4 {
		t.Fatalf("alice pages: %d", count)
	}
}

func TestStoragePresetsOwnerOnSiteLimits(t *testing.T) {
	s := newPresetSite(t)
	own := s.callers["owner-site"]
	s.must(t, "PUT", "/resources/data", map[string]string{"kind": "kv", "preset": "private"}, s.key, 201)
	s.must(t, "PUT", "/resources/sealed", map[string]string{"kind": "kv", "preset": "custom", "read": "nobody", "add": "nobody", "edit": "nobody", "delete": "nobody"}, s.key, 201)
	s.must(t, "PUT", "/kv/data/keys/k", `{"value":1}`, own, 200)
	s.must(t, "GET", "/kv/data/keys/k", nil, own, 200)
	// nobody is the owner's tools only.
	s.must(t, "PUT", "/kv/sealed/keys/k", `{"value":1}`, own, 403)
	s.must(t, "PUT", "/kv/sealed/keys/k", `{"value":1}`, s.key, 200)
	s.must(t, "GET", "/kv/sealed/keys/k", nil, own, 403)
	// Storage data only: never settings, usage, visitor lookups, links or
	// deleting resources.
	s.must(t, "PUT", "/resources/data", map[string]string{"kind": "kv", "preset": "public"}, own, 403)
	s.must(t, "DELETE", "/resources/data", nil, own, 403)
	s.must(t, "GET", "/resources", nil, own, 403)
	s.must(t, "GET", "/usage", nil, own, 403)
	s.must(t, "GET", "/visitors?id="+s.ids["alice"], nil, own, 403)
	for _, path := range []string{"/v1/sites/shop", "/v1/sites/shop/passcode", "/v1/sites/shop/versions"} {
		if r := s.a.at(t, "GET", s.host, path, nil, own); r.status == 200 {
			t.Fatalf("%s from the page: %d %s", path, r.status, r.body)
		}
	}
	if r := s.a.at(t, "DELETE", s.host, "/v1/sites/shop", nil, own); r.status < 400 {
		t.Fatalf("delete site from the page: %d", r.status)
	}
	// CSRF and cross-origin requests never carry owner rights.
	s.must(t, "PUT", "/kv/data/keys/k", `{"value":2}`, browser(s.host, strings.TrimPrefix(own["Cookie"], visitorCookieHost+"="), "X-SH-CSRF", ""), 403)
	for _, h := range []map[string]string{
		browser(s.host, strings.TrimPrefix(own["Cookie"], visitorCookieHost+"="), "Origin", "https://evil.example", "Sec-Fetch-Site", "cross-site"),
		browser(s.host, strings.TrimPrefix(own["Cookie"], visitorCookieHost+"="), "Origin", "https://other.preset-owner."+pcSiteDomain, "Sec-Fetch-Site", "same-site"),
	} {
		if r := s.do(t, "GET", "/kv/data/keys/k", nil, h); r.status == 200 {
			t.Fatalf("cross-origin owner read: %d %s", r.status, r.body)
		}
	}
	// Another site's session (same owner) is not this site's.
	s.a.deploy(t, s.owner, "blog")
	blogID := s.a.siteID(t, s.owner, "blog")
	_, handle := s.a.userID(t, s.owner)
	blogHost := "blog." + handle + "." + pcSiteDomain
	other := s.a.session(t, s.owner, blogID, blogHost)
	s.must(t, "GET", "/kv/data/keys/k", nil, browser(s.host, other), 403)
	// A session for this site made on another host is not valid here.
	elsewhere := s.a.session(t, s.owner, s.sid, handle+"."+pcSiteDomain)
	s.must(t, "GET", "/kv/data/keys/k", nil, browser(s.host, elsewhere), 403)
	// On the person host the owner is a plain visitor.
	person := handle + "." + pcSiteDomain
	personCookie := s.a.session(t, s.owner, s.sid, person)
	if r := s.a.at(t, "GET", person, "/v1/sites/shop/storage/kv/data/keys/k", nil, browser(person, personCookie)); r.status == 200 {
		t.Fatalf("person host owner read: %d %s", r.status, r.body)
	}
	// A legacy resource gives no owner rights from the page.
	insertLegacyStorage(t, s.a.database, s.sid, "old", "sqlite", "owner", "anyone", "full", "")
	s.must(t, "POST", "/sqlite/old/query", map[string]string{"sql": "SELECT 1"}, own, 403)
	// SH.me() says when the page is the owner's.
	me := s.a.at(t, "GET", s.host, "/v1/sites/shop/me", nil, own).json(t)
	if me["site_owner"] != true {
		t.Fatalf("owner me: %v", me)
	}
	if me := s.a.at(t, "GET", s.host, "/v1/sites/shop/me", nil, s.callers["alice"]).json(t); me["site_owner"] != nil {
		t.Fatalf("visitor me: %v", me)
	}
}

func TestStoragePresetsKVAndFilesOwnership(t *testing.T) {
	s := newPresetSite(t)
	alice, bob := s.callers["alice"], s.callers["bob"]
	for _, kind := range []string{"kv", "files"} {
		s.must(t, "PUT", "/resources/wall-"+kind, map[string]string{"kind": kind, "preset": "wall"}, s.key, 201)
		s.must(t, "PUT", "/resources/board-"+kind, map[string]string{"kind": kind, "preset": "board"}, s.key, 201)
	}
	// KV: a board edit by someone else keeps the original author.
	s.must(t, "PUT", "/kv/board-kv/keys/item", `{"value":"alice's"}`, alice, 200)
	s.must(t, "PUT", "/kv/board-kv/keys/item", `{"value":"bob edited"}`, bob, 200)
	got := s.must(t, "GET", "/kv/board-kv/keys/item", nil, s.key, 200).json(t)
	if got["visitor_id"] != s.aliceID {
		t.Fatalf("author changed: %v", got)
	}
	if me := s.must(t, "GET", "/kv/board-kv/keys/item", nil, alice, 200).json(t); me["mine"] != true || me["visitor_id"] != nil {
		t.Fatalf("alice view: %v", me)
	}
	if them := s.must(t, "GET", "/kv/board-kv/keys/item", nil, bob, 200).json(t); them["mine"] != false {
		t.Fatalf("bob view: %v", them)
	}
	s.must(t, "DELETE", "/kv/board-kv/keys/item", nil, bob, 403)
	// Wall: authors take back their own post; others cannot.
	s.must(t, "PUT", "/kv/wall-kv/keys/post", `{"value":"hi"}`, alice, 200)
	s.must(t, "PUT", "/kv/wall-kv/keys/post", `{"value":"overwrite"}`, bob, 409)
	s.must(t, "DELETE", "/kv/wall-kv/keys/post", nil, bob, 404)
	s.must(t, "DELETE", "/kv/wall-kv/keys/post", nil, alice, 200)
	s.must(t, "PUT", "/files/wall-files/objects/photo.txt", "hi", alice, 200)
	s.must(t, "PUT", "/files/wall-files/objects/photo.txt", "overwrite", bob, 409)
	s.must(t, "DELETE", "/files/wall-files/objects/photo.txt", nil, bob, 404)
	list := s.must(t, "GET", "/files/wall-files/objects", nil, bob, 200)
	if !strings.Contains(string(list.body), `"mine":false`) || strings.Contains(string(list.body), s.aliceID) {
		t.Fatalf("bob list: %s", list.body)
	}
	if r := s.must(t, "GET", "/files/wall-files/objects/photo.txt", nil, s.callers["anon"], 200); string(r.body) != "hi" {
		t.Fatalf("anon read: %s", r.body)
	}
	s.must(t, "DELETE", "/files/wall-files/objects/photo.txt", nil, alice, 200)
}

func TestStoragePresetsSimpleHackKeepsItsRules(t *testing.T) {
	s := newPresetSite(t)
	hackMode = true
	t.Cleanup(func() { hackMode = false })
	r := s.must(t, "PUT", "/resources/team", map[string]string{"kind": "sqlite", "read": "anyone", "write": "signed-in"}, s.key, 201).json(t)
	if _, has := r["preset"]; has || r["write_mode"] != "full" {
		t.Fatalf("hack answer: %v", r)
	}
	s.must(t, "PUT", "/resources/team2", map[string]string{"kind": "kv", "preset": "wall"}, s.key, 400)
	s.must(t, "POST", "/sqlite/team/schema", map[string]string{"sql": "CREATE TABLE t (id INTEGER PRIMARY KEY, v TEXT)"}, s.key, 200)
	s.must(t, "POST", "/sqlite/team/execute", map[string]string{"sql": "INSERT INTO t(v) VALUES('x')"}, s.callers["alice"], 200)
	s.must(t, "GET", "/sqlite/team/tables/t/rows/1", nil, s.callers["alice"], 404)
	s.must(t, "DELETE", "/sqlite/team/tables/t/rows/1", nil, s.callers["alice"], 404)
}

func TestStoragePresetsCascadeIsRefused(t *testing.T) {
	s := newPresetSite(t)
	alice, bob := s.callers["alice"], s.callers["bob"]
	s.must(t, "PUT", "/resources/wall", map[string]string{"kind": "sqlite", "preset": "wall"}, s.key, 201)
	s.must(t, "POST", "/sqlite/wall/schema", map[string]string{"sql": "CREATE TABLE posts (id INTEGER PRIMARY KEY, body TEXT)"}, s.key, 200)
	s.must(t, "POST", "/sqlite/wall/schema", map[string]string{"sql": "CREATE TABLE replies (id INTEGER PRIMARY KEY, post_id INTEGER REFERENCES posts(id) ON DELETE CASCADE, body TEXT)"}, s.key, 200)
	p := fmt.Sprint(s.must(t, "POST", "/sqlite/wall/tables/posts/rows", `{"body":"hi"}`, alice, 200).json(t)["last_insert_id"])
	s.must(t, "POST", "/sqlite/wall/tables/replies/rows", `{"body":"bob says","post_id":`+p+`}`, bob, 200)
	// A foreign-key action that would delete Bob's reply is a write to
	// another table, so the page's delete is refused whole.
	s.must(t, "DELETE", "/sqlite/wall/tables/posts/rows/"+p, nil, alice, 403)
	left := s.must(t, "POST", "/sqlite/wall/query", map[string]string{"sql": "SELECT count(*) FROM replies"}, s.key, 200)
	if !strings.Contains(string(left.body), "[[1]]") {
		t.Fatalf("cascade ran: %s", left.body)
	}
}

// Fixes from the security review (2026-10-10).
func TestStoragePresetsReviewFixes(t *testing.T) {
	s := newPresetSite(t)
	alice, bob, own := s.callers["alice"], s.callers["bob"], s.callers["owner-site"]
	// A page wrote visitor_id freely on a legacy database; a later preset
	// never lets that forged id count as the victim's own.
	insertLegacyStorage(t, s.a.database, s.sid, "old", "sqlite", "anyone", "signed-in", "full", "")
	s.must(t, "POST", "/sqlite/old/schema", map[string]string{"sql": "CREATE TABLE orders (id INTEGER PRIMARY KEY, item TEXT, visitor_id TEXT)"}, s.key, 200)
	s.must(t, "POST", "/sqlite/old/execute", map[string]any{"sql": "INSERT INTO orders(item, visitor_id) VALUES('planted', ?)", "params": []any{s.aliceID}}, bob, 200)
	s.must(t, "PUT", "/resources/old", map[string]string{"kind": "sqlite", "preset": "board"}, s.key, 200)
	s.must(t, "PUT", "/resources/old", map[string]string{"kind": "sqlite", "preset": "personal"}, s.key, 200)
	if r := s.must(t, "GET", "/sqlite/old/tables/orders/rows", nil, alice, 200); strings.Contains(string(r.body), "planted") {
		t.Fatalf("forged row became alice's: %s", r.body)
	}
	// Table rules match SQLite's case-insensitive names; unknown tables warn.
	put := s.must(t, "PUT", "/resources/db", map[string]any{"kind": "sqlite", "preset": "public", "tables": map[string]any{"Orders": map[string]string{"preset": "records"}, "later": map[string]string{"preset": "private"}}}, s.key, 201).json(t)
	if !strings.Contains(fmt.Sprint(put["warnings"]), "later") || strings.Contains(fmt.Sprint(put["warnings"]), "orders") && !strings.Contains(fmt.Sprint(put["warnings"]), "orders does not") {
		t.Fatalf("warnings: %v", put["warnings"])
	}
	s.must(t, "POST", "/sqlite/db/schema", map[string]string{"sql": "CREATE TABLE orders (id INTEGER PRIMARY KEY, item TEXT)"}, s.key, 200)
	s.must(t, "POST", "/sqlite/db/tables/orders/rows", `{"item":"a1"}`, alice, 200)
	if r := s.must(t, "GET", "/sqlite/db/tables/orders/rows", nil, bob, 200); strings.Contains(string(r.body), "a1") {
		t.Fatalf("Orders rule did not apply to orders: %s", r.body)
	}
	// An older-field save cannot quietly keep table presets it cannot see.
	s.must(t, "PUT", "/resources/db", map[string]string{"kind": "sqlite", "read": "owner", "write": "owner"}, s.key, 409)
	// Renaming a table with its own preset would hand it the wider default.
	// (The schema route refuses ALTER TABLE ... RENAME outright today; the
	// 409 table_preset_rename guard stands behind that.)
	if r := s.do(t, "POST", "/sqlite/db/schema", map[string]string{"sql": "ALTER TABLE orders RENAME TO orders_v2"}, s.key); r.status == 200 {
		t.Fatalf("renamed a table with its own preset: %s", r.body)
	}
	note := s.must(t, "POST", "/sqlite/db/schema", map[string]string{"sql": "CREATE TABLE extra (id INTEGER PRIMARY KEY)"}, s.key, 200).json(t)
	if !strings.Contains(fmt.Sprint(note["notes"]), "extra") {
		t.Fatalf("no note for a new table on a wide default: %v", note)
	}
	// An own delete that cascades within the same table is refused whole.
	s.must(t, "PUT", "/resources/wall", map[string]string{"kind": "sqlite", "preset": "wall"}, s.key, 201)
	s.must(t, "POST", "/sqlite/wall/schema", map[string]string{"sql": "CREATE TABLE posts (id INTEGER PRIMARY KEY, parent INTEGER REFERENCES posts(id) ON DELETE CASCADE, body TEXT)"}, s.key, 200)
	p := fmt.Sprint(s.must(t, "POST", "/sqlite/wall/tables/posts/rows", `{"body":"top"}`, alice, 200).json(t)["last_insert_id"])
	s.must(t, "POST", "/sqlite/wall/tables/posts/rows", `{"body":"reply","parent":`+p+`}`, bob, 200)
	s.must(t, "DELETE", "/sqlite/wall/tables/posts/rows/"+p, nil, alice, 403)
	if r := s.must(t, "POST", "/sqlite/wall/query", map[string]string{"sql": "SELECT count(*) FROM posts"}, s.key, 200); !strings.Contains(string(r.body), "[[2]]") {
		t.Fatalf("cascade ran: %s", r.body)
	}
	// Pages the owner opens while signed in are never framed by another origin.
	page := s.a.at(t, "GET", s.host, "/", nil, map[string]string{"Cookie": own["Cookie"]})
	if !strings.Contains(page.header.Get("Content-Security-Policy"), "frame-ancestors 'self'") || page.header.Get("X-Frame-Options") != "SAMEORIGIN" {
		t.Fatalf("owner page frame headers: %v", page.header)
	}
	if page := s.a.at(t, "GET", s.host, "/", nil, map[string]string{"Cookie": alice["Cookie"]}); strings.Contains(page.header.Get("Content-Security-Policy"), "frame-ancestors") {
		t.Fatalf("visitor page got frame headers: %v", page.header)
	}
	// SH.me() says site_owner only for the strict same-origin session.
	if me := s.a.at(t, "GET", s.host, "/v1/sites/shop/me", nil, map[string]string{"Cookie": own["Cookie"], "Origin": "https://evil.example", "Sec-Fetch-Site": "cross-site"}).json(t); me["site_owner"] == true {
		t.Fatalf("cross-site me: %v", me)
	}
}

// Fixes from the second security review (2026-10-10).
func TestStoragePresetsReviewRound2(t *testing.T) {
	s := newPresetSite(t)
	bob, own := s.callers["bob"], s.callers["owner-site"]
	s.must(t, "PUT", "/resources/data", map[string]string{"kind": "kv", "preset": "private"}, s.key, 201)
	s.must(t, "PUT", "/kv/data/keys/k", `{"value":1}`, own, 200)
	// A page framed by another origin (auth.js says so) never has owner rights.
	framed := map[string]string{}
	for k, v := range own {
		framed[k] = v
	}
	framed["X-SH-Framed"] = "1"
	s.must(t, "GET", "/kv/data/keys/k", nil, framed, 403)
	// Every page answer varies by cookie, so a cached copy never drops the guard.
	if page := s.a.at(t, "GET", s.host, "/", nil, nil); !strings.Contains(strings.Join(page.header.Values("Vary"), ","), "Cookie") {
		t.Fatalf("no Vary: Cookie: %v", page.header)
	}
	// An older body without write fields cannot quietly keep table presets.
	s.must(t, "PUT", "/resources/db", map[string]any{"kind": "sqlite", "preset": "private", "tables": map[string]any{"orders": map[string]string{"preset": "records"}}}, s.key, 201)
	s.must(t, "PUT", "/resources/db", map[string]string{"kind": "sqlite", "read": "anyone"}, s.key, 409)
	// A legacy full-mode KV recorded whoever overwrote a key; the first preset
	// forgets those writers, so own never hands someone else's key over.
	insertLegacyStorage(t, s.a.database, s.sid, "oldkv", "kv", "anyone", "signed-in", "full", "")
	s.must(t, "PUT", "/kv/oldkv/keys/profile", `{"value":"bob wrote"}`, bob, 200)
	s.must(t, "PUT", "/resources/oldkv", map[string]string{"kind": "kv", "preset": "personal"}, s.key, 200)
	s.must(t, "GET", "/kv/oldkv/keys/profile", nil, bob, 404)
	if got := s.must(t, "GET", "/kv/oldkv/keys/profile", nil, s.key, 200).json(t); got["visitor_id"] != nil {
		t.Fatalf("writer kept: %v", got)
	}
}
