// Package storetest is what the data features' own tests share: a site with
// a storage service on a disposable database, resources declared through the
// real rules, and the small readers of a refusal's code. Tests only.
package storetest

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	goruntime "runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/lib/pq"
	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/data/resources"
	sitesrepo "github.com/vsriram/simple-host/host/sites/repo"

	sqlite3 "github.com/ncruces/go-sqlite3"
	"github.com/vsriram/simple-host/host/data"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/papermetrics"
	runtime "github.com/vsriram/simple-host/host/storage"
	ruleengine "github.com/vsriram/simple-host/host/storage/rules"
)

// Disk is a site tree in a temporary directory.
func Disk(t *testing.T) *runtime.DiskStorage {
	t.Helper()
	disk, err := runtime.NewDiskStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return disk
}

// DB is the test's database (DB_DSN); the test skips without one.
func DB(t *testing.T) *sql.DB {
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

// KV is an owner's site "kv-test" with the key-value namespace "data", its
// storage service admitting one call at a time and failing the test on a
// nested admission. combined: one pool for KV, SQLite and files.
func KV(t *testing.T, combined bool) (*data.Service, data.Call) {
	t.Helper()
	db := DB(t)
	ctx := context.Background()
	u, err := accountsrepo.CreateUser(ctx, db, "kv-"+strconv.FormatInt(time.Now().UnixNano(), 36), "", false)
	if err != nil {
		t.Fatal(err)
	}
	owner := u.ID
	t.Cleanup(func() {
		if tx, err := db.Begin(); err == nil {
			_ = accountsrepo.DeleteAccountRow(ctx, tx, owner)
			_ = tx.Commit()
		}
	})
	created, err := sitesrepo.CreateSite(ctx, db, owner, "kv-test", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	site := created.ID
	gate := data.NewSQLiteGate(2, time.Second)
	slots := data.NewSiteSlots(1, gate, time.Second)
	var mu sync.Mutex
	held := map[string]bool{}
	slots.AdmitHook = func(enter bool) {
		var stack [64]byte
		id := strings.Fields(string(stack[:goruntime.Stack(stack[:], false)]))[1]
		mu.Lock()
		defer mu.Unlock()
		if enter && held[id] {
			t.Fatal("nested Admit on the same goroutine")
		}
		if enter {
			held[id] = true
		} else {
			delete(held, id)
		}
	}
	disk := Disk(t)
	s := data.New(db, data.Deps{Disk: disk, CombinedPool: combined, SiteSlots: slots, Locks: &sync.Map{}, WriteLockWait: time.Second, WriteTimeout: 30 * time.Second, SQLiteTimeout: 30 * time.Second})
	c := data.Call{Owner: true, OwnerID: owner, SiteID: site, SiteName: "kv-test", ResourceName: "data", RuntimeDir: filepath.Join(disk.SiteDir(owner, "kv-test"), "runtime")}
	result, err := Put(s, resources.ResourceInput{Call: c, Name: "data", Kind: "kv"})
	if err != nil {
		t.Fatal(err)
	}
	c.Resource = result.Resource
	return s, c
}

// Rules is KV's site with the SQLite database "rows" made from schema, and
// its declaration reading every row.
func Rules(t *testing.T, schema string) (*data.Service, data.Call, resources.ResourceInput) {
	t.Helper()
	s, c := KV(t, false)
	c.ResourceName = "rows"
	s.Deps.SQLitePath = filepath.Join(c.RuntimeDir, "sqlite", "rows.sqlite")
	if schema != "" {
		if err := os.MkdirAll(filepath.Dir(s.Deps.SQLitePath), 0700); err != nil {
			t.Fatal(err)
		}
		conn, err := sqlite3.Open(s.Deps.SQLitePath)
		if err != nil {
			t.Fatal(err)
		}
		if err := conn.Exec(schema); err != nil {
			t.Fatal(err)
		}
		if err := conn.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return s, c, resources.ResourceInput{Call: c, Name: "rows", Kind: "sqlite", Rules: ReadSet("true")}
}

// Read is Rules with set declared, called by a visitor who passed the site's
// gates.
func Read(t *testing.T, schema string, set *json.RawMessage) (*data.Service, data.Call, resources.ResourceInput) {
	t.Helper()
	s, c, in := Rules(t, schema)
	c.PasscodeOK, c.ViewersOK = true, true
	in.Rules = set
	c.Resource = PutRules(t, s, in)
	c.Owner = false
	c.CSRFOK = true
	return s, c, in
}

// Exec runs sql on the service's SQLite database.
func Exec(t *testing.T, s *data.Service, sql string) {
	t.Helper()
	conn, err := sqlite3.Open(s.Deps.SQLitePath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err = conn.Exec(sql); err != nil {
		t.Fatal(err)
	}
}

// PutRules declares in and returns the resource as it loads back, failing
// the test if saving and loading disagree.
func PutRules(t *testing.T, s *data.Service, in resources.ResourceInput) model.Resource {
	t.Helper()
	result, err := Put(s, in)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := s.LoadResource(context.Background(), in.Call.SiteID, in.Name)
	if err != nil {
		t.Fatal(err)
	}
	// PostgreSQL canonicalizes JSON formatting without changing the set.
	canonical := func(raw json.RawMessage) string {
		if raw == nil {
			return "NULL"
		}
		set, errs := ruleengine.DecodeSet(raw)
		if len(errs) != 0 {
			t.Fatal(errs)
		}
		return string(ruleengine.EncodeSet(set))
	}
	if canonical(result.Resource.Rules) != canonical(loaded.Rules) {
		t.Fatal("default rules changed")
	}
	result.Resource.Rules = loaded.Rules
	for name, q := range result.Resource.Tables {
		if canonical(q.Rules) != canonical(loaded.Tables[name].Rules) {
			t.Fatal("table rules changed")
		}
		q.Rules = loaded.Tables[name].Rules
		result.Resource.Tables[name] = q
	}
	if result.Resource.Fingerprint() != loaded.Fingerprint() {
		t.Fatal("save/load differ")
	}
	return loaded
}

// Raw is raw as a rules body.
func Raw(raw string) *json.RawMessage { x := json.RawMessage(raw); return &x }

// Set is a rule set of policies.
func Set(policies ...ruleengine.Policy) *json.RawMessage {
	raw := json.RawMessage(ruleengine.EncodeSet(ruleengine.PolicySet{Policies: append([]ruleengine.Policy{}, policies...)}))
	return &raw
}

// ReadSet is a rule set that reads the rows using allows.
func ReadSet(using string) *json.RawMessage {
	return Set(ruleengine.Policy{Name: "visible", Command: ruleengine.CmdRead, Using: using})
}

// Code is the refusal's or the failure's code ("" when there is neither).
func Code(auth *data.Error, err error) string {
	if err == nil && auth != nil {
		err = auth
	}
	if err == nil || reflect.ValueOf(err).IsNil() {
		return ""
	}
	if e, ok := err.(*data.Error); ok {
		return e.Code
	}
	return err.Error()
}

// Put declares a resource (PUT .../storage/resources/{name}).
func Put(s *data.Service, in resources.ResourceInput) (resources.ResourceResult, error) {
	return resources.PutStorageResource(s, context.Background(), in)
}

// JSON is v as a JSON request body.
func JSON(t *testing.T, v any) io.Reader {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.NewReader(b)
}

// WatchedReader is an empty body that records whether it was read.
type WatchedReader struct{ Seen *bool }

func (r WatchedReader) Read([]byte) (int, error) { *r.Seen = true; return 0, io.EOF }

// FatalReader is a body that must never be read.
type FatalReader struct{ T *testing.T }

func (r FatalReader) Read([]byte) (int, error) {
	r.T.Fatal("request body was read")
	return 0, io.EOF
}

// commitCounter counts the committed writes paper metrics hear of.
type commitCounter struct {
	papermetrics.Recorder
	n *int
}

func (c commitCounter) WriteCommitted(context.Context) { *c.n++ }

// CountCommits is ctx with a recorder that counts committed writes into n.
func CountCommits(ctx context.Context, n *int) context.Context {
	return papermetrics.WithRecorder(ctx, commitCounter{n: n})
}

type connHookKey struct{}

func init() {
	sqlite3.AutoExtension(func(conn *sqlite3.Conn) error {
		if hook, ok := conn.GetInterrupt().Value(connHookKey{}).(func(*sqlite3.Conn) error); ok {
			return hook(conn)
		}
		return nil
	})
}

// ObserveConnection is a context whose SQLite connection the returned check
// proves was closed once, with no statement left open.
func ObserveConnection(t *testing.T) (context.Context, func()) {
	t.Helper()
	var conn *sqlite3.Conn
	closes := 0
	ctx := context.WithValue(context.Background(), connHookKey{}, func(c *sqlite3.Conn) error {
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

// Admission is an owner's service on the SQLite database "data" holding the
// table notes with one row, admitting one call per site.
func Admission(t *testing.T, db *sql.DB) (*data.Service, data.Call) {
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
	gate := data.NewSQLiteGate(2, time.Second)
	return data.New(db, data.Deps{Disk: Disk(t), SQLitePath: path, SQLiteGate: gate, SiteSlots: data.NewSiteSlots(1, gate, time.Second), SQLiteTimeout: time.Second, SQLiteMaxMemory: 256 << 20, Locks: &sync.Map{}, WriteLockWait: time.Second, WriteTimeout: time.Second}), data.Call{Owner: true, SiteID: "11111111-1111-1111-1111-111111111111", OwnerID: "owner", SiteName: "site", ResourceName: "data", RuntimeDir: dir}
}

// CheckIdle fails the test when a slot is still held or waited for.
func CheckIdle(t *testing.T, slots *data.SiteSlots) {
	t.Helper()
	if sites, global := slots.InUse(); sites != 0 || global != 0 {
		t.Fatalf("leaked slots: sites=%d global=%d", sites, global)
	}
}

// Refusal declares in, expecting invalid_rules at clause with its diagnostic.
func Refusal(t *testing.T, s *data.Service, in resources.ResourceInput, clause string) *data.Error {
	t.Helper()
	_, err := Put(s, in)
	e, ok := err.(*data.Error)
	if !ok || e.Code != "invalid_rules" || e.RuleError == nil || e.RuleError.Clause != clause || len(e.RulesErrors) == 0 {
		t.Fatalf("want invalid_rules/%s, got %#v", clause, err)
	}
	if e.RuleError.Message == "" || *e.RuleError != e.RulesErrors[0] {
		t.Fatalf("lost rule_error: %+v", e)
	}
	return e
}
