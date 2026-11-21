package data

import (
	"context"
	"encoding/json"
	"testing"

	sqlite3 "github.com/ncruces/go-sqlite3"
	model "github.com/vsriram/simple-host/host/model"
)

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
	own := model.Resource{Kind: "sqlite", Rules: *readSet(`visitor_id == auth.id || auth.role == "owner"`)}
	c := Call{Context: context.Background(), VisitorID: "visitor", ActiveChecked: true, Resource: own}
	if err := (&Service{}).checkRowReferences(conn, c, "child", "id", 2); err != nil {
		t.Fatalf("own parent: %v", err)
	}
	if err := (&Service{}).checkRowReferences(conn, c, "child", "id", 1); err == nil || err.(*Error).Code != "invalid_reference" {
		t.Fatalf("other parent: %v", err)
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
	disk := testDisk(t)
	s := New(nil, Deps{Disk: disk})
	own := model.Resource{Kind: "sqlite", Rules: *readSet(`visitor_id == auth.id || auth.role == "owner"`)}
	c := Call{Context: context.Background(), VisitorID: "visitor", ActiveChecked: true, Resource: own}
	meta, err := tableMetadata(conn, c, "child")
	if err != nil {
		t.Fatal(err)
	}
	in := TableInput{Table: "child", Values: map[string]json.RawMessage{"parent_id": json.RawMessage(`1`)}}
	var id int64
	if err := s.addTableRow(conn, c, in, meta, &Admission{}, &id); err == nil || err.(*Error).Code != "invalid_reference" || disk.Changes() != 0 {
		t.Fatalf("inaccessible parent: %v, marks %d", err, disk.Changes())
	}
	in.Values = map[string]json.RawMessage{"parent_id": json.RawMessage(`2`)}
	err = s.addTableRow(conn, c, in, meta, &Admission{}, &id)
	if err != nil || id != 1 || disk.Changes() != 1 {
		t.Fatalf("accessible parent: id %d, error %v, marks %d", id, err, disk.Changes())
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
	disk := testDisk(t)
	s := New(nil, Deps{Disk: disk})
	c := Call{Context: context.Background(), VisitorID: "visitor"}
	meta, err := tableMetadata(conn, c, "notes")
	if err != nil {
		t.Fatal(err)
	}
	in := TableInput{Table: "notes", Values: map[string]json.RawMessage{"id": json.RawMessage(`42`), "body": json.RawMessage(`"hello"`)}}
	var id int64
	if err := s.addTableRow(conn, c, in, meta, &Admission{}, &id); err == nil || err.(*Error).Message != "the server assigns ids" {
		t.Fatalf("client id: %v", err)
	}
	if disk.Changes() != 0 || conn.TotalChanges() != 0 {
		t.Fatalf("rejected insert changed data: marks %d, changes %d", disk.Changes(), conn.TotalChanges())
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
	disk := testDisk(t)
	s := New(nil, Deps{Disk: disk})
	c := Call{Context: context.Background(), VisitorID: "visitor"}
	meta, err := tableMetadata(conn, c, "notes")
	if err != nil {
		t.Fatal(err)
	}
	var id int64
	err = s.addTableRow(conn, c, TableInput{Table: "notes", Values: map[string]json.RawMessage{"body": json.RawMessage(`"hello"`)}}, meta, &Admission{}, &id)
	if e, ok := err.(*Error); !ok || e.Code != "forbidden" || disk.Changes() != 0 || conn.TotalChanges() != 0 {
		t.Fatalf("trigger insert: error %v, marks %d, changes %d", err, disk.Changes(), conn.TotalChanges())
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
	disk := testDisk(t)
	s := New(nil, Deps{Disk: disk})
	c := Call{Context: context.Background(), Owner: true}
	meta, err := tableMetadata(conn, c, "notes")
	if err != nil {
		t.Fatal(err)
	}
	var id int64
	err = s.addTableRow(conn, c, TableInput{Table: "notes", Values: map[string]json.RawMessage{"body": json.RawMessage(`"hello"`)}}, meta, &Admission{}, &id)
	if err != nil || id != 1 || disk.Changes() != 1 || conn.TotalChanges() != 2 {
		t.Fatalf("owner trigger insert: id %d, error %v, marks %d, changes %d", id, err, disk.Changes(), conn.TotalChanges())
	}
}

func TestTableWriteAuthorizesBeforeReplay(t *testing.T) {
	s := New(nil, Deps{})
	c := Call{Resource: model.Resource{SitePasscode: "inherit"}}
	for _, action := range []string{"add", "edit", "delete"} {
		result := s.AuthorizeTableWrite(context.Background(), TableInput{Call: c, Table: "notes"}, action)
		if result.Allowed() || result.Authorization == nil || result.Authorization.Code != "site_locked" {
			t.Fatalf("%s: %#v", action, result)
		}
	}
}
