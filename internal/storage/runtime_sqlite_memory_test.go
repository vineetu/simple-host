package storage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	sqlite3 "github.com/ncruces/go-sqlite3"
)

func TestSQLiteMemorySizes(t *testing.T) {
	for _, tc := range []struct{ size, cap, cache int64 }{
		{0, 5 << 20, 256 << 10}, {1_000_000, 6 << 20, 256 << 10}, {10_000_000, 25 << 20, 2 << 20},
	} {
		for _, write := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d/write=%t", tc.size, write), func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "data.sqlite")
				// Reads include WAL bytes; writes budget for growth and exclude WAL.
				for suffix, size := range map[string]int64{"": tc.size / 2, "-wal": tc.size - tc.size/2} {
					f, err := os.Create(path + suffix)
					if err != nil {
						t.Fatal(err)
					}
					if err = f.Truncate(size); err != nil {
						t.Fatal(err)
					}
					f.Close()
				}
				size, err := sqliteReadSize(path)
				if err != nil {
					t.Fatal(err)
				}
				if write {
					size = tc.size/2 + (tc.size - tc.size/2)
				}
				cap, cache := sqliteMemory(size)
				if cap != tc.cap || cache != tc.cache {
					t.Fatalf("cap/cache = %d/%d, want %d/%d", cap, cache, tc.cap, tc.cache)
				}
			})
		}
	}
}

func TestSQLiteMemoryLargeWriteBudgets(t *testing.T) {
	for _, budget := range []int64{1_000_000, 10_000_000, 1 << 30, 2 << 30, 10 << 30} {
		for _, kind := range []string{"table", "legacy", "matrix"} {
			t.Run(fmt.Sprintf("%s/%dBytes", kind, budget), func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "data.sqlite")
				seed, err := sqlite3.Open(path)
				if err != nil {
					t.Fatal(err)
				}
				if err = seed.Exec("CREATE TABLE posts(id INTEGER PRIMARY KEY, body TEXT)"); err != nil {
					t.Fatal(err)
				}
				if err = seed.Close(); err != nil {
					t.Fatal(err)
				}
				fi, err := os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
				cap, cache := sqliteMemory(budget)
				want := int64(2053 << 20)
				if budget == 1_000_000 {
					want = 6 << 20
				}
				if budget == 10_000_000 {
					want = 25 << 20
				}
				if budget >= 2<<30 {
					want = sqliteMemoryCeiling
				}
				if cap != want {
					t.Fatalf("cap=%d want=%d", cap, want)
				}
				ctx := context.Background()
				switch kind {
				case "table":
					c, done, e := OpenTableConn(ctx, path, true, time.Second, 0, func() (int64, error) { return budget - fi.Size(), nil })
					if e != nil {
						t.Fatal(e)
					}
					defer done()
					checkMemoryPragmas(t, c, cap, cache)
					err = c.Exec("INSERT INTO posts(body) VALUES ('ok')")
				case "legacy":
					c, e := OpenLegacySQLite(ctx, path, 0, budget, true)
					if e != nil {
						t.Fatal(e)
					}
					defer c.Close()
					checkMemoryPragmas(t, c, cap, cache)
					err = c.Exec("INSERT INTO posts(body) VALUES ('ok')")
				case "matrix":
					// Adding the ownership column and index is a real committed schema write.
					_, err = PrepareMatrixDatabase(ctx, path, 0, budget, fi.Size(), fi.Size(), false, false, func(string) bool { return true }, func(string) bool { return false }, func(e error) { t.Error(e) }, func() {}, func(map[string]bool) {})
				}
				if err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func checkMemoryPragmas(t *testing.T, c *sqlite3.Conn, cap, cache int64) {
	t.Helper()
	if got := SQLiteMemoryLimit(c.GetInterrupt()); got != cap {
		t.Fatalf("connection cap=%d want=%d", got, cap)
	}
	for query, want := range map[string]int64{"PRAGMA cache_size": -cache / 1024, "PRAGMA temp_store": 2} {
		s, _, err := c.Prepare(query)
		if err != nil {
			t.Fatal(err)
		}
		if !s.Step() || s.ColumnInt64(0) != want {
			t.Fatalf("%s: want %d", query, want)
		}
		s.Close()
	}
}

func TestSQLiteMemoryReadIncludesWAL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.sqlite")
	writer, err := sqlite3.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if err = writer.Exec("PRAGMA journal_mode=WAL; PRAGMA wal_autocheckpoint=0; CREATE TABLE posts(body BLOB); INSERT INTO posts VALUES(zeroblob(1000000))"); err != nil {
		t.Fatal(err)
	}
	db, _ := os.Stat(path)
	wal, err := os.Stat(path + "-wal")
	if err != nil {
		t.Fatal(err)
	}
	size, err := sqliteReadSize(path)
	if err != nil {
		t.Fatal(err)
	}
	if size != db.Size()+wal.Size() {
		t.Fatal("WAL was not counted")
	}
	cap, cache := sqliteMemory(size)
	c, done, err := OpenTableConn(context.Background(), path, false, time.Second, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	checkMemoryPragmas(t, c, cap, cache)
	done()
	uri, err := LegacySQLiteQueryURI(path)
	if err != nil {
		t.Fatal(err)
	}
	c, err = OpenLegacySQLite(context.Background(), uri, 0, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	checkMemoryPragmas(t, c, cap, cache)
	c.Close()
	if err = CheckpointExportSQLite(context.Background(), path, 0); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteMemoryMissingRead(t *testing.T) {
	c, err := OpenLegacySQLite(context.Background(), ":memory:", 0, 10_000_000, false)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	checkMemoryPragmas(t, c, 5<<20, 256<<10)
	path := filepath.Join(t.TempDir(), "missing.sqlite")
	c, done, err := OpenTableConn(context.Background(), path, false, time.Second, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	checkMemoryPragmas(t, c, 5<<20, 256<<10)
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("read created a DB file")
	}
}
