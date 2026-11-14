package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
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

	"github.com/lib/pq"
	repository "github.com/vsriram/simple-host/internal/repository/storage"
	rules "github.com/vsriram/simple-host/internal/rules/storage"
	runtime "github.com/vsriram/simple-host/internal/storage"
)

func kvFixture(t *testing.T, store string, combined bool) (*Service, Call) {
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
	s := New(db, Deps{KVStore: store, CombinedPool: combined, SiteSlots: slots, Locks: &sync.Map{}, WriteLockWait: time.Second, WriteTimeout: 30 * time.Second, SQLiteTimeout: 30 * time.Second, Committed: func(context.Context) {}, Notify: func(Call, string, map[string]any) {}, MarkChanged: func() {}})
	c := Call{Owner: true, OwnerID: owner, SiteID: site, SiteName: "kv-test", ResourceName: "data", RuntimeDir: t.TempDir()}
	result, err := s.PutStorageResource(context.Background(), ResourceInput{Call: c, Name: "data", Kind: "kv", AccessRequest: rules.AccessRequest{Preset: "private"}})
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

func TestKVStoresNormalizationKeysAndOrdering(t *testing.T) {
	for _, store := range []string{"postgres", "sqlite"} {
		t.Run(store, func(t *testing.T) {
			s, c := kvFixture(t, store, false)
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
			t.Logf("%s key order: %q", store, keys)
			if store == "sqlite" && !sort.StringsAreSorted(keys) {
				t.Fatal("not byte order")
			}
			kvPut(t, s, c, "nul", `"\u0000"`, "invalid_value")
			kvPut(t, s, c, "huge", `1e1000000`, "invalid_value")
			loaded, err := repository.LoadResource(ctx, s.db, c.SiteID, c.ResourceName)
			if err != nil || loaded.KVStore != store {
				t.Fatalf("marker: %+v %v", loaded, err)
			}
			before := loaded.Fingerprint()
			loaded.KVStore = "other"
			if before == loaded.Fingerprint() {
				t.Fatal("marker missing from fingerprint")
			}
		})
	}
}

func TestKVStoresQuotaBoundaries(t *testing.T) {
	for _, combined := range []bool{false, true} {
		for _, store := range []string{"postgres", "sqlite"} {
			t.Run(fmt.Sprintf("%s/combined=%t", store, combined), func(t *testing.T) {
				s, c := kvFixture(t, store, combined)
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
			})
		}
	}
}

func TestKVMixedStoreMarkersAndAdmission(t *testing.T) {
	s, c := kvFixture(t, "postgres", false)
	ctx := context.Background()
	kvPut(t, s, c, "pg", "100", "")
	// A leftover file beside the Postgres marker must be ignored completely.
	if err := s.createKVSQLite(ctx, c, "data"); err != nil {
		t.Fatal(err)
	}
	err := s.deps.SiteSlots.Admit(ctx, c.SiteID, func(ctx context.Context, _ *Admission) error {
		conn, done, err := runtime.OpenRuntimeKV(ctx, runtime.RuntimeKVPath(c.RuntimeDir, "data"), true, false)
		if err != nil {
			return err
		}
		defer done()
		if err = conn.Exec("BEGIN"); err != nil {
			return err
		}
		if err = runtime.RuntimeKVPut(conn, "leftover", `"not authoritative"`, "", false, 0); err != nil {
			return err
		}
		return conn.Exec("COMMIT; PRAGMA wal_checkpoint(TRUNCATE)")
	})
	if err != nil {
		t.Fatal(err)
	}
	s.deps.KVStore = "sqlite"
	result, err := s.PutStorageResource(ctx, ResourceInput{Call: c, Name: "file", Kind: "kv"})
	if err != nil {
		t.Fatal(err)
	}
	fc := c
	fc.ResourceName = "file"
	fc.Resource = result.Resource
	if _, err = os.Stat(runtime.RuntimeKVPath(c.RuntimeDir, "file")); err != nil {
		t.Fatal(err)
	}
	kvPut(t, s, fc, "sqlite", "1", "")
	kvPut(t, s, c, "pg", "1000", "") // Postgres mutation calls external measurement
	u, err := s.MeasureSiteStorage(ctx, c.SiteID, c.RuntimeDir)
	if err != nil || u.KV != 13 {
		t.Fatalf("mixed: %+v %v", u, err)
	}
	var rows int
	if err = s.db.QueryRow(`SELECT count(*) FROM site_storage_kv WHERE site_id=$1 AND resource_name='file'`, c.SiteID).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("SQLite namespace has PG rows: %d %v", rows, err)
	}
	// Flag changes and policy saves keep the existing marker.
	s.deps.KVStore = "postgres"
	result, err = s.PutStorageResource(ctx, ResourceInput{Call: c, Name: "file", Kind: "kv"})
	if err != nil || result.Resource.KVStore != "sqlite" {
		t.Fatalf("resave: %+v %v", result, err)
	}
	// Owner SQL, row write budget and preset preparation each exercise their
	// designated admission form while the site also has a KV file.
	s.deps.SQLitePath = filepath.Join(c.RuntimeDir, "sqlite", "rows.sqlite")
	rc := c
	rc.ResourceName = "rows"
	rc.Resource = rules.Resource{Kind: "sqlite"}
	_, err = s.LegacySQL(ctx, LegacySQLInput{Call: rc, Mode: "schema", ReadBody: func() ([]byte, error) {
		return []byte(`{"sql":"CREATE TABLE notes(id INTEGER PRIMARY KEY, body TEXT)"}`), nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.PutStorageResource(ctx, ResourceInput{Call: rc, Name: "rows", Kind: "sqlite", AccessRequest: rules.AccessRequest{Preset: "personal"}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.TableAdd(ctx, TableInput{Call: rc, Table: "notes", Values: map[string]json.RawMessage{"body": json.RawMessage(`"ok"`)}, Authorized: s.AuthorizeTableWrite(ctx, TableInput{Call: rc, Table: "notes"}, "add")})
	if err != nil {
		t.Fatal(err)
	}
	// Upload accounting takes and releases its own admission before staging.
	result, err = s.PutStorageResource(ctx, ResourceInput{Call: c, Name: "uploads", Kind: "files"})
	if err != nil {
		t.Fatal(err)
	}
	upload := c
	upload.ResourceName = "uploads"
	upload.Resource = result.Resource
	s.deps.FileObjectsLimit = 1000
	s.deps.FilePath = func(call Call, path string) string {
		return filepath.Join(call.RuntimeDir, "files", call.ResourceName, path)
	}
	s.deps.ScanFile = func(Call, string, []byte) {}
	if _, auth, err := s.PutFile(ctx, FilePutInput{Call: upload, Path: "example.txt", Body: strings.NewReader("file")}); err != nil || auth != nil {
		t.Fatalf("mixed upload: %+v %v", auth, err)
	}

}

func TestKVMetaRandomOperations(t *testing.T) {
	s, c := kvFixture(t, "sqlite", false)
	rng := rand.New(rand.NewSource(20401))
	expected := map[string]string{}
	for i := 0; i < 1000; i++ {
		key := "key-" + strconv.Itoa(rng.Intn(100))
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
	t.Log("seed 20401, 1000 operations")
}

// Fill with one-byte values and shortest keys (all non-NUL ASCII, then
// two-byte keys, then three-byte keys). The fixture writes every entry through
// each native store, checking the same logical allowance at every prefix. The
// final boundary and refusal go through the service's ordinary write path.
func TestKVTinyEntriesLogicalPool(t *testing.T) {
	const limit int64 = 1000000
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
	for _, store := range []string{"postgres", "sqlite"} {
		t.Run(store, func(t *testing.T) {
			s, c := kvFixture(t, store, true)
			ctx := context.Background()
			if store == "postgres" {
				tx, err := s.db.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				stmt, err := tx.Prepare(pq.CopyIn("site_storage_kv", "site_id", "resource_name", "key", "value"))
				if err != nil {
					t.Fatal(err)
				}
				var used int64
				for _, key := range keys {
					used += int64(len(key) + 1)
					if used > limit {
						t.Fatal("fixture exceeded logical pool")
					}
					if _, err = stmt.Exec(c.SiteID, c.ResourceName, key, "1"); err != nil {
						t.Fatal(err)
					}
				}
				if _, err = stmt.Exec(); err != nil {
					t.Fatal(err)
				}
				stmt.Close()
				if err = tx.Commit(); err != nil {
					t.Fatal(err)
				}
			} else {
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
			}
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
		})
	}
}
