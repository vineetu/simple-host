package kv

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

	"github.com/vsriram/simple-host/host/data"
	"github.com/vsriram/simple-host/host/data/resources"
	"github.com/vsriram/simple-host/host/data/storetest"

	sqlite3 "github.com/ncruces/go-sqlite3"
	repository "github.com/vsriram/simple-host/host/data/repo"
	runtime "github.com/vsriram/simple-host/host/storage"
)

func kvFixture(t *testing.T, combined bool) (*data.Service, data.Call) {
	t.Helper()
	db := storetest.DB(t)
	var owner, site string
	if err := db.QueryRow(`INSERT INTO users(username) VALUES(gen_random_uuid()::text) RETURNING id`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Exec(`DELETE FROM users WHERE id=$1`, owner) })
	if err := db.QueryRow(`INSERT INTO sites(user_id,name) VALUES($1,'kv-test') RETURNING id`, owner).Scan(&site); err != nil {
		t.Fatal(err)
	}
	gate := data.NewSQLiteGate(2, time.Second)
	slots := data.NewSiteSlots(1, gate, time.Second)
	// A goroutine-level hook catches nested admission even if a test uses a
	// site capacity greater than one (where nesting might otherwise pass).
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
	disk := storetest.Disk(t)
	s := data.New(db, data.Deps{Disk: disk, CombinedPool: combined, SiteSlots: slots, Locks: &sync.Map{}, WriteLockWait: time.Second, WriteTimeout: 30 * time.Second, SQLiteTimeout: 30 * time.Second})
	c := data.Call{Owner: true, OwnerID: owner, SiteID: site, SiteName: "kv-test", ResourceName: "data", RuntimeDir: filepath.Join(disk.SiteDir(owner, "kv-test"), "runtime")}
	result, err := resources.PutStorageResource(s, context.Background(), resources.ResourceInput{Call: c, Name: "data", Kind: "kv"})
	if err != nil {
		t.Fatal(err)
	}
	c.Resource = result.Resource
	return s, c
}

func kvPut(t *testing.T, s *data.Service, c data.Call, key, value string, want string) {
	t.Helper()
	_, auth, err := PutKV(s, context.Background(), KVPutInput{Call: c, Key: key, Body: strings.NewReader(`{"value":` + value + `}`)})
	if auth != nil {
		t.Fatalf("unexpected auth: %+v", auth)
	}
	if want == "" {
		if err != nil {
			t.Fatal(err)
		}
		return
	}
	if e, ok := err.(*data.Error); !ok || e.Code != want {
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
		got, err := GetKV(s, ctx, KVReadInput{Call: c, Key: key})
		if err != nil {
			t.Fatal(err)
		}
		normalized, err := repository.KVNormalize(ctx, s.DB, json.RawMessage(`{"b":1,"a":0,"b":1.0e2}`))
		if err != nil {
			t.Fatal(err)
		}
		if string(got.Item.Value) != normalized {
			t.Fatalf("normalized: %s != %s", got.Item.Value, normalized)
		}
	}
	got, err := ListKV(s, ctx, KVListInput{Call: c, Prefix: "x%_\\", Limit: 100, ValidLimit: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 1 || got.Items[0].Key != "x%_\\one" {
		t.Fatalf("literal prefix: %+v", got)
	}
	all, err := ListKV(s, ctx, KVListInput{Call: c, Limit: 100, ValidLimit: true})
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
		limit := data.SiteLimitBytes("", combined)
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
		if _, err = DeleteKV(s, context.Background(), KVDeleteInput{Call: c, Key: "key"}); err != nil {
			t.Fatal(err)
		}
		u, err = s.MeasureSiteStorage(context.Background(), c.SiteID, c.RuntimeDir)
		if err != nil || u.KV != 0 || s.BudgetUsed(u) != limit-4 {
			t.Fatalf("delete charge: %+v %v", u, err)
		}
	}
}

func TestKVMetaRandomOperations(t *testing.T) {
	s, c := kvFixture(t, false)
	rng := rand.New(rand.NewSource(20401))
	expected := map[string]string{}
	for i := 0; i < 60; i++ {
		key := "key-" + strconv.Itoa(rng.Intn(15))
		if rng.Intn(3) == 0 {
			if _, err := DeleteKV(s, context.Background(), KVDeleteInput{Call: c, Key: key}); err != nil {
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
	err := s.Deps.SiteSlots.Admit(context.Background(), c.SiteID, func(ctx context.Context, _ *data.Admission) error {
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
	s.Deps.SiteLimit = strconv.FormatInt(limit, 10)
	ctx := context.Background()
	err := s.Deps.SiteSlots.Admit(ctx, c.SiteID, func(ctx context.Context, _ *data.Admission) error {
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

// Full-mode KV writers are cleared in their own SQLite transaction when
// rules replace the legacy mode.
func TestRulesSaveClearsKVWriters(t *testing.T) {
	s, c := kvFixture(t, false)
	kvPut(t, s, c, "old", `1`, "")
	if err := withKV(s, context.Background(), c, true, func(_ context.Context, conn *sqlite3.Conn, _ *data.Admission) error {
		return conn.Exec(`UPDATE kv SET visitor_id='forged' WHERE key='old'`)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`UPDATE site_storage_resources SET acc_read=NULL,write_policy='anyone',write_mode='full' WHERE site_id=$1 AND name='data'`, c.SiteID); err != nil {
		t.Fatal(err)
	}
	storetest.PutRules(t, s, resources.ResourceInput{Call: c, Name: "data", Kind: "kv", Rules: storetest.Set()})
	var stored string
	err := withKV(s, context.Background(), c, false, func(_ context.Context, conn *sqlite3.Conn, _ *data.Admission) error {
		stmt, _, err := conn.Prepare(`SELECT visitor_id FROM kv WHERE key='old'`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		if stmt.Step() {
			stored = stmt.ColumnText(0)
		}
		return stmt.Err()
	})
	if err != nil || stored != "" {
		t.Fatalf("KV writer: %q %v", stored, err)
	}
}
