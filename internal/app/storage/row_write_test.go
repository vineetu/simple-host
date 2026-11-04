package storage

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	sqlite3 "github.com/ncruces/go-sqlite3"
	rules "github.com/vsriram/simple-host/internal/rules/storage"
)

func TestRowReferenceError(t *testing.T) {
	if err := rowReferenceError(404, nil); err != nil {
		t.Fatalf("successful check: %v", err)
	}
	for _, tc := range []struct {
		status        int
		code, message string
	}{
		{404, "invalid_reference", "referenced row not found"},
		{400, "invalid_rows", "cannot check references"},
	} {
		err := rowReferenceError(tc.status, errors.New("reference check failed"))
		got, ok := err.(*Error)
		if !ok || got.Code != tc.code || got.Message != tc.message {
			t.Fatalf("status %d: %v", tc.status, err)
		}
	}
}

func TestRunTableWrite(t *testing.T) {
	conn, err := sqlite3.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.Exec(`CREATE TABLE notes (id INTEGER PRIMARY KEY, body TEXT UNIQUE)`); err != nil {
		t.Fatal(err)
	}
	id, err := runTableWrite(conn, "notes", false, sqlite3.AUTH_INSERT, `INSERT INTO notes(body) VALUES (?) RETURNING id`, []json.RawMessage{json.RawMessage(`"hello"`)}, true)
	if err != nil || id != 1 {
		t.Fatalf("insert: %d, %v", id, err)
	}
	_, err = runTableWrite(conn, "notes", false, sqlite3.AUTH_INSERT, `INSERT INTO notes(body) VALUES (?) RETURNING id`, []json.RawMessage{json.RawMessage(`"hello"`)}, true)
	if e, ok := err.(*Error); !ok || e.Code != "row_conflict" {
		t.Fatalf("duplicate: %v", err)
	}
	_, err = runTableWrite(conn, "notes", false, sqlite3.AUTH_INSERT, `INSERT INTO notes(body) VALUES (?) RETURNING id`, nil, true)
	if e, ok := err.(*Error); !ok || e.Code != "invalid_rows" {
		t.Fatalf("bind: %v", err)
	}
}

func TestRowMayRead(t *testing.T) {
	tests := []struct {
		call    Call
		policy  string
		filter  string
		allowed bool
	}{
		{Call{}, rules.WhoOwn, "", false},
		{Call{VisitorID: "v"}, rules.WhoOwn, "v", true},
		{Call{VisitorID: "v", OwnerOnSite: true}, rules.WhoOwn, "", true},
		{Call{}, rules.WhoChecked, "", true},
		{Call{}, rules.WhoOwner, "", false},
		{Call{Owner: true}, rules.WhoOwner, "", true},
	}
	for _, tt := range tests {
		filter, allowed := rowMayRead(tt.call, tt.policy)
		if filter != tt.filter || allowed != tt.allowed {
			t.Errorf("%+v %q: got %q, %v", tt.call, tt.policy, filter, allowed)
		}
	}
}

func TestCheckRowReferencesVisibility(t *testing.T) {
	conn, err := sqlite3.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.Exec(`CREATE TABLE parent (id INTEGER PRIMARY KEY, visitor_id TEXT);
		CREATE TABLE child (id INTEGER PRIMARY KEY, parent_id INTEGER REFERENCES parent(id));
		INSERT INTO parent VALUES (1, 'other'), (2, 'visitor');
		INSERT INTO child VALUES (1, 1), (2, 2)`); err != nil {
		t.Fatal(err)
	}
	matrix := rules.AccessMatrix{Read: rules.WhoOwn}
	c := Call{VisitorID: "visitor", Resource: rules.Resource{Access: &matrix}}
	if err := checkRowReferences(conn, c, "child", "id", 2); err != nil {
		t.Fatalf("own parent: %v", err)
	}
	if err := checkRowReferences(conn, c, "child", "id", 1); err == nil || err.(*Error).Code != "invalid_reference" {
		t.Fatalf("other parent: %v", err)
	}
}

func TestFinishTableWrite(t *testing.T) {
	conn, err := sqlite3.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var events []string
	s := New(nil, Deps{Committed: func(context.Context) { events = append(events, "metric") }, MarkChanged: func() { events = append(events, "changed") }})
	if err := s.finishTableWrite(conn, Call{}, func() error { return errors.New("commit failed") }); err == nil || err.(*Error).Code != "invalid_rows" || len(events) != 0 {
		t.Fatalf("failed commit: error %v, events %v", err, events)
	}
	if err := s.finishTableWrite(conn, Call{}, func() error { return nil }); err != nil || len(events) != 2 || events[0] != "metric" || events[1] != "changed" {
		t.Fatalf("successful commit: error %v, events %v", err, events)
	}
}

func TestAddTableRowCommitAndRollback(t *testing.T) {
	conn, err := sqlite3.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.Exec(`CREATE TABLE parent (id INTEGER PRIMARY KEY, visitor_id TEXT);
		CREATE TABLE child (id INTEGER PRIMARY KEY, visitor_id TEXT, parent_id INTEGER REFERENCES parent(id));
		INSERT INTO parent VALUES (1, 'other'), (2, 'visitor')`); err != nil {
		t.Fatal(err)
	}
	marks := 0
	s := New(nil, Deps{MarkChanged: func() { marks++ }})
	c := Call{VisitorID: "visitor", Resource: rules.Resource{Access: &rules.AccessMatrix{Read: rules.WhoOwn}}}
	meta, err := tableMetadata(conn, c, "child", "")
	if err != nil {
		t.Fatal(err)
	}
	in := TableInput{Table: "child", Values: map[string]json.RawMessage{"parent_id": json.RawMessage(`1`)}}
	if _, err := s.addTableRow(conn, c, in, meta); err == nil || err.(*Error).Code != "invalid_reference" || marks != 0 {
		t.Fatalf("inaccessible parent: %v, marks %d", err, marks)
	}
	in.Values = map[string]json.RawMessage{"parent_id": json.RawMessage(`2`)}
	id, err := s.addTableRow(conn, c, in, meta)
	if err != nil || id != 1 || marks != 1 {
		t.Fatalf("accessible parent: id %d, error %v, marks %d", id, err, marks)
	}
}

func TestAddTableRowRejectsClientIDWithoutWriting(t *testing.T) {
	conn, err := sqlite3.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.Exec(`CREATE TABLE notes (id INTEGER PRIMARY KEY, visitor_id TEXT, body TEXT)`); err != nil {
		t.Fatal(err)
	}
	marks := 0
	s := New(nil, Deps{MarkChanged: func() { marks++ }})
	c := Call{VisitorID: "visitor"}
	meta, err := tableMetadata(conn, c, "notes", "")
	if err != nil {
		t.Fatal(err)
	}
	in := TableInput{Table: "notes", Values: map[string]json.RawMessage{"id": json.RawMessage(`42`), "body": json.RawMessage(`"hello"`)}}
	if _, err := s.addTableRow(conn, c, in, meta); err == nil || err.(*Error).Message != "the server assigns ids" {
		t.Fatalf("client id: %v", err)
	}
	if marks != 0 || conn.TotalChanges() != 0 {
		t.Fatalf("rejected insert changed data: marks %d, changes %d", marks, conn.TotalChanges())
	}
}

func TestAddTableRowRejectsTriggerWritingAnotherTable(t *testing.T) {
	conn, err := sqlite3.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.Exec(`CREATE TABLE notes (id INTEGER PRIMARY KEY, body TEXT);
		CREATE TABLE audit (body TEXT);
		CREATE TRIGGER audit_note AFTER INSERT ON notes BEGIN INSERT INTO audit VALUES (new.body); END`); err != nil {
		t.Fatal(err)
	}
	marks := 0
	s := New(nil, Deps{MarkChanged: func() { marks++ }})
	c := Call{VisitorID: "visitor"}
	meta, err := tableMetadata(conn, c, "notes", "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.addTableRow(conn, c, TableInput{Table: "notes", Values: map[string]json.RawMessage{"body": json.RawMessage(`"hello"`)}}, meta)
	if e, ok := err.(*Error); !ok || e.Code != "forbidden" || marks != 0 || conn.TotalChanges() != 0 {
		t.Fatalf("trigger insert: error %v, marks %d, changes %d", err, marks, conn.TotalChanges())
	}
}

func TestAddTableRowOwnerMayUseTrigger(t *testing.T) {
	conn, err := sqlite3.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.Exec(`CREATE TABLE notes (id INTEGER PRIMARY KEY, body TEXT);
		CREATE TABLE audit (body TEXT);
		CREATE TRIGGER audit_note AFTER INSERT ON notes BEGIN INSERT INTO audit VALUES (new.body); END`); err != nil {
		t.Fatal(err)
	}
	marks := 0
	s := New(nil, Deps{MarkChanged: func() { marks++ }})
	c := Call{Owner: true}
	meta, err := tableMetadata(conn, c, "notes", "")
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.addTableRow(conn, c, TableInput{Table: "notes", Values: map[string]json.RawMessage{"body": json.RawMessage(`"hello"`)}}, meta)
	if err != nil || id != 1 || marks != 1 || conn.TotalChanges() != 2 {
		t.Fatalf("owner trigger insert: id %d, error %v, marks %d, changes %d", id, err, marks, conn.TotalChanges())
	}
}

func TestTableWriteAuthorizesBeforeReplay(t *testing.T) {
	s := New(nil, Deps{PasscodeAllows: func() bool { return false }})
	c := Call{Resource: rules.Resource{SitePasscode: "inherit"}}
	for _, action := range []string{"add", "edit", "delete"} {
		result := s.AuthorizeTableWrite(context.Background(), TableInput{Call: c, Table: "notes"}, action)
		if result.Allowed() || result.Authorization == nil || result.Authorization.Code != "site_locked" {
			t.Fatalf("%s: %#v", action, result)
		}
	}
}
