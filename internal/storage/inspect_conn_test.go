package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	sqlite3 "github.com/ncruces/go-sqlite3"
)

func inspectDirNames(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return strings.Join(names, ",")
}

func inspectTables(t *testing.T, path string) string {
	t.Helper()
	conn, done, err := OpenInspectConn(context.Background(), path, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	tables, err := SQLTables(conn)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for name := range tables {
		names = append(names, name)
	}
	sort.Strings(names)
	// A missing file is an empty in-memory database; a real one is read-only.
	if _, statErr := os.Stat(path); statErr == nil {
		if err := conn.Exec(`CREATE TABLE forbidden(id INTEGER)`); err == nil {
			t.Fatal("an inspection connection accepted a write")
		}
	}
	return strings.Join(names, ",")
}

// A schema inspection leaves no file behind on a cleanly closed WAL database,
// refuses a nonempty WAL, and answers "no tables" for a missing file.
func TestOpenInspectConnLeavesNoFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.sqlite")
	if got := inspectTables(t, path); got != "" || inspectDirNames(t, dir) != "" {
		t.Fatalf("missing file: tables %q, files %q", got, inspectDirNames(t, dir))
	}
	conn, err := sqlite3.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Exec(`PRAGMA journal_mode=WAL; CREATE TABLE first(id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if got := inspectDirNames(t, dir); got != "data.sqlite" {
		t.Fatalf("fixture files: %s", got)
	}
	if got := inspectTables(t, path); got != "first" {
		t.Fatalf("closed database: %q", got)
	}
	if got := inspectDirNames(t, dir); got != "data.sqlite" {
		t.Fatalf("the inspection left files: %s", got)
	}
	// A writer with the database open: its table is still only in the log.
	writer, err := sqlite3.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if err := writer.Exec(`PRAGMA wal_autocheckpoint=0; CREATE TABLE second(id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(path + "-wal"); err != nil || fi.Size() == 0 {
		t.Fatalf("fixture: no log in use: %v", err)
	}
	// Copy the live files to model a crash without changing the writer's
	// shared memory. Inspection must not create or repair any sidecar.
	for _, shm := range []string{"intact", "missing", "truncated"} {
		t.Run(shm, func(t *testing.T) {
			copyDir := t.TempDir()
			copyPath := filepath.Join(copyDir, "data.sqlite")
			before := map[string][]byte{}
			for _, suffix := range []string{"", "-wal", "-shm"} {
				if suffix == "-shm" && shm == "missing" {
					continue
				}
				raw, err := os.ReadFile(path + suffix)
				if err != nil {
					t.Fatal(err)
				}
				if suffix == "-shm" && shm == "truncated" {
					raw = raw[:1]
				}
				if err := os.WriteFile(copyPath+suffix, raw, 0600); err != nil {
					t.Fatal(err)
				}
				before["data.sqlite"+suffix] = raw
			}
			conn, done, err := OpenInspectConn(context.Background(), copyPath, 10*time.Second)
			if !errors.Is(err, ErrInspectWAL) || conn != nil || done != nil {
				if done != nil {
					done()
				}
				t.Fatalf("inspection of a nonempty WAL: %v", err)
			}
			entries, err := os.ReadDir(copyDir)
			if err != nil {
				t.Fatal(err)
			}
			after := map[string][]byte{}
			for _, entry := range entries {
				raw, err := os.ReadFile(filepath.Join(copyDir, entry.Name()))
				if err != nil {
					t.Fatal(err)
				}
				after[entry.Name()] = raw
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatal("inspection modified database or sidecar files")
			}
		})
	}
}
