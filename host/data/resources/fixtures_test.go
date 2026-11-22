package resources

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	goruntime "runtime"
	"strings"
	"sync"
	"testing"
	"time"

	runtime "github.com/vsriram/simple-host/host/storage"

	_ "github.com/lib/pq"
	"github.com/vsriram/simple-host/host/data"
	"github.com/vsriram/simple-host/host/testdb"
)

func TestMain(m *testing.M) { testdb.Main(m) }

// kvFixture is an owner's site "kv-test" with the key-value namespace "data", its
// storage service admitting one call at a time and failing the test on a
// nested admission. combined: one pool for KV, SQLite and files.
func kvFixture(t *testing.T, combined bool) (*data.Service, data.Call) {
	t.Helper()
	db := testDB(t)
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
	disk := storageDisk(t)
	s := data.New(db, data.Deps{Disk: disk, CombinedPool: combined, SiteSlots: slots, Locks: &sync.Map{}, WriteLockWait: time.Second, WriteTimeout: 30 * time.Second, SQLiteTimeout: 30 * time.Second})
	c := data.Call{Owner: true, OwnerID: owner, SiteID: site, SiteName: "kv-test", ResourceName: "data", RuntimeDir: filepath.Join(disk.SiteDir(owner, "kv-test"), "runtime")}
	result, err := PutStorageResource(s, context.Background(), ResourceInput{Call: c, Name: "data", Kind: "kv"})
	if err != nil {
		t.Fatal(err)
	}
	c.Resource = result.Resource
	return s, c
}

// codeOf is the refusal's or the failure's code ("" when there is neither).
func codeOf(auth *data.Error, err error) string {
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

// storageDisk is a site tree in a temporary directory.
func storageDisk(t *testing.T) *runtime.DiskStorage {
	t.Helper()
	disk, err := runtime.NewDiskStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return disk
}

// testDB is the test's database (DB_DSN); the test skips without one.
func testDB(t *testing.T) *sql.DB {
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
