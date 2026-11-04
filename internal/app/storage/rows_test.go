package storage

import (
	"context"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	sqlite3 "github.com/ncruces/go-sqlite3"
	rules "github.com/vsriram/simple-host/internal/rules/storage"
	runtime "github.com/vsriram/simple-host/internal/storage"
)

func TestGetRowVisibility(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.sqlite")
	conn, err := sqlite3.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Exec(`CREATE TABLE notes (id INTEGER PRIMARY KEY, visitor_id TEXT, body TEXT); INSERT INTO notes VALUES (1, 'other', 'hello')`); err != nil {
		t.Fatal(err)
	}
	conn.Close()
	s := New(nil, Deps{SQLitePath: path, SQLiteGate: runtime.NewSQLiteGate(1, time.Second), SQLiteTimeout: time.Second, SQLiteMaxMemory: 4096 * 65536, ViewersAllow: func() bool { return true }})
	c := Call{VisitorID: "visitor", Resource: rules.Resource{Read: rules.WhoAnyone}}
	got, err := s.TableGet(context.Background(), &c, TableInput{Table: "notes", ID: 1})
	if err != nil || got.Authorization != nil {
		t.Fatalf("read: %+v %v", got, err)
	}
	row := got.Body["row"].([]any)
	if row[1] != nil || got.Body["mine"] != false {
		t.Fatalf("visibility: %#v", got.Body)
	}
	c.Owner = true
	got, err = s.TableGet(context.Background(), &c, TableInput{Table: "notes", ID: 1})
	if err != nil || got.Body["row"].([]any)[1] != "other" {
		t.Fatalf("owner: %+v %v", got, err)
	}
	_, err = s.TableGet(context.Background(), &c, TableInput{Table: "notes", ID: 2})
	if e, ok := err.(*Error); !ok || e.Code != "row_not_found" {
		t.Fatalf("missing: %v", err)
	}
}

func TestListRowsPaginationAndVisibility(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.sqlite")
	conn, err := sqlite3.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = conn.Exec(`CREATE TABLE notes (id INTEGER PRIMARY KEY, visitor_id TEXT, body TEXT); INSERT INTO notes VALUES (1, 'other', 'a'), (2, 'visitor', 'b')`); err != nil {
		t.Fatal(err)
	}
	conn.Close()
	s := New(nil, Deps{SQLitePath: path, SQLiteGate: runtime.NewSQLiteGate(1, time.Second), SQLiteTimeout: time.Second, SQLiteMaxMemory: 4096 * 65536, ViewersAllow: func() bool { return true }})
	c := Call{VisitorID: "visitor", Resource: rules.Resource{Read: rules.WhoAnyone}}
	in := TableInput{Table: "notes", Query: url.Values{"limit": {"1"}}}
	first, err := s.TableList(context.Background(), &c, in)
	if err != nil || first.Authorization != nil {
		t.Fatalf("first: %+v %v", first, err)
	}
	rows := first.Body["rows"].([][]any)
	if len(rows) != 1 || rows[0][1] != nil || first.Body["next_after"] == "" {
		t.Fatalf("first: %#v", first.Body)
	}
	in.Query.Set("after", first.Body["next_after"].(string))
	second, err := s.TableList(context.Background(), &c, in)
	if err != nil || len(second.Body["rows"].([][]any)) != 1 || second.Body["mine"].([]bool)[0] != true || second.Body["next_after"] != "" {
		t.Fatalf("second: %+v %v", second, err)
	}
}
