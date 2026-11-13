package storage

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	sqlite3 "github.com/ncruces/go-sqlite3"
)

func TestLegacyAdmissionInjectedPanics(t *testing.T) {
	db := admissionDB(t)
	for _, mode := range []string{"query", "execute", "schema"} {
		stages := []string{"after_open", "after_prepare", "after_step", "close"}
		if mode != "query" {
			stages = append(stages, "before_commit", "after_commit")
		}
		for _, stage := range stages {
			t.Run(mode+"/"+stage, func(t *testing.T) {
				s, c := admissionFixture(t, db)
				sql := "SELECT * FROM notes"
				if mode == "execute" {
					sql = "INSERT INTO notes(body) VALUES ('second')"
				}
				if mode == "schema" {
					sql = "CREATE TABLE added(id INTEGER PRIMARY KEY, body TEXT)"
					c.Resource.Read = "own"
					seed, err := sqlite3.Open(s.deps.SQLitePath)
					if err != nil {
						t.Fatal(err)
					}
					if err := seed.Exec("ALTER TABLE notes ADD COLUMN visitor_id TEXT"); err != nil {
						t.Fatal(err)
					}
					seed.Close()
				}
				body, _ := json.Marshal(map[string]string{"sql": sql})
				ctx, closed := observeAdmissionConnection(t)
				hit := false
				s.sqliteHook = func(at string) {
					if at == stage {
						hit = true
						panic("injected")
					}
				}
				out, err := s.LegacySQL(ctx, LegacySQLInput{Call: c, Mode: mode, ReadBody: func() ([]byte, error) { return body, nil }, ResultLimit: 1 << 20})
				// Exec includes SQLite's implicit commit. An after-step panic on that
				// path must keep the inserted id, just like an after-commit panic.
				success := stage == "close" || stage == "after_commit" || (mode == "execute" && stage == "after_step")
				if !hit {
					t.Fatal("hook not reached")
				}
				if success {
					if err != nil {
						t.Fatal(err)
					}
					if mode == "execute" && (out.LastInsertID != 2 || out.Changes != 1) {
						t.Fatalf("lost result: %+v", out)
					}
				} else if e, ok := err.(*Error); !ok || e.Code != "storage_failed" {
					t.Fatalf("panic error: %v", err)
				}
				closed()
				checkAdmissionIdle(t, s.deps.SiteSlots)
				mu, _ := s.deps.Locks.LoadOrStore(c.OwnerID+"/"+c.SiteName, &sync.Mutex{})
				if !mu.(*sync.Mutex).TryLock() {
					t.Fatal("write lock leaked")
				}
				mu.(*sync.Mutex).Unlock()
				seed, err := sqlite3.Open(s.deps.SQLitePath)
				if err != nil {
					t.Fatal(err)
				}
				defer seed.Close()
				query := "SELECT count(*) FROM notes"
				want := int64(1)
				if mode == "execute" && success {
					want = 2
				}
				if mode == "schema" {
					query = "SELECT count(*) FROM sqlite_schema WHERE name='added'"
					want = 0
					if success {
						want = 1
					}
				}
				stmt, _, err := seed.Prepare(query)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				if !stmt.Step() || stmt.ColumnInt64(0) != want {
					t.Fatalf("durable result differs, want %d", want)
				}
			})
		}
	}
}

func TestInspectionAdmissionInjectedPanics(t *testing.T) {
	for _, stage := range []string{"after_open", "after_prepare", "after_step", "close"} {
		t.Run(stage, func(t *testing.T) {
			s, c := admissionFixture(t, nil)
			ctx, closed := observeAdmissionConnection(t)
			s.sqliteHook = func(at string) {
				if at == stage {
					panic("injected")
				}
			}
			err := s.Inspect(ctx, c, false, func(i *Inspection) error {
				stmt, _, err := i.conn.Prepare("SELECT * FROM notes")
				if err != nil {
					return err
				}
				defer stmt.Close()
				s.sqliteStage("after_prepare")
				stmt.Step()
				s.sqliteStage("after_step")
				return stmt.Err()
			})
			if stage == "close" {
				if err != nil {
					t.Fatal(err)
				}
			} else if e, ok := err.(*Error); !ok || e.Code != "storage_failed" {
				t.Fatalf("panic error: %v", err)
			}
			closed()
			checkAdmissionIdle(t, s.deps.SiteSlots)
		})
	}
}

func TestLegacyAdmissionMemoryPanic(t *testing.T) {
	s, c := admissionFixture(t, nil)
	s.sqliteHook = func(at string) {
		if at == "after_prepare" {
			panic("sqlite3: out of memory")
		}
	}
	_, err := s.LegacySQL(context.Background(), LegacySQLInput{Call: c, Mode: "query", ReadBody: func() ([]byte, error) { return []byte(`{"sql":"SELECT * FROM notes"}`), nil }, ResultLimit: 1 << 20})
	if e, ok := err.(*Error); !ok || e.Code != "query_too_expensive" {
		t.Fatal(err)
	}
	checkAdmissionIdle(t, s.deps.SiteSlots)
	// Keep the real parser diagnostic, rather than swallowing it in recovery.
	s.sqliteHook = nil
	body, _ := json.Marshal(map[string]string{"sql": "SELECT " + strings.Repeat("abs(", 1100) + "1" + strings.Repeat(")", 1100)})
	_, err = s.LegacySQL(context.Background(), LegacySQLInput{Call: c, Mode: "query", ReadBody: func() ([]byte, error) { return body, nil }, ResultLimit: 1 << 20})
	if e, ok := err.(*Error); !ok || e.Code != "sql_error" {
		t.Fatal(err)
	}
}
