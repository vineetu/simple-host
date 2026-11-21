package datatest

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	sqlite3 "github.com/ncruces/go-sqlite3"
	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/server/servertest"

	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
)

// SIM-358: the owner adds a row on behalf of a signed-in visitor, in terms
// of the existing preset/visitor_id model (records, personal), plus the
// owner's people pick list. The example used here (a coach adding a
// workout entry for a client) is only an example: the server concepts
// stay owner, visitor_id, and preset.

func colIndex(cols []any, name string) int {
	for i, c := range cols {
		if c == name {
			return i
		}
	}
	return -1
}

func aliceEmailOf(t *testing.T, s servertest.StorageTestSite) string {
	t.Helper()
	var email string
	if err := s.A.Database.QueryRow(`SELECT username FROM users WHERE id = $1`, s.AliceID).Scan(&email); err != nil {
		t.Fatal(err)
	}
	return email
}

func TestStorageOwnerAddsForVisitor(t *testing.T) {
	s := servertest.NewStorageTestSite(t)
	aliceEmail := aliceEmailOf(t, s)
	// carol has an account but has never signed in on this site: no
	// visitor identity here yet (no pre-sign-in email binding).
	carol := s.A.NewPerson(t, "preset-carol")
	carolID, _ := s.A.UserID(t, carol)

	// Review fix (blocking, both reviewers): for only ever applies to a
	// table whose read is own, checked on the table's actual matrix, not
	// on whether a visitor_id/written_by column happens to exist. A wall
	// table (read anyone) must refuse it before it ever touches schema or
	// data, so a row cannot be planted there, publicly readable and shown
	// to the named visitor as mine.
	s.Must(t, "PUT", "/resources/board", servertest.StorageRules("sqlite", servertest.RulesWall), s.Key, 201)
	s.Must(t, "POST", "/sqlite/board/schema", map[string]string{"sql": "CREATE TABLE posts (id INTEGER PRIMARY KEY, note TEXT)"}, s.Key, 200)
	if r := s.Must(t, "POST", "/sqlite/board/tables/posts/rows?for="+s.AliceID, map[string]string{"note": "planted"}, s.Key, 403); r.JSON(t)["code"] != "for_requires_own" {
		t.Fatalf("for on a wall table: %s", r.Body)
	}
	// Confirm nothing was planted: no row exists, publicly or for alice.
	if r := s.Must(t, "GET", "/sqlite/board/tables/posts/rows", nil, nil, 200); len(r.JSON(t)["rows"].([]any)) != 0 {
		t.Fatalf("wall row planted despite the refusal: %s", r.Body)
	}

	for _, preset := range []string{"records", "personal"} {
		name := "coach-" + preset
		s.Must(t, "PUT", "/resources/"+name, servertest.StorageRules("sqlite", map[string]string{"records": servertest.RulesRecords, "personal": servertest.RulesPersonal}[preset]), s.Key, 201)
		s.Must(t, "POST", "/sqlite/"+name+"/schema", map[string]string{"sql": "CREATE TABLE entries (id INTEGER PRIMARY KEY, note TEXT, created_at TEXT)"}, s.Key, 200)
		rows := "/sqlite/" + name + "/tables/entries/rows"

		// A visitor cannot use for, even naming themselves or another visitor.
		if r := s.Must(t, "POST", rows+"?for="+s.AliceID, map[string]string{"note": "nope"}, s.Callers["bob"], 403); r.JSON(t)["code"] != "forbidden" {
			t.Fatalf("%s: visitor for: %s", preset, r.Body)
		}

		// Refused for someone who has never signed in on this site: a clear
		// code, not a generic failure.
		if r := s.Must(t, "POST", rows+"?for="+carolID, map[string]string{"note": "for carol"}, s.Key, 404); r.JSON(t)["code"] != "visitor_unknown" {
			t.Fatalf("%s: unknown visitor: %s", preset, r.Body)
		}
		if r := s.Must(t, "POST", rows+"?for="+url.QueryEscape("never-"+aliceEmail), map[string]string{"note": "for nobody"}, s.Key, 404); r.JSON(t)["code"] != "visitor_unknown" {
			t.Fatalf("%s: unknown email: %s", preset, r.Body)
		}

		// The owner adds for alice: by id (API key/connector path) and by
		// email (the owner signed in on the site's own address, as
		// auth.js's table().add(values, {for}) would send). written_by is
		// added lazily, under the write lock, on this very first use — an
		// existing table works without the owner resaving its preset.
		byID := s.Must(t, "POST", rows+"?for="+s.AliceID, map[string]string{"note": "program A"}, s.Key, 200)
		idByID := fmt.Sprint(byID.JSON(t)["last_insert_id"])
		byEmail := s.Must(t, "POST", rows+"?for="+url.QueryEscape(aliceEmail), map[string]string{"note": "program B"}, s.Callers["owner-site"], 200)
		idByEmail := fmt.Sprint(byEmail.JSON(t)["last_insert_id"])

		// A visitor cannot set visitor_id or written_by directly: the
		// server stamps its own values regardless of what is sent.
		added := s.Must(t, "POST", rows, map[string]any{"note": "mine", "visitor_id": s.OwnerID, "written_by": "owner"}, s.Callers["alice"], 200)
		ownID := fmt.Sprint(added.JSON(t)["last_insert_id"])
		got := s.Must(t, "GET", rows+"/"+ownID, nil, s.Callers["alice"], 200)
		cols, row := got.JSON(t)["columns"].([]any), got.JSON(t)["row"].([]any)
		if row[colIndex(cols, "visitor_id")] != s.AliceID {
			t.Fatalf("%s: visitor forged visitor_id: %s", preset, got.Body)
		}
		if row[colIndex(cols, "written_by")] != "" {
			t.Fatalf("%s: visitor forged written_by: %s", preset, got.Body)
		}

		for _, id := range []string{idByID, idByEmail} {
			// The target visitor sees it, marked as written by the owner —
			// never looking like their own.
			got = s.Must(t, "GET", rows+"/"+id, nil, s.Callers["alice"], 200)
			cols, row = got.JSON(t)["columns"].([]any), got.JSON(t)["row"].([]any)
			if row[colIndex(cols, "visitor_id")] != s.AliceID {
				t.Fatalf("%s: wrong visitor_id: %s", preset, got.Body)
			}
			if row[colIndex(cols, "written_by")] != "owner" {
				t.Fatalf("%s: not marked owner-written: %s", preset, got.Body)
			}
			if got.JSON(t)["mine"] != true {
				t.Fatalf("%s: not shown as alice's own read: %s", preset, got.Body)
			}
			// Another visitor cannot see it (own: 404, like a missing row).
			s.Must(t, "GET", rows+"/"+id, nil, s.Callers["bob"], 404)
			// written_by can never be edited either, by anyone, on any
			// preset: it is set once, at insert, never after.
			if r := s.Must(t, "PATCH", rows+"/"+id, map[string]any{"written_by": ""}, s.Key, 400); r.JSON(t)["code"] != "server_column" {
				t.Fatalf("%s: owner edited written_by: %s", preset, r.Body)
			}
			if preset == "personal" {
				if r := s.Must(t, "PATCH", rows+"/"+id, map[string]any{"written_by": "owner"}, s.Callers["alice"], 400); r.JSON(t)["code"] != "server_column" {
					t.Fatalf("%s: visitor edited written_by: %s", preset, r.Body)
				}
			}
			// records: alice reads it, never edits or deletes it. personal: she may.
			changeStatus := 200
			if preset == "records" {
				changeStatus = 403
			}
			s.Must(t, "PATCH", rows+"/"+id, map[string]string{"note": "changed by alice"}, s.Callers["alice"], changeStatus)
			// Whatever alice could not do, the owner still can (checked
			// before the delete below, which would otherwise remove the
			// row this is about to edit).
			s.Must(t, "PATCH", rows+"/"+id, map[string]string{"note": "owner edit"}, s.Key, 200)
			s.Must(t, "DELETE", rows+"/"+id, nil, s.Callers["alice"], changeStatus)
		}
	}
}

func TestStorageOwnerForDoesNotNotify(t *testing.T) {
	s, x := notificationFixture(t)
	subscribeNotification(t, s, x)
	s.Must(t, "PUT", "/resources/orders", servertest.StorageRules("sqlite", servertest.RulesRecords), s.Key, 201)
	waitNotificationEmits(t, s)
	s.Must(t, "POST", "/sqlite/orders/schema", map[string]string{"sql": "CREATE TABLE orders(id INTEGER PRIMARY KEY, item TEXT, created_at TEXT)"}, s.Key, 200)
	waitNotificationEmits(t, s)
	ctx, cancel := context.WithCancel(context.Background())
	s.A.Data.StartNotifications(ctx)
	t.Cleanup(func() { cancel(); s.A.Data.WaitNotifications() })

	rows := "/sqlite/orders/tables/orders/rows"
	// Owner adds for alice, by API key: never notified (c.Owner already
	// never notifies, unchanged).
	s.Must(t, "POST", rows+"?for="+s.AliceID, map[string]string{"item": "owner key, for alice"}, s.Key, 200)
	waitNotificationEmits(t, s)
	// Owner adds for alice, signed in on the site's own address: also never
	// notified — the new case this ticket must not regress (an ordinary
	// owner-on-site add, with no for, does still notify; see record.added
	// == 1 below, from alice's own add, not from this one).
	s.Must(t, "POST", rows+"?for="+s.AliceID, map[string]string{"item": "owner on site, for alice"}, s.Callers["owner-site"], 200)
	waitNotificationEmits(t, s)
	// A plain visitor add still notifies (sanity: suppression is specific
	// to the owner, not a regression that silences everyone).
	s.Must(t, "POST", rows, map[string]string{"item": "alice's own order"}, s.Callers["alice"], 200)
	waitNotificationEmits(t, s)

	deadline := time.Now().Add(5 * time.Second)
	for len(x.snapshot()) < 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	got := x.snapshot()
	if len(got) != 1 {
		t.Fatalf("want exactly 1 delivered event (alice's own add only), got %d: %+v", len(got), got)
	}
	if got[0].body["name"] != "record.added" || got[0].body["data"].(map[string]any)["visitor"] != s.AliceID {
		t.Fatalf("unexpected event: %v", got[0].body)
	}
}

func TestStoragePeoplePickList(t *testing.T) {
	s := servertest.NewStorageTestSite(t)
	// carol signs in on the site (has an identity) but saves nothing and is
	// not a named viewer: the privacy rule excludes her from the list.
	carol := s.A.NewPerson(t, "preset-carol")
	carolID, _ := s.A.UserID(t, carol)
	s.A.Session(t, carol, s.Sid, s.Host)

	// carol has signed in but saved nothing and is not a named viewer: the
	// pick list lists nobody yet, not everyone who ever merely signed in.
	empty := s.Must(t, "GET", "/people", nil, s.Key, 200)
	if list, _ := empty.JSON(t)["people"].([]any); len(list) != 0 {
		t.Fatalf("empty pick list: %s", empty.Body)
	}

	// alice gets saved data (the row_write test above exercises the write
	// path; here a direct add is enough).
	s.Must(t, "PUT", "/resources/notes", servertest.StorageRules("sqlite", servertest.RulesPersonal), s.Key, 201)
	s.Must(t, "POST", "/sqlite/notes/schema", map[string]string{"sql": "CREATE TABLE entries(id INTEGER PRIMARY KEY, note TEXT)"}, s.Key, 200)
	s.Must(t, "POST", "/sqlite/notes/tables/entries/rows", map[string]string{"note": "hi"}, s.Callers["alice"], 200)

	// A visitor cannot read the pick list.
	if r := s.Must(t, "GET", "/people", nil, s.Callers["alice"], 403); r.JSON(t)["code"] != "forbidden" {
		t.Fatalf("visitor pick list: %s", r.Body)
	}
	// The owner, signed in on the site, may too (not only the API key).
	s.Must(t, "GET", "/people", nil, s.Callers["owner-site"], 200)

	// dana is a named viewer who has not signed in yet: listed, by email,
	// not yet a usable for target (she has no visitor identity here).
	// Granting the first named viewer locks the site to named viewers and
	// the owner, so this happens last, after alice's data is already in.
	dana := s.A.NewPerson(t, "preset-dana")
	if r := s.A.At(t, "POST", servertest.PcSiteDomain, "/v1/sites/shop/viewers", map[string]any{"emails": []string{dana.Email}}, s.Key); r.Status != 200 {
		t.Fatalf("grant viewer: %d %s", r.Status, r.Body)
	}

	list, _ := s.Must(t, "GET", "/people", nil, s.Key, 200).JSON(t)["people"].([]any)
	byID := map[string]map[string]any{}
	var danaEntry map[string]any
	for _, raw := range list {
		p := raw.(map[string]any)
		if id, _ := p["visitor_id"].(string); id != "" {
			byID[id] = p
			if id == carolID {
				t.Fatalf("carol (signed in, no data, not a named viewer) listed: %v", p)
			}
		}
		if p["email"] == dana.Email {
			danaEntry = p
		}
	}
	alice, ok := byID[s.AliceID]
	if !ok || alice["has_data"] != true || alice["signed_in"] != true {
		t.Fatalf("alice not listed with data: %v", byID)
	}
	if danaEntry == nil || danaEntry["named_viewer"] != true || danaEntry["signed_in"] == true {
		t.Fatalf("dana (named viewer, never signed in) not listed correctly: %v", danaEntry)
	}
}

// TestStorageOwnerForGroupRefused: for, and the people pick list, are Host
// only: named viewers and the canonical-email resolution behind for are not
// a product concept, so both are refused once a product storage binding
// provider is installed.
func TestStorageOwnerForGroupRefused(t *testing.T) {
	s := servertest.NewStorageTestSite(t)
	s.A.Data.SetStorageBindings(&ownerBindingFixture{})
	s.Must(t, "PUT", "/resources/orders", servertest.StorageRules("sqlite", servertest.RulesRecords), s.Key, 201)
	s.Must(t, "POST", "/sqlite/orders/schema", map[string]string{"sql": "CREATE TABLE entries (id INTEGER PRIMARY KEY, note TEXT)"}, s.Key, 200)
	if r := s.Must(t, "POST", "/sqlite/orders/tables/entries/rows?for="+s.AliceID, map[string]string{"note": "nope"}, s.Key, 404); r.JSON(t)["code"] != "for_not_available" {
		t.Fatalf("for on Simple Hack: %s", r.Body)
	}
	if r := s.Must(t, "GET", "/people", nil, s.Key, 404); r.Status != 404 {
		t.Fatalf("people pick list on Simple Hack: %s", r.Body)
	}
	// An ordinary add, with no for, is unaffected.
	s.Must(t, "POST", "/sqlite/orders/tables/entries/rows", map[string]string{"note": "fine"}, s.Key, 200)
}

// TestStorageOwnerForRawSQLWrittenByBlocked: the owner's raw SQL console
// (storage_sql_execute, pre-existing and otherwise unrestricted — it can
// still set visitor_id directly, documented, left as is) must not be able
// to forge written_by. Review fix item 9.
func TestStorageOwnerForRawSQLWrittenByBlocked(t *testing.T) {
	s := servertest.NewStorageTestSite(t)
	s.Must(t, "PUT", "/resources/orders", servertest.StorageRules("sqlite", servertest.RulesRecords), s.Key, 201)
	// written_by is server-owned (never declared, the same as visitor_id);
	// the schema route refuses even naming it, whether or not it exists.
	s.Must(t, "POST", "/sqlite/orders/schema", map[string]string{"sql": "CREATE TABLE entries (id INTEGER PRIMARY KEY, note TEXT)"}, s.Key, 200)
	if r := s.Must(t, "POST", "/sqlite/orders/schema", map[string]string{"sql": "ALTER TABLE entries ADD COLUMN written_by TEXT"}, s.Key, 400); r.JSON(t)["code"] != "server_column" {
		t.Fatalf("schema declaring written_by: %s", r.Body)
	}
	r := s.Must(t, "POST", "/sqlite/orders/tables/entries/rows", map[string]string{"note": "seed"}, s.Callers["alice"], 200)
	id := fmt.Sprint(r.JSON(t)["last_insert_id"])
	for _, sql := range []string{
		"INSERT INTO entries(note, written_by) VALUES('x','owner')",
		"UPDATE entries SET written_by = 'owner' WHERE id = " + id,
	} {
		if r := s.Must(t, "POST", "/sqlite/orders/execute", map[string]string{"sql": sql}, s.Key, 400); r.JSON(t)["code"] != "server_column" {
			t.Fatalf("raw SQL written_by %q: %s", sql, r.Body)
		}
	}
	// visitor_id is pre-existing, documented, unrestricted raw-SQL
	// behaviour: unaffected by this fix.
	s.Must(t, "POST", "/sqlite/orders/execute", map[string]string{"sql": "UPDATE entries SET visitor_id = NULL WHERE id = " + id}, s.Key, 200)
}

// TestStoragePeopleExcludesForgedVisitorID: a SQLite row's visitor_id can
// be whatever the owner's raw storage_sql_execute wrote, including another
// account's real id entirely. The pick list must never resolve or list
// that id's email on the strength of a table's own content: only an id the
// server itself recorded as signed in on this site. Review fix item 3.
func TestStoragePeopleExcludesForgedVisitorID(t *testing.T) {
	s := servertest.NewStorageTestSite(t)
	// carol has a real account but has never signed in on this site.
	carol := s.A.NewPerson(t, "preset-carol")
	carolID, _ := s.A.UserID(t, carol)

	s.Must(t, "PUT", "/resources/orders", servertest.StorageRules("sqlite", servertest.RulesRecords), s.Key, 201)
	s.Must(t, "POST", "/sqlite/orders/schema", map[string]string{"sql": "CREATE TABLE entries (id INTEGER PRIMARY KEY, note TEXT)"}, s.Key, 200)
	// The owner's raw SQL plants carol's real account id as visitor_id,
	// without her ever having signed in here.
	s.Must(t, "POST", "/sqlite/orders/execute", map[string]string{"sql": "INSERT INTO entries(note, visitor_id) VALUES('planted', '" + carolID + "')"}, s.Key, 200)

	list, _ := s.Must(t, "GET", "/people", nil, s.Key, 200).JSON(t)["people"].([]any)
	for _, raw := range list {
		p := raw.(map[string]any)
		if p["visitor_id"] == carolID || p["email"] == carol.Email {
			t.Fatalf("forged visitor_id resolved and listed: %v", p)
		}
	}
}

// TestStorageOwnerForAmbiguousEmail: for by email must resolve against the
// same canonical sign-in email storage_visitor_emails/SH.me() use, among
// people the server recorded as signed in on this site — not a raw
// username or OAuth-identity row match alone, and never a guess among more
// than one match. Review fix item 2.
func TestStorageOwnerForAmbiguousEmail(t *testing.T) {
	s := servertest.NewStorageTestSite(t)
	aliceEmail := aliceEmailOf(t, s)

	// carol's own username does not look like an email, so her canonical
	// sign-in email comes from her OAuth identity, not her username —
	// which this test sets to alice's address, on purpose, to make the two
	// signed-in accounts resolve to the same email.
	key, _ := auth.GenerateAPIKey()
	username := "carol-handle-" + key[4:12]
	carol, err := accountsrepo.CreateUser(context.Background(), s.A.Database, username, key, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = s.A.Database.Exec(`DELETE FROM users WHERE id = $1`, carol.ID) })
	if _, err := s.A.Database.Exec(`UPDATE users SET handle = $1 WHERE id = $2`, username, carol.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.A.Database.Exec(`INSERT INTO oauth_identities(user_id, provider, provider_user_id, email, email_verified) VALUES ($1,'google',$2,$3,true)`, carol.ID, "fake-sub-"+carol.ID, aliceEmail); err != nil {
		t.Fatal(err)
	}
	s.A.Session(t, servertest.Person{Email: username, Key: key}, s.Sid, s.Host)

	s.Must(t, "PUT", "/resources/orders", servertest.StorageRules("sqlite", servertest.RulesRecords), s.Key, 201)
	s.Must(t, "POST", "/sqlite/orders/schema", map[string]string{"sql": "CREATE TABLE entries (id INTEGER PRIMARY KEY, note TEXT)"}, s.Key, 200)
	if r := s.Must(t, "POST", "/sqlite/orders/tables/entries/rows?for="+url.QueryEscape(aliceEmail), map[string]string{"note": "ambiguous"}, s.Key, 400); r.JSON(t)["code"] != "visitor_email_ambiguous" {
		t.Fatalf("ambiguous email: %s", r.Body)
	}
}

// TestStorageOwnerForTriggerTamperRolledBack: review round 2, item A. A
// trigger that rewrites visitor_id or written_by as a side effect of a
// for= insert must not ship a row that looks like the visitor's own.
// Confirms both halves of the fix: the schema route refuses to create such
// a trigger at all (closing the route this ticket controls), and, for one
// that reached the table some other way (simulated here by writing it
// directly to the site's SQLite file, bypassing that route entirely, since
// a plain for= add's INSERT runs with no authorizer for the owner and so
// cannot itself observe what a trigger does), the insert is verified and
// rolled back rather than committed.
func TestStorageOwnerForTriggerTamperRolledBack(t *testing.T) {
	s := servertest.NewStorageTestSite(t)
	s.Must(t, "PUT", "/resources/orders", servertest.StorageRules("sqlite", servertest.RulesRecords), s.Key, 201)
	s.Must(t, "POST", "/sqlite/orders/schema", map[string]string{"sql": "CREATE TABLE entries (id INTEGER PRIMARY KEY, note TEXT)"}, s.Key, 200)

	// The schema route itself refuses a trigger naming written_by,
	// whether or not the column exists yet.
	wipeTrigger := "CREATE TRIGGER wipe AFTER INSERT ON entries BEGIN UPDATE entries SET written_by = '' WHERE id = NEW.id; END"
	if r := s.Must(t, "POST", "/sqlite/orders/schema", map[string]string{"sql": wipeTrigger}, s.Key, 400); r.JSON(t)["code"] != "server_column" {
		t.Fatalf("trigger naming written_by through schema: %s", r.Body)
	}

	// A first, ordinary for= add succeeds and brings written_by into
	// existence (lazily, under the write lock); EnsureWrittenByColumn's
	// trigger/view preflight only ever runs once, when the column does
	// not exist yet, so it cannot see a trigger installed afterward.
	first := s.Must(t, "POST", "/sqlite/orders/tables/entries/rows?for="+s.AliceID, map[string]string{"note": "program A"}, s.Key, 200)
	firstID := fmt.Sprint(first.JSON(t)["last_insert_id"])

	// Simulate a trigger that reached the table some other way than this
	// ticket's own, now-refused, schema route (an older binary, a
	// restored backup): written directly to the site's SQLite file.
	path := filepath.Join(s.A.Sites.Disk.SiteDir(s.OwnerID, "shop"), "runtime", "sqlite", "orders.sqlite")
	conn, err := sqlite3.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Exec(wipeTrigger); err != nil {
		conn.Close()
		t.Fatal(err)
	}
	conn.Close()

	// A second for= add now fires that trigger as a side effect of its own
	// INSERT; the server notices the row no longer matches what it
	// intended and rolls the whole add back instead of shipping it.
	if r := s.Must(t, "POST", "/sqlite/orders/tables/entries/rows?for="+s.AliceID, map[string]string{"note": "tampered"}, s.Key, 409); r.JSON(t)["code"] != "written_by_tampered" {
		t.Fatalf("tampered insert: %s", r.Body)
	}

	// Rolled back: only the first, untampered row exists; nothing
	// visitor-looking from the tampered attempt.
	rows, _ := s.Must(t, "GET", "/sqlite/orders/tables/entries/rows", nil, s.Key, 200).JSON(t)["rows"].([]any)
	if len(rows) != 1 {
		t.Fatalf("tampered row not rolled back: %v", rows)
	}
	got := s.Must(t, "GET", "/sqlite/orders/tables/entries/rows/"+firstID, nil, s.Callers["alice"], 200)
	if got.JSON(t)["row"].([]any)[colIndex(got.JSON(t)["columns"].([]any), "written_by")] != "owner" {
		t.Fatalf("first row's own written_by disturbed: %s", got.Body)
	}
}
