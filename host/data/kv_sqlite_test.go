package data

import (
	"context"
	"database/sql"
	"encoding/json"
	"math/rand"
	"os"
	"path/filepath"
	goruntime "runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	repository "github.com/vsriram/simple-host/host/data/repo"
	model "github.com/vsriram/simple-host/host/model"
	runtime "github.com/vsriram/simple-host/host/storage"
	ruleengine "github.com/vsriram/simple-host/host/storage/rules"
)

func kvFixture(t *testing.T, combined bool) (*Service, Call) {
	t.Helper()
	db := admissionDB(t)
	var owner, site string
	if err := db.QueryRow(`INSERT INTO users(username) VALUES(gen_random_uuid()::text) RETURNING id`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Exec(`DELETE FROM users WHERE id=$1`, owner) })
	if err := db.QueryRow(`INSERT INTO sites(user_id,name) VALUES($1,'kv-test') RETURNING id`, owner).Scan(&site); err != nil {
		t.Fatal(err)
	}
	gate := NewSQLiteGate(2, time.Second)
	slots := NewSiteSlots(1, gate, time.Second)
	// A goroutine-level hook catches nested admission even if a test uses a
	// site capacity greater than one (where nesting might otherwise pass).
	var mu sync.Mutex
	held := map[string]bool{}
	slots.admitHook = func(enter bool) {
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
	disk := testDisk(t)
	s := New(db, Deps{Disk: disk, CombinedPool: combined, SiteSlots: slots, Locks: &sync.Map{}, WriteLockWait: time.Second, WriteTimeout: 30 * time.Second, SQLiteTimeout: 30 * time.Second})
	c := Call{Owner: true, OwnerID: owner, SiteID: site, SiteName: "kv-test", ResourceName: "data", RuntimeDir: filepath.Join(disk.SiteDir(owner, "kv-test"), "runtime")}
	result, err := s.PutStorageResource(context.Background(), ResourceInput{Call: c, Name: "data", Kind: "kv"})
	if err != nil {
		t.Fatal(err)
	}
	c.Resource = result.Resource
	return s, c
}

func kvPut(t *testing.T, s *Service, c Call, key, value string, want string) {
	t.Helper()
	_, auth, err := s.PutKV(context.Background(), KVPutInput{Call: c, Key: key, Body: strings.NewReader(`{"value":` + value + `}`)})
	if auth != nil {
		t.Fatalf("unexpected auth: %+v", auth)
	}
	if want == "" {
		if err != nil {
			t.Fatal(err)
		}
		return
	}
	if e, ok := err.(*Error); !ok || e.Code != want {
		t.Fatalf("want %s, got %v", want, err)
	}
}

func TestKVNormalizationKeysAndOrdering(t *testing.T) {
	s, c := kvFixture(t, false)
	ctx := context.Background()
	for _, key := range []string{"A", "a", "x%_\\one", "xAAone", "é", "中", "z"} {
		kvPut(t, s, c, key, `{"b":1,"a":0,"b":1.0e2}`, "")
	}
	for _, key := range []string{"A", "a"} {
		got, err := s.GetKV(ctx, KVReadInput{Call: c, Key: key})
		if err != nil {
			t.Fatal(err)
		}
		normalized, err := repository.KVNormalize(ctx, s.db, json.RawMessage(`{"b":1,"a":0,"b":1.0e2}`))
		if err != nil {
			t.Fatal(err)
		}
		if string(got.Item.Value) != normalized {
			t.Fatalf("normalized: %s != %s", got.Item.Value, normalized)
		}
	}
	got, err := s.ListKV(ctx, KVListInput{Call: c, Prefix: "x%_\\", Limit: 100, ValidLimit: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 1 || got.Items[0].Key != "x%_\\one" {
		t.Fatalf("literal prefix: %+v", got)
	}
	all, err := s.ListKV(ctx, KVListInput{Call: c, Limit: 100, ValidLimit: true})
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, item := range all.Items {
		keys = append(keys, item.Key)
	}
	if !sort.StringsAreSorted(keys) {
		t.Fatal("not byte order")
	}
	kvPut(t, s, c, "nul", `"\u0000"`, "invalid_value")
	kvPut(t, s, c, "huge", `1e1000000`, "invalid_value")
}

func TestKVQuotaBoundaries(t *testing.T) {
	for _, combined := range []bool{false, true} {
		s, c := kvFixture(t, combined)
		limit := SiteLimitBytes("", combined)
		// A normal SQLite database consumes the rest of the actual product pool;
		// KV has four bytes left. File bytes count only in Hack's combined pool.
		if err := os.MkdirAll(filepath.Join(c.RuntimeDir, "files", "bucket"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(c.RuntimeDir, "files", "bucket", "data"), []byte("123"), 0600); err != nil {
			t.Fatal(err)
		}
		other := limit - 4
		if combined {
			other -= 3
		}
		if err := os.MkdirAll(filepath.Join(c.RuntimeDir, "sqlite"), 0700); err != nil {
			t.Fatal(err)
		}
		f, err := os.Create(filepath.Join(c.RuntimeDir, "sqlite", "other.sqlite"))
		if err != nil {
			t.Fatal(err)
		}
		if err = f.Truncate(other); err != nil {
			t.Fatal(err)
		}
		f.Close()
		kvPut(t, s, c, "key", "10", "site_full") // five bytes, one over
		kvPut(t, s, c, "key", "1", "")           // four bytes, exact boundary
		u, err := s.MeasureSiteStorage(context.Background(), c.SiteID, c.RuntimeDir)
		if err != nil || s.BudgetUsed(u) != limit {
			t.Fatalf("boundary: %+v %v", u, err)
		}
		if _, err = s.DeleteKV(context.Background(), KVDeleteInput{Call: c, Key: "key"}); err != nil {
			t.Fatal(err)
		}
		u, err = s.MeasureSiteStorage(context.Background(), c.SiteID, c.RuntimeDir)
		if err != nil || u.KV != 0 || s.BudgetUsed(u) != limit-4 {
			t.Fatalf("delete charge: %+v %v", u, err)
		}
	}
}

func TestKVAdmissionBesideOtherStorage(t *testing.T) {
	s, c := kvFixture(t, false)
	ctx := context.Background()
	kvPut(t, s, c, "key", "1", "")
	// Owner SQL, row write budget and rules preparation each exercise their
	// designated admission form while the site also has a KV file.
	s.deps.SQLitePath = filepath.Join(c.RuntimeDir, "sqlite", "rows.sqlite")
	rc := c
	rc.ResourceName = "rows"
	rc.Resource = model.Resource{Kind: "sqlite"}
	_, err := s.LegacySQL(ctx, LegacySQLInput{Call: rc, Mode: "schema", Body: strings.NewReader(`{"sql":"CREATE TABLE notes(id INTEGER PRIMARY KEY, body TEXT)"}`)})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.PutStorageResource(ctx, ResourceInput{Call: rc, Name: "rows", Kind: "sqlite", Rules: saveSet(ruleengine.Policy{Name: "read", Command: ruleengine.CmdRead, Using: `visitor_id == auth.id || auth.role == "owner"`}, ruleengine.Policy{Name: "add", Command: ruleengine.CmdAdd, Check: `auth.signed_in`}, ruleengine.Policy{Name: "edit", Command: ruleengine.CmdEdit, Using: `visitor_id == auth.id || auth.role == "owner"`}, ruleengine.Policy{Name: "delete", Command: ruleengine.CmdDelete, Using: `visitor_id == auth.id || auth.role == "owner"`})})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.TableWrite(ctx, TableInput{Call: rc, Table: "notes", Values: map[string]json.RawMessage{"body": json.RawMessage(`"ok"`)}, Authorized: s.AuthorizeTableWrite(ctx, TableInput{Call: rc, Table: "notes"}, "add")})
	if err != nil {
		t.Fatal(err)
	}
	// Upload accounting takes and releases its own admission before staging.
	result, err := s.PutStorageResource(ctx, ResourceInput{Call: c, Name: "uploads", Kind: "files"})
	if err != nil {
		t.Fatal(err)
	}
	upload := c
	upload.ResourceName = "uploads"
	upload.Resource = result.Resource
	s.deps.FileObjectsLimit = 1000
	if _, auth, err := s.PutFile(ctx, FilePutInput{Call: upload, Path: "example.txt", Body: strings.NewReader("file")}); err != nil || auth != nil {
		t.Fatalf("mixed upload: %+v %v", auth, err)
	}

}

func TestKVMetaRandomOperations(t *testing.T) {
	s, c := kvFixture(t, false)
	rng := rand.New(rand.NewSource(20401))
	expected := map[string]string{}
	for i := 0; i < 60; i++ {
		key := "key-" + strconv.Itoa(rng.Intn(15))
		if rng.Intn(3) == 0 {
			if _, err := s.DeleteKV(context.Background(), KVDeleteInput{Call: c, Key: key}); err != nil {
				t.Fatal(err)
			}
			delete(expected, key)
		} else {
			value := strconv.Itoa(rng.Intn(100000))
			kvPut(t, s, c, key, value, "")
			expected[key] = value
		}
	}
	var want int64
	for k, v := range expected {
		want += int64(len(k) + len(v))
	}
	err := s.deps.SiteSlots.Admit(context.Background(), c.SiteID, func(ctx context.Context, _ *Admission) error {
		conn, done, err := runtime.OpenRuntimeKV(ctx, runtime.RuntimeKVPath(c.RuntimeDir, c.ResourceName), false, false)
		if err != nil {
			return err
		}
		defer done()
		meta, err := runtime.RuntimeKVMeta(conn)
		if err != nil {
			return err
		}
		stmt, _, err := conn.Prepare(`SELECT count(*),coalesce(sum(length(CAST(key AS BLOB))+length(CAST(value AS BLOB))),0) FROM kv`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		if !stmt.Step() {
			return sql.ErrNoRows
		}
		if meta.Entries != int64(len(expected)) || meta.LogicalBytes != want || meta.Entries != stmt.ColumnInt64(0) || meta.LogicalBytes != stmt.ColumnInt64(1) {
			t.Fatalf("meta %+v, expected %d/%d", meta, len(expected), want)
		}
		return stmt.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
}

// Fill with one-byte values and shortest keys (all non-NUL ASCII, then
// two-byte keys, then three-byte keys). The fixture writes every entry through
// the store, checking the same logical allowance at every prefix. The
// final boundary and refusal go through the service's ordinary write path.
func TestKVTinyEntriesLogicalPool(t *testing.T) {
	// A small configured pool keeps the fixture quick; the rule is the same.
	const limit int64 = 50000
	var keys []string
	var charged int64
	for length := 1; charged < limit; length++ {
		digits := make([]byte, length)
		for i := range digits {
			digits[i] = 1
		}
		for {
			cost := int64(length + 1)
			if charged+cost > limit {
				break
			}
			keys = append(keys, string(digits))
			charged += cost
			i := length - 1
			for i >= 0 && digits[i] == 127 {
				digits[i] = 1
				i--
			}
			if i < 0 {
				break
			}
			digits[i]++
		}
		if charged+int64(length+1) > limit {
			break
		}
	}
	s, c := kvFixture(t, true)
	s.deps.SiteLimit = strconv.FormatInt(limit, 10)
	ctx := context.Background()
	err := s.deps.SiteSlots.Admit(ctx, c.SiteID, func(ctx context.Context, _ *Admission) error {
		conn, done, err := runtime.OpenRuntimeKV(ctx, runtime.RuntimeKVPath(c.RuntimeDir, c.ResourceName), true, false)
		if err != nil {
			return err
		}
		defer done()
		if err = conn.Exec("BEGIN"); err != nil {
			return err
		}
		defer conn.Exec("ROLLBACK")
		var used int64
		for _, key := range keys {
			used += int64(len(key) + 1)
			if used > limit {
				t.Fatal("fixture exceeded logical pool")
			}
			if err = runtime.RuntimeKVPut(conn, key, "1", "", false, 0); err != nil {
				return err
			}
		}
		return conn.Exec("COMMIT; PRAGMA wal_checkpoint(TRUNCATE)")
	})
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(runtime.RuntimeKVPath(c.RuntimeDir, c.ResourceName))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() <= 4*limit {
		t.Fatalf("fixture did not exceed old physical cap: %d", fi.Size())
	}
	t.Logf("%d entries, %d charged bytes, %d physical bytes", len(keys), charged, fi.Size())
	u, err := s.MeasureSiteStorage(ctx, c.SiteID, c.RuntimeDir)
	if err != nil || u.KV != charged {
		t.Fatalf("charge=%+v want %d: %v", u, charged, err)
	}
	// A three-byte key exists; extend its value to consume any remainder.
	last := keys[len(keys)-1]
	value := strings.Repeat("1", int(limit-charged)+1)
	kvPut(t, s, c, last, value, "")
	kvPut(t, s, c, last, value+"1", "site_full")
	kvPut(t, s, c, "not-yet-present", "1", "site_full")
}
