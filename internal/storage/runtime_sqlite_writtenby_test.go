package storage

import (
	"errors"
	"testing"

	sqlite3 "github.com/ncruces/go-sqlite3"
)

// TestEnsureWrittenByColumn covers the SIM-358 review fix: the lazy
// written_by upgrade must succeed on an ordinary table, be idempotent, and
// refuse rather than silently reshape a table a trigger or view depends on.
func TestEnsureWrittenByColumn(t *testing.T) {
	conn, err := sqlite3.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	if err := conn.Exec(`CREATE TABLE entries (id INTEGER PRIMARY KEY, note TEXT, visitor_id TEXT)`); err != nil {
		t.Fatal(err)
	}
	if err := EnsureWrittenByColumn(conn, "entries"); err != nil {
		t.Fatalf("plain table: %v", err)
	}
	cols, err := TableColumns(conn, "entries")
	if err != nil || cols["written_by"] != "TEXT" {
		t.Fatalf("written_by not added: %v %v", cols, err)
	}
	// Idempotent: calling again on a table that already has it is a no-op,
	// not an error.
	if err := EnsureWrittenByColumn(conn, "entries"); err != nil {
		t.Fatalf("second call: %v", err)
	}

	// A table with a dependent trigger (INSERT ... SELECT * copies every
	// column, so one more column changes what it does) refuses the ALTER.
	if err := conn.Exec(`CREATE TABLE audited (id INTEGER PRIMARY KEY, note TEXT, visitor_id TEXT)`); err != nil {
		t.Fatal(err)
	}
	if err := conn.Exec(`CREATE TABLE audited_copy (id INTEGER PRIMARY KEY, note TEXT, visitor_id TEXT)`); err != nil {
		t.Fatal(err)
	}
	if err := conn.Exec(`CREATE TRIGGER audited_mirror AFTER INSERT ON audited BEGIN INSERT INTO audited_copy SELECT * FROM audited WHERE id = NEW.id; END`); err != nil {
		t.Fatal(err)
	}
	if err := EnsureWrittenByColumn(conn, "audited"); !errors.Is(err, ErrWrittenBySchemaLocked) {
		t.Fatalf("trigger-dependent table: want ErrWrittenBySchemaLocked, got %v", err)
	}
	if cols, _ := TableColumns(conn, "audited"); cols["written_by"] == "TEXT" {
		t.Fatal("column added despite the dependent trigger")
	}

	// A table with a dependent view (SELECT * changes shape too) also
	// refuses.
	if err := conn.Exec(`CREATE TABLE viewed (id INTEGER PRIMARY KEY, note TEXT, visitor_id TEXT)`); err != nil {
		t.Fatal(err)
	}
	if err := conn.Exec(`CREATE VIEW viewed_all AS SELECT * FROM viewed`); err != nil {
		t.Fatal(err)
	}
	if err := EnsureWrittenByColumn(conn, "viewed"); !errors.Is(err, ErrWrittenBySchemaLocked) {
		t.Fatalf("view-dependent table: want ErrWrittenBySchemaLocked, got %v", err)
	}

	// An unrelated trigger or view on a different table never blocks this
	// one.
	if err := conn.Exec(`CREATE TABLE plain (id INTEGER PRIMARY KEY, note TEXT, visitor_id TEXT)`); err != nil {
		t.Fatal(err)
	}
	if err := EnsureWrittenByColumn(conn, "plain"); err != nil {
		t.Fatalf("unrelated dependents should not block: %v", err)
	}
}
