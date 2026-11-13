package storage

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/lib/pq"
	sqlite3 "github.com/ncruces/go-sqlite3"
	runtime "github.com/vsriram/simple-host/internal/storage"
	"github.com/vsriram/simple-host/internal/testdb"
)

func TestMain(m *testing.M) { testdb.Main(m) }

type admissionConnHookKey struct{}

func init() {
	sqlite3.AutoExtension(func(conn *sqlite3.Conn) error {
		if hook, ok := conn.GetInterrupt().Value(admissionConnHookKey{}).(func(*sqlite3.Conn) error); ok {
			return hook(conn)
		}
		return nil
	})
}

func observeAdmissionConnection(t *testing.T) (context.Context, func()) {
	t.Helper()
	var conn *sqlite3.Conn
	closes := 0
	ctx := context.WithValue(context.Background(), admissionConnHookKey{}, func(c *sqlite3.Conn) error {
		conn = c
		return c.Trace(sqlite3.TRACE_CLOSE, func(sqlite3.TraceEvent, any, any) error {
			closes++
			return nil
		})
	})
	return ctx, func() {
		t.Helper()
		if conn == nil {
			t.Fatal("connection never opened")
		}
		for stmt := range conn.Stmts() {
			t.Errorf("statement leaked: %s", stmt.SQL())
			_ = stmt.Close()
		}
		// SQLite emits TRACE_CLOSE even on BUSY. A second close emits no
		// trace only if the operation's cleanup really closed the connection.
		if closes != 1 {
			t.Errorf("close attempts = %d, want one operation cleanup", closes)
		}
		before := closes
		if err := conn.Close(); err != nil {
			t.Fatal(err)
		}
		if closes != before {
			t.Fatal("operation cleanup left the connection open")
		}
	}
}

func admissionDB(t *testing.T) *sql.DB {
	t.Helper()
	if os.Getenv("DB_DSN") == "" {
		t.Skip("DB_DSN unset")
	}
	db, err := sql.Open("postgres", os.Getenv("DB_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func admissionFixture(t *testing.T, db *sql.DB) (*Service, Call) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "sqlite", "data.sqlite")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	conn, err := sqlite3.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Exec(`CREATE TABLE notes(id INTEGER PRIMARY KEY, body TEXT); INSERT INTO notes VALUES(1, 'first')`); err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	gate := NewSQLiteGate(2, time.Second)
	return New(db, Deps{SQLitePath: path, SQLiteGate: gate, SiteSlots: NewSiteSlots(1, gate, time.Second), SQLiteTimeout: time.Second, SQLiteMaxMemory: 256 << 20, Locks: &sync.Map{}, WriteLockWait: time.Second, WriteTimeout: time.Second, MarkChanged: func() {}}), Call{Owner: true, SiteID: "11111111-1111-1111-1111-111111111111", OwnerID: "owner", SiteName: "site", ResourceName: "data", RuntimeDir: dir}
}

func runAdmissionRow(s *Service, c Call, operation string) (TableResult, error) {
	return runAdmissionRowContext(context.Background(), s, c, operation)
}

func runAdmissionRowContext(ctx context.Context, s *Service, c Call, operation string) (TableResult, error) {
	in := TableInput{Call: c, Table: "notes", ID: 1, Values: map[string]json.RawMessage{"body": json.RawMessage(`"changed"`)}, Query: url.Values{}}
	switch operation {
	case "get":
		return s.TableGet(ctx, in)
	case "list":
		return s.TableList(ctx, in)
	}
	in.Authorized = s.AuthorizeTableWrite(ctx, in, operation)
	switch operation {
	case "add":
		return s.TableAdd(ctx, in)
	case "edit":
		return s.TableEdit(ctx, in)
	default:
		return s.TableRemove(ctx, in)
	}
}

func TestRowAdmissionInjectedPanics(t *testing.T) {
	db := admissionDB(t)
	for _, operation := range []string{"get", "list", "add", "edit", "delete"} {
		stages := []string{"after_open", "after_prepare", "after_step", "close"}
		if operation != "get" && operation != "list" {
			stages = append(stages, "before_commit", "after_commit")
		}
		for _, stage := range stages {
			t.Run(operation+"/"+stage, func(t *testing.T) {
				s, c := admissionFixture(t, db)
				ctx, checkClosed := observeAdmissionConnection(t)
				committed, changed := 0, 0
				s.deps.Committed = func(context.Context) { committed++ }
				s.deps.MarkChanged = func() { changed++ }
				s.sqliteHook = func(at string) {
					if at == stage {
						panic("injected")
					}
				}
				var logs bytes.Buffer
				old := log.Writer()
				log.SetOutput(&logs)
				defer log.SetOutput(old)
				out, err := runAdmissionRowContext(ctx, s, c, operation)
				success := stage == "after_commit" || stage == "close"
				if success {
					if err != nil || out.Body == nil {
						t.Fatalf("lost result: %+v %v", out, err)
					}
				} else if err == nil || err.(*Error).Code != "storage_failed" {
					t.Fatalf("panic: %+v %v", out, err)
				}
				wantCommits := 0
				if success && operation != "get" && operation != "list" {
					wantCommits = 1
				}
				if committed != wantCommits || changed != wantCommits {
					t.Fatalf("commit callbacks: metric=%d changed=%d want=%d", committed, changed, wantCommits)
				}
				checkClosed()
				checkAdmissionIdle(t, s.deps.SiteSlots)
				mu, _ := s.deps.Locks.LoadOrStore(c.OwnerID+"/"+c.SiteName, &sync.Mutex{})
				if !mu.(*sync.Mutex).TryLock() {
					t.Fatal("write lock leaked")
				}
				mu.(*sync.Mutex).Unlock()
				if strings.Count(logs.String(), "recovered SQLite") != 1 {
					t.Fatalf("want one panic log: %q", logs.String())
				}
				conn, err := sqlite3.Open(s.deps.SQLitePath)
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				stmt, _, err := conn.Prepare(`SELECT count(*), max(id), max(body) FROM notes`)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				if !stmt.Step() {
					t.Fatal(stmt.Err())
				}
				count := int64(1)
				if success && operation == "add" {
					count = 2
					if out.Body["last_insert_id"] != int64(2) || stmt.ColumnInt64(1) != 2 {
						t.Fatalf("wrong inserted id: %+v", out.Body)
					}
				}
				if success && operation == "delete" {
					count = 0
					if out.Body["deleted"] != true {
						t.Fatal(out.Body)
					}
				}
				if stmt.ColumnInt64(0) != count {
					t.Fatalf("row count %d want %d", stmt.ColumnInt64(0), count)
				}
				if operation == "edit" {
					want := "first"
					if success {
						want = "changed"
					}
					if stmt.ColumnText(2) != want {
						t.Fatalf("edit=%q want=%q", stmt.ColumnText(2), want)
					}
				}
			})
		}
	}
}

func TestRowAdmissionSharedByServicesAndResources(t *testing.T) {
	first, c := admissionFixture(t, nil)
	second := New(nil, first.deps)
	secondCall := c
	secondCall.ResourceName = "other"
	second.deps.SQLitePath = filepath.Join(c.RuntimeDir, "sqlite", "other.sqlite")
	data, err := os.ReadFile(first.deps.SQLitePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second.deps.SQLitePath, data, 0600); err != nil {
		t.Fatal(err)
	}
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	first.sqliteHook = func(stage string) {
		if stage == "after_open" {
			close(entered)
			<-release
		}
	}
	go func() { _, err := runAdmissionRow(first, c, "get"); done <- err }()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err = second.TableGet(ctx, TableInput{Call: secondCall, Table: "notes", ID: 1})
	if err == nil || err.(*Error).Code != "storage_busy" {
		t.Fatalf("second resource/service bypassed slot: %v", err)
	}
	secondCall.SiteID = "other-site"
	if _, err := runAdmissionRow(second, secondCall, "get"); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	checkAdmissionIdle(t, first.deps.SiteSlots)
}

func TestOwnerSQLRealParserLimit(t *testing.T) {
	s, c := admissionFixture(t, nil)
	query := "SELECT " + strings.Repeat("abs(", 1100) + "1" + strings.Repeat(")", 1100)
	body, _ := json.Marshal(map[string]any{"sql": query})
	_, err := s.LegacySQL(context.Background(), LegacySQLInput{Call: c, Mode: "query", ReadBody: func() ([]byte, error) { return body, nil }, ResultLimit: 1 << 20})
	e, ok := err.(*Error)
	if !ok || e.Code != "sql_error" || e.Message != "SQLite: Recursion limit" {
		t.Fatalf("parser diagnostic: %#v", err)
	}
}

func TestRowAdmissionRealMemoryLimit(t *testing.T) {
	s, c := admissionFixture(t, nil)
	conn, err := sqlite3.Open(s.deps.SQLitePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Exec(`UPDATE notes SET body = zeroblob(800000)`); err != nil {
		t.Fatal(err)
	}
	conn.Close()
	s.deps.SQLiteMaxMemory = 384 << 10
	_, err = runAdmissionRow(s, c, "get")
	if e, ok := err.(*Error); !ok || e.Code != "query_too_expensive" {
		t.Fatalf("memory cap: %v", err)
	}
	checkAdmissionIdle(t, s.deps.SiteSlots)
}

func TestRowAdmissionMetadataMemoryLimit(t *testing.T) {
	s, c := admissionFixture(t, nil)
	seed, err := sqlite3.Open(s.deps.SQLitePath)
	if err != nil {
		t.Fatal(err)
	}
	// cache_size now loads the schema during setup. Use a wide table so the
	// schema fits, but TableInfo's SELECT * LIMIT 0 needs more memory to prepare
	// its result columns. No row data is needed to exhaust the cap.
	columns := []string{"id INTEGER PRIMARY KEY", "body TEXT"}
	for i := range 1000 {
		columns = append(columns, fmt.Sprintf("c%d TEXT", i))
	}
	if err := seed.Exec("DROP TABLE notes; CREATE TABLE notes (" + strings.Join(columns, ",") + ")"); err != nil {
		t.Fatal(err)
	}
	if _, order, err := runtime.TableInfo(seed, "notes"); err != nil || len(order) != len(columns) {
		t.Fatalf("uncapped metadata: columns=%d err=%v", len(order), err)
	}
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}
	s.deps.SQLiteMaxMemory = 512 << 10
	// Prove this cap allows connection setup and fails while loading metadata,
	// before any row statement is prepared or any large row is read.
	metadataReached := false
	err = s.deps.SiteSlots.Admit(context.Background(), c.SiteID, func(ctx context.Context, _ *Admission) error {
		conn, done, err := runtime.OpenTableConn(ctx, s.deps.SQLitePath, false,
			time.Second, s.deps.SQLiteMaxMemory, nil)
		if err != nil {
			t.Fatalf("setup failed before metadata: %v", err)
		}
		defer done()
		metadataReached = true
		_, _, err = runtime.TableInfo(conn, "notes")
		return err
	})
	if !metadataReached {
		t.Fatal("memory cap failed during setup, before metadata")
	}
	if e, ok := err.(*Error); !ok || e.Code != "query_too_expensive" {
		t.Fatalf("metadata-only memory cap: %v", err)
	}
	ctx, checkClosed := observeAdmissionConnection(t)
	_, err = runAdmissionRowContext(ctx, s, c, "get")
	if e, ok := err.(*Error); !ok || e.Code != "query_too_expensive" {
		t.Fatalf("metadata memory cap: %v", err)
	}
	checkClosed()
	checkAdmissionIdle(t, s.deps.SiteSlots)
}

func TestRowAdmissionSetupMemoryLimit(t *testing.T) {
	s, c := admissionFixture(t, nil)
	seed, err := sqlite3.Open(s.deps.SQLitePath)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 100 {
		if err := seed.Exec(fmt.Sprintf("CREATE TABLE metadata_%d (%s)", i,
			"c0 TEXT, c1 TEXT, c2 TEXT, c3 TEXT, c4 TEXT, c5 TEXT, c6 TEXT, c7 TEXT, c8 TEXT, c9 TEXT")); err != nil {
			t.Fatal(err)
		}
	}
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}
	s.deps.SQLiteMaxMemory = 384 << 10
	ctx, checkClosed := observeAdmissionConnection(t)
	setupCompleted := false
	err = s.deps.SiteSlots.Admit(ctx, c.SiteID, func(ctx context.Context, _ *Admission) error {
		_, done, err := runtime.OpenTableConn(ctx, s.deps.SQLitePath, false,
			time.Second, s.deps.SQLiteMaxMemory, nil)
		if err != nil {
			return err
		}
		defer done()
		setupCompleted = true
		return nil
	})
	if setupCompleted {
		t.Fatal("expected memory exhaustion while configuring the connection")
	}
	if e, ok := err.(*Error); !ok || e.Code != "query_too_expensive" {
		t.Fatalf("setup memory cap: %v", err)
	}
	checkClosed()
	checkAdmissionIdle(t, s.deps.SiteSlots)
}
