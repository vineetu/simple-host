package datatest

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"
	"testing"

	sqlite3 "github.com/ncruces/go-sqlite3"
	"github.com/vsriram/simple-host/host/access/auth"
	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
)

// The owner adds a row on behalf of a signed-in visitor (?for=), and the
// owner's people pick list (SIM-358).

func colIndex(cols []any, name string) int {
	for i, c := range cols {
		if c == name {
			return i
		}
	}
	return -1
}

func aliceEmailOf(t *testing.T, s Shop) string {
	t.Helper()
	var email string
	if err := s.A.Database.QueryRow(`SELECT username FROM users WHERE id = $1`, s.AliceID).Scan(&email); err != nil {
		t.Fatal(err)
	}
	return email
}

func entriesTable(t *testing.T, s Shop, rules string) string {
	t.Helper()
	s.Must(t, "PUT", "/resources/orders", StorageRules("sqlite", rules), s.Key, 201)
	s.Must(t, "POST", "/sqlite/orders/schema", map[string]string{"sql": "CREATE TABLE entries (id INTEGER PRIMARY KEY, note TEXT)"}, s.Key, 200)
	return "/sqlite/orders/tables/entries/rows"
}

func TestStorageOwnerAddsForVisitor(t *testing.T) {
	s := newShop(t)
	aliceEmail := aliceEmailOf(t, s)
	carol := s.A.NewPerson(t, "carol") // has an account, never signed in on the site

	// for only applies to a table whose read is own: a wall row would be
	// public and shown to the visitor as theirs.
	s.Must(t, "PUT", "/resources/board", StorageRules("sqlite", RulesWall), s.Key, 201)
	s.Must(t, "POST", "/sqlite/board/schema", map[string]string{"sql": "CREATE TABLE posts (id INTEGER PRIMARY KEY, note TEXT)"}, s.Key, 200)
	if r := s.Must(t, "POST", "/sqlite/board/tables/posts/rows?for="+s.AliceID, map[string]string{"note": "planted"}, s.Key, 403); r.Code() != "for_requires_own" {
		t.Fatalf("for on a wall table: %s", r.Body)
	}

	for _, preset := range []string{"records", "personal"} {
		name := "coach-" + preset
		s.Must(t, "PUT", "/resources/"+name, StorageRules("sqlite", map[string]string{"records": RulesRecords, "personal": RulesPersonal}[preset]), s.Key, 201)
		s.Must(t, "POST", "/sqlite/"+name+"/schema", map[string]string{"sql": "CREATE TABLE entries (id INTEGER PRIMARY KEY, note TEXT, created_at TEXT)"}, s.Key, 200)
		rows := "/sqlite/" + name + "/tables/entries/rows"

		if r := s.Must(t, "POST", rows+"?for="+s.AliceID, map[string]string{"note": "nope"}, s.Callers["bob"], 403); r.Code() != "forbidden" {
			t.Fatalf("%s: visitor for: %s", preset, r.Body)
		}
		if r := s.Must(t, "POST", rows+"?for="+carol.ID, map[string]string{"note": "x"}, s.Key, 404); r.Code() != "visitor_unknown" {
			t.Fatalf("%s: unknown visitor: %s", preset, r.Body)
		}

		// By id (key) and by email (owner signed in on the site).
		idByID := fmt.Sprint(s.Must(t, "POST", rows+"?for="+s.AliceID, map[string]string{"note": "A"}, s.Key, 200).JSON(t)["last_insert_id"])
		idByEmail := fmt.Sprint(s.Must(t, "POST", rows+"?for="+url.QueryEscape(aliceEmail), map[string]string{"note": "B"}, s.Callers["owner-site"], 200).JSON(t)["last_insert_id"])

		// A visitor cannot forge visitor_id or written_by.
		own := fmt.Sprint(s.Must(t, "POST", rows, map[string]any{"note": "mine", "visitor_id": s.OwnerID, "written_by": "owner"}, s.Callers["alice"], 200).JSON(t)["last_insert_id"])
		got := s.Must(t, "GET", rows+"/"+own, nil, s.Callers["alice"], 200).JSON(t)
		cols, row := got["columns"].([]any), got["row"].([]any)
		if row[colIndex(cols, "visitor_id")] != s.AliceID || row[colIndex(cols, "written_by")] != "" {
			t.Fatalf("%s: visitor forged columns: %v", preset, row)
		}

		for _, id := range []string{idByID, idByEmail} {
			got := s.Must(t, "GET", rows+"/"+id, nil, s.Callers["alice"], 200).JSON(t)
			cols, row := got["columns"].([]any), got["row"].([]any)
			if row[colIndex(cols, "visitor_id")] != s.AliceID || row[colIndex(cols, "written_by")] != "owner" || got["mine"] != true {
				t.Fatalf("%s: for row: %v", preset, got)
			}
			s.Must(t, "GET", rows+"/"+id, nil, s.Callers["bob"], 404)
			if r := s.Must(t, "PATCH", rows+"/"+id, map[string]any{"written_by": ""}, s.Key, 400); r.Code() != "server_column" {
				t.Fatalf("%s: owner edited written_by: %s", preset, r.Body)
			}
			// records: alice never edits or deletes it; personal: she may.
			status := 200
			if preset == "records" {
				status = 403
			}
			s.Must(t, "PATCH", rows+"/"+id, map[string]string{"note": "alice"}, s.Callers["alice"], status)
			s.Must(t, "PATCH", rows+"/"+id, map[string]string{"note": "owner"}, s.Key, 200)
			s.Must(t, "DELETE", rows+"/"+id, nil, s.Callers["alice"], status)
		}
	}
}

func TestStoragePeoplePickList(t *testing.T) {
	s := newShop(t)
	// carol signed in but saved nothing and is not a named viewer: not listed.
	carol := s.A.NewPerson(t, "carol")
	s.A.Session(t, carol, s.Sid, s.Host)
	if list, _ := s.Must(t, "GET", "/people", nil, s.Key, 200).JSON(t)["people"].([]any); len(list) != 0 {
		t.Fatalf("empty pick list: %v", list)
	}
	rows := entriesTable(t, s, RulesPersonal)
	s.Must(t, "POST", rows, map[string]string{"note": "hi"}, s.Callers["alice"], 200)
	s.Must(t, "GET", "/people", nil, s.Callers["alice"], 403)
	s.Must(t, "GET", "/people", nil, s.Callers["owner-site"], 200)

	// dana is a named viewer who never signed in: listed by email.
	dana := s.A.NewPerson(t, "dana")
	if _, err := accountsrepo.AddSiteViewer(context.Background(), s.A.Database, s.Sid, dana.Email); err != nil {
		t.Fatal(err)
	}
	list, _ := s.Must(t, "GET", "/people", nil, s.Key, 200).JSON(t)["people"].([]any)
	var alice, danaEntry map[string]any
	for _, raw := range list {
		p := raw.(map[string]any)
		if p["visitor_id"] == carol.ID {
			t.Fatalf("carol listed: %v", p)
		}
		if p["visitor_id"] == s.AliceID {
			alice = p
		}
		if p["email"] == dana.Email {
			danaEntry = p
		}
	}
	if alice == nil || alice["has_data"] != true || alice["signed_in"] != true {
		t.Fatalf("alice: %v", list)
	}
	if danaEntry == nil || danaEntry["named_viewer"] != true || danaEntry["signed_in"] == true {
		t.Fatalf("dana: %v", danaEntry)
	}
}

// for and the pick list are Host only: refused once a product storage
// binding provider is installed.
func TestStorageOwnerForGroupRefused(t *testing.T) {
	s := newShop(t)
	s.A.Data.SetStorageBindings(&ownerBindingFixture{})
	rows := entriesTable(t, s, RulesRecords)
	if r := s.Must(t, "POST", rows+"?for="+s.AliceID, map[string]string{"note": "nope"}, s.Key, 404); r.Code() != "for_not_available" {
		t.Fatalf("for on a product: %s", r.Body)
	}
	s.Must(t, "GET", "/people", nil, s.Key, 404)
	s.Must(t, "POST", rows, map[string]string{"note": "fine"}, s.Key, 200)
}

// The owner's raw SQL cannot forge written_by (it may still set visitor_id).
func TestStorageOwnerForRawSQLWrittenByBlocked(t *testing.T) {
	s := newShop(t)
	entriesTable(t, s, RulesRecords)
	for _, q := range []struct{ route, sql string }{
		{"/sqlite/orders/schema", "ALTER TABLE entries ADD COLUMN written_by TEXT"},
		{"/sqlite/orders/execute", "INSERT INTO entries(note, written_by) VALUES('x','owner')"},
		{"/sqlite/orders/execute", "UPDATE entries SET written_by = 'owner'"},
	} {
		if r := s.Must(t, "POST", q.route, map[string]string{"sql": q.sql}, s.Key, 400); r.Code() != "server_column" {
			t.Fatalf("%q: %s", q.sql, r.Body)
		}
	}
	s.Must(t, "POST", "/sqlite/orders/execute", map[string]string{"sql": "UPDATE entries SET visitor_id = NULL"}, s.Key, 200)
}

// A visitor_id the owner's raw SQL planted never puts that account's email
// on the pick list: only ids the server recorded as signed in on the site.
func TestStoragePeopleExcludesForgedVisitorID(t *testing.T) {
	s := newShop(t)
	carol := s.A.NewPerson(t, "carol")
	entriesTable(t, s, RulesRecords)
	s.Must(t, "POST", "/sqlite/orders/execute", map[string]string{"sql": "INSERT INTO entries(note, visitor_id) VALUES('planted', '" + carol.ID + "')"}, s.Key, 200)
	list, _ := s.Must(t, "GET", "/people", nil, s.Key, 200).JSON(t)["people"].([]any)
	for _, raw := range list {
		if p := raw.(map[string]any); p["visitor_id"] == carol.ID || p["email"] == carol.Email {
			t.Fatalf("forged visitor_id listed: %v", p)
		}
	}
}

// for by email never guesses between two signed-in people with the same
// canonical sign-in email.
func TestStorageOwnerForAmbiguousEmail(t *testing.T) {
	s := newShop(t)
	aliceEmail := aliceEmailOf(t, s)
	key, _ := auth.GenerateAPIKey()
	username := "carol-handle-" + key[4:12]
	carol, err := accountsrepo.CreateUser(context.Background(), s.A.Database, username, key, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = s.A.Database.Exec(`DELETE FROM users WHERE id = $1`, carol.ID) })
	if _, err := s.A.Database.Exec(`INSERT INTO oauth_identities(user_id, provider, provider_user_id, email, email_verified) VALUES ($1,'google',$2,$3,true)`, carol.ID, "fake-sub-"+carol.ID, aliceEmail); err != nil {
		t.Fatal(err)
	}
	s.A.Session(t, rt.Person{ID: carol.ID}, s.Sid, s.Host)
	rows := entriesTable(t, s, RulesRecords)
	if r := s.Must(t, "POST", rows+"?for="+url.QueryEscape(aliceEmail), map[string]string{"note": "x"}, s.Key, 400); r.Code() != "visitor_email_ambiguous" {
		t.Fatalf("ambiguous email: %s", r.Body)
	}
}

// A trigger that rewrites written_by is refused by the schema route, and
// one planted in the file directly makes a for= add roll back.
func TestStorageOwnerForTriggerTamperRolledBack(t *testing.T) {
	s := newShop(t)
	rows := entriesTable(t, s, RulesRecords)
	wipe := "CREATE TRIGGER wipe AFTER INSERT ON entries BEGIN UPDATE entries SET written_by = '' WHERE id = NEW.id; END"
	if r := s.Must(t, "POST", "/sqlite/orders/schema", map[string]string{"sql": wipe}, s.Key, 400); r.Code() != "server_column" {
		t.Fatalf("trigger through schema: %s", r.Body)
	}
	s.Must(t, "POST", rows+"?for="+s.AliceID, map[string]string{"note": "A"}, s.Key, 200)
	conn, err := sqlite3.Open(filepath.Join(s.A.Disk.SiteDir(s.OwnerID, "shop"), "runtime", "sqlite", "orders.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	err = conn.Exec(wipe)
	conn.Close()
	if err != nil {
		t.Fatal(err)
	}
	if r := s.Must(t, "POST", rows+"?for="+s.AliceID, map[string]string{"note": "tampered"}, s.Key, 409); r.Code() != "written_by_tampered" {
		t.Fatalf("tampered insert: %s", r.Body)
	}
	if got, _ := s.Must(t, "GET", rows, nil, s.Key, 200).JSON(t)["rows"].([]any); len(got) != 1 {
		t.Fatalf("tampered row not rolled back: %v", got)
	}
}
