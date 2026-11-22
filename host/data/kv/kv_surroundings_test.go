package kv

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/vsriram/simple-host/host/data"
	"github.com/vsriram/simple-host/host/data/resources"

	sqlite3 "github.com/ncruces/go-sqlite3"
	runtime "github.com/vsriram/simple-host/host/storage"
)

func TestKVResourceDeleteRestoresWALAndSHM(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "rollback"}[fail], func(t *testing.T) {
			s, c := kvFixture(t, false)
			ctx := context.Background()
			// The fixture's runtime directory is the one its disk stages.
			root := c.RuntimeDir
			path := runtime.RuntimeKVPath(root, "data")
			conn, done, err := runtime.OpenRuntimeKV(ctx, path, true, false)
			if err != nil {
				t.Fatal(err)
			}
			defer done()
			// Keep the writer open: committed bytes genuinely reside in the live WAL.
			if err = conn.Exec("BEGIN IMMEDIATE"); err != nil {
				t.Fatal(err)
			}
			if err = runtime.RuntimeKVPut(conn, "wal-key", `"saved"`, "author", false, 0); err != nil {
				t.Fatal(err)
			}
			if err = conn.Exec("COMMIT"); err != nil {
				t.Fatal(err)
			}
			original := map[string][]byte{}
			for _, suffix := range []string{"", "-wal", "-shm"} {
				b, e := os.ReadFile(path + suffix)
				if e != nil {
					t.Fatal(e)
				}
				original[suffix] = b
			}
			keep := filepath.Join(root, "files", "bucket", "keep")
			if err = os.MkdirAll(filepath.Dir(keep), 0700); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(keep, []byte("untouched"), 0600); err != nil {
				t.Fatal(err)
			}
			if fail {
				// A referencing row forces the declaration delete to fail after disk staging.
				table := "kv_delete_guard_" + c.OwnerID[:8]
				if _, err = s.DB.Exec(`CREATE TABLE ` + table + `(site_id uuid, name text, FOREIGN KEY(site_id,name) REFERENCES site_storage_resources(site_id,name))`); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { s.DB.Exec(`DROP TABLE ` + table) })
				if _, err = s.DB.Exec(`INSERT INTO `+table+` VALUES($1,$2)`, c.SiteID, c.ResourceName); err != nil {
					t.Fatal(err)
				}
			}
			err = resources.DeleteStorageResource(s, ctx, c)
			if (err != nil) != fail {
				t.Fatalf("delete failure=%v: %v", fail, err)
			}
			for suffix, want := range original {
				got, e := os.ReadFile(path + suffix)
				if fail {
					if e != nil || !bytes.Equal(got, want) {
						t.Fatalf("%s not restored: %v", suffix, e)
					}
				} else if !os.IsNotExist(e) {
					t.Fatalf("%s remains: %v", suffix, e)
				}
			}
			if fail {
				got, e := runtime.RuntimeKVGet(conn, "wal-key", "")
				if e != nil || string(got.Value) != `"saved"` {
					t.Fatalf("restored data: %+v %v", got, e)
				}
			}
			if b, e := os.ReadFile(keep); e != nil || string(b) != "untouched" {
				t.Fatalf("file bucket touched: %q %v", b, e)
			}
			matches, _ := filepath.Glob(filepath.Join(root, ".deleting-*"))
			if len(matches) != 0 {
				t.Fatalf("staging remains: %v", matches)
			}
		})
	}
}

// SQLite lock contention on a KV file is reported as busy, not failed.
func TestKVFileBusyErrors(t *testing.T) {
	for _, err := range []error{sqlite3.BUSY, sqlite3.LOCKED} {
		var e *data.Error
		if !errors.As(data.KVFileError(err), &e) || e.Code != "storage_busy" {
			t.Fatal(err)
		}
	}
}

func TestKVMissingFileUsageDoesNotHideOtherFailures(t *testing.T) {
	s, c := kvFixture(t, false)
	ctx := context.Background()
	path := runtime.RuntimeKVPath(c.RuntimeDir, "data")
	kvPut(t, s, c, "key", "1", "")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	u, err := s.MeasureSiteStorage(ctx, c.SiteID, c.RuntimeDir)
	if err != nil || u.KV != 0 {
		t.Fatalf("missing usage: %+v %v", u, err)
	}
	_, err = GetKV(s, ctx, KVReadInput{Call: c, Key: "key"})
	var e *data.Error
	if !errors.As(err, &e) || e.Code != "storage_failed" {
		t.Fatalf("missing read: %v", err)
	}
	// Another namespace can still write when only this file is missing.
	result, err := resources.PutStorageResource(s, ctx, resources.ResourceInput{Call: c, Name: "pg", Kind: "kv"})
	if err != nil {
		t.Fatal(err)
	}
	pg := c
	pg.ResourceName = "pg"
	pg.Resource = result.Resource
	kvPut(t, s, pg, "before", "1", "")
	if err = os.WriteFile(path, []byte("not SQLite"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.MeasureSiteStorage(ctx, c.SiteID, c.RuntimeDir); err == nil {
		t.Fatal("corruption counted as zero")
	}
	if _, _, err = PutKV(s, ctx, KVPutInput{Call: pg, Key: "new", Body: bytes.NewBufferString(`{"value":1}`)}); err == nil {
		t.Fatal("unmeasured write passed")
	}
}

func TestKVFileStatsValuesExcludeKeys(t *testing.T) {
	s, c := kvFixture(t, false)
	kvPut(t, s, c, "long-key", `"é"`, "")
	entries, valueBytes, err := s.KVFileStats(context.Background(), c.SiteID, c.RuntimeDir)
	if err != nil || entries != 1 || valueBytes != 4 {
		t.Fatalf("stats=%d/%d: %v", entries, valueBytes, err)
	}
}
