package storage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	repo "github.com/vsriram/simple-host/host/repository/storage"
	runtime "github.com/vsriram/simple-host/host/storage"
)

func reloadMoveCall(t *testing.T, m *KVMover, c Call) Call {
	t.Helper()
	var err error
	c.Resource, err = repo.LoadResource(context.Background(), m.DB, c.SiteID, c.ResourceName)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestKVRollbackLaterWritesAndRepeatedMoves(t *testing.T) {
	m, s, c := moveFixture(t)
	for cycle := 0; cycle < 2; cycle++ {
		moveRun(t, m, c, "apply")
		c = reloadMoveCall(t, m, c)
		kvPut(t, s, c, "new", `[2,3]`, "")
		kvPut(t, s, c, "A", `{"changed":true}`, "")
		if auth, err := s.DeleteKV(context.Background(), KVDeleteInput{Call: c, Key: "_"}); err != nil || auth != nil {
			t.Fatalf("delete: %v %v", auth, err)
		}
		before, err := s.ListKV(context.Background(), KVListInput{Call: c, Limit: 100, ValidLimit: true})
		if err != nil {
			t.Fatal(err)
		}
		path := runtime.RuntimeKVPath(c.RuntimeDir, c.ResourceName)
		fileBefore, meta, err := runtime.ReadKVSnapshot(context.Background(), path)
		if err != nil {
			t.Fatal(err)
		}
		reports := moveRun(t, m, c, "rollback")
		moveState(t, m, c, "postgres", 7)
		if len(reports) != 1 || reports[0].SourceSHA256 != reports[0].FileSHA256 || reports[0].Entries != meta.Entries || reports[0].LogicalBytes != meta.LogicalBytes {
			t.Fatalf("rollback proof: %+v", reports)
		}
		c = reloadMoveCall(t, m, c)
		after, err := s.ListKV(context.Background(), KVListInput{Call: c, Limit: 100, ValidLimit: true})
		if err != nil || !reflect.DeepEqual(before, after) {
			t.Fatalf("service changed: before=%+v after=%+v err=%v", before, after, err)
		}
		for _, e := range fileBefore {
			got, err := s.GetKV(context.Background(), KVReadInput{Call: c, Key: e.Key})
			if err != nil || string(got.Item.Value) != e.Value || got.Item.VisitorID != e.Writer {
				t.Fatalf("get %q after rollback: %+v %v", e.Key, got, err)
			}
		}
		kept, err := filepath.Glob(path + ".rolledback-*")
		if err != nil || len(kept) != cycle+1 {
			t.Fatalf("retained copies: %v %v", kept, err)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("active file remains: %v", err)
		}
		moveRun(t, m, c, "verify")
		snapshot := moveSnapshot(t, m)
		if reports := moveRun(t, m, c, "rollback"); len(reports) != 1 || reports[0].Site != c.SiteID || reports[0].Resource != c.ResourceName || reports[0].Status != kvAlreadyRestored {
			t.Fatalf("second rollback did not report already restored: %+v", reports)
		}
		if after := moveSnapshot(t, m); after != snapshot {
			t.Fatalf("second rollback changed data:\nbefore %s\nafter %s", snapshot, after)
		}
	}
}

func TestKVRollbackMismatchRollsBackTransaction(t *testing.T) {
	for _, field := range []string{"value", "writer_id", "updated_at"} {
		t.Run(field, func(t *testing.T) {
			m, _, c := moveFixture(t)
			moveRun(t, m, c, "apply")
			path := runtime.RuntimeKVPath(c.RuntimeDir, c.ResourceName)
			before, _, err := runtime.CheckpointKVSnapshot(context.Background(), path)
			if err != nil {
				t.Fatal(err)
			}
			fileBefore, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			mutation := map[string]string{"value": "NEW.value='null'::jsonb;", "writer_id": "NEW.writer_id='wrong';", "updated_at": "NEW.updated_at=NEW.updated_at+interval '1 microsecond';"}[field]
			// Mutate the INSERT in Postgres, before the mover reads its own
			// uncommitted rows back. This proves the comparison is transactional.
			_, err = m.DB.Exec(`CREATE FUNCTION kv_rollback_mismatch() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.site_id='` + c.SiteID + `' THEN ` + mutation + ` END IF; RETURN NEW; END $$; CREATE TRIGGER kv_rollback_mismatch BEFORE INSERT ON site_storage_kv FOR EACH ROW EXECUTE FUNCTION kv_rollback_mismatch()`)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				m.DB.Exec(`DROP TRIGGER kv_rollback_mismatch ON site_storage_kv; DROP FUNCTION kv_rollback_mismatch()`)
			})
			err = m.Run(context.Background(), KVMoveOptions{Mode: "rollback", Sites: []string{c.SiteID}, Log: io.Discard})
			if !errors.Is(err, ErrKVCopyDifference) {
				t.Fatalf("mismatch not caught: %v", err)
			}
			moveState(t, m, c, "sqlite", 0)
			after, _, err := runtime.ReadKVSnapshot(context.Background(), path)
			fileAfter, readErr := os.ReadFile(path)
			if err != nil || readErr != nil || !reflect.DeepEqual(before, after) || !bytes.Equal(fileBefore, fileAfter) {
				t.Fatalf("SQLite file changed: %v %v", err, readErr)
			}
		})
	}
}

func TestKVRollbackRefusesSavedRules(t *testing.T) {
	m, s, c := moveFixture(t)
	s.deps.PasscodeAllows = func() bool { return true }
	s.deps.ViewersAllow = func() bool { return true }
	moveRun(t, m, c, "apply")
	c = reloadMoveCall(t, m, c)
	s.deps.RulesEnabled = true
	c.Resource = putRules(t, s, ResourceInput{Call: c, Name: c.ResourceName, Kind: "kv", Rules: kvRuleSet("true", "true", "auth.signed_in", "auth.signed_in", "auth.signed_in")})
	kvPut(t, s, c, "later", `{"saved":true}`, "")
	beforeResource := c.Resource.Fingerprint()
	path := runtime.RuntimeKVPath(c.RuntimeDir, c.ResourceName)
	before, metaBefore, err := runtime.CheckpointKVSnapshot(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	fileBefore, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// A second namespace on the same site, without rules: it must still go back.
	plain := c
	plain.ResourceName = "plain"
	if _, err = m.DB.Exec(`INSERT INTO site_storage_resources(site_id,name,kind) VALUES($1,'plain','kv')`, c.SiteID); err != nil {
		t.Fatal(err)
	}
	if _, err = m.DB.Exec(`INSERT INTO site_storage_kv(site_id,resource_name,key,value) VALUES($1,'plain','k','{"a":1}')`, c.SiteID); err != nil {
		t.Fatal(err)
	}
	moveRun(t, m, c, "apply")
	moveState(t, m, plain, "sqlite", 0)
	fileBefore, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sidecarsBefore, _ := filepath.Glob(path + "-*")
	var log bytes.Buffer
	err = m.Run(context.Background(), KVMoveOptions{Mode: "rollback", Sites: []string{c.SiteID}, Log: &log})
	if err == nil || !errors.Is(err, repo.ErrKVHasRules) || !strings.Contains(err.Error(), "saved rules") || !strings.Contains(log.String(), "saved rules") {
		t.Fatalf("saved rules rollback not refused and reported: %v %s", err, &log)
	}
	if !strings.Contains(err.Error(), `"`+c.ResourceName+`"`) || !strings.Contains(err.Error(), c.SiteID) || strings.Contains(err.Error(), `"plain"`) {
		t.Fatalf("refusal does not name the namespace: %v", err)
	}
	if sidecarsAfter, _ := filepath.Glob(path + "-*"); !reflect.DeepEqual(sidecarsBefore, sidecarsAfter) {
		t.Fatalf("refused rollback touched the file's sidecars: %v -> %v", sidecarsBefore, sidecarsAfter)
	}
	moveState(t, m, c, "sqlite", 0)
	moveState(t, m, plain, "postgres", 1)
	c = reloadMoveCall(t, m, c)
	if c.Resource.Fingerprint() != beforeResource {
		t.Fatal("rollback changed resource metadata or rules")
	}
	after, metaAfter, err := runtime.ReadKVSnapshot(context.Background(), path)
	fileAfter, readErr := os.ReadFile(path)
	if err != nil || readErr != nil || !reflect.DeepEqual(before, after) || metaBefore != metaAfter || !bytes.Equal(fileBefore, fileAfter) {
		t.Fatalf("rollback changed the active SQLite file, entries or accounting: %v %v", err, readErr)
	}
	if kept, err := filepath.Glob(path + ".rolledback-*"); err != nil || len(kept) != 0 {
		t.Fatalf("refused rollback renamed the active file: %v %v", kept, err)
	}
	c.Owner = false
	kvRulesGet(t, s, c, "later", "")
}

func TestKVRollbackSkipsAlreadyRestoredAndRefusesSharedLock(t *testing.T) {
	m, _, c := moveFixture(t)
	moveRun(t, m, c, "apply")
	shared, err := runtime.LockMaintenance(m.DataDir, false)
	if err != nil {
		t.Fatal(err)
	}
	err = m.Run(context.Background(), KVMoveOptions{Mode: "rollback", Log: io.Discard})
	shared.Close()
	if !errors.Is(err, runtime.ErrDataDirInUse) {
		t.Fatalf("rollback did not refuse shared lock: %v", err)
	}
	// Postgres already holds rows for a namespace still marked sqlite: skipped
	// and reported, not a failure (exit 0), and neither side is touched.
	if _, err = m.DB.Exec(`INSERT INTO site_storage_kv(site_id,resource_name,key,value) VALUES($1,$2,'stray','1')`, c.SiteID, c.ResourceName); err != nil {
		t.Fatal(err)
	}
	before := moveSnapshot(t, m)
	reports := moveRun(t, m, c, "rollback")
	if len(reports) != 1 || reports[0].Status != kvStillOnSQLite || reports[0].Error != "" {
		t.Fatalf("namespace with Postgres rows not skipped and reported: %+v", reports)
	}
	if after := moveSnapshot(t, m); after != before {
		t.Fatalf("skipped rollback changed something:\nbefore %s\nafter  %s", before, after)
	}
	moveState(t, m, c, "sqlite", 1)
}

// The transaction's own check gives the same answer when the state changes
// after the first look.
func TestKVRollbackAlreadyRestoredInsideTransaction(t *testing.T) {
	m, _, c := moveFixture(t)
	moveRun(t, m, c, "apply")
	n := repo.KVNamespace{SiteID: c.SiteID, Name: c.ResourceName}
	if _, err := m.DB.Exec(`INSERT INTO site_storage_kv(site_id,resource_name,key,value) VALUES($1,$2,'stray','1')`, c.SiteID, c.ResourceName); err != nil {
		t.Fatal(err)
	}
	err := repo.KVMoveRollback(context.Background(), m.DB, n, nil, func([]repo.KVMoveEntry) error { t.Fatal("compared"); return nil })
	if !errors.Is(err, repo.ErrKVAlreadyRestored) {
		t.Fatalf("stray rows: %v", err)
	}
	moveState(t, m, c, "sqlite", 1)
}

func TestKVRollbackSyncsDirectoryAfterRename(t *testing.T) {
	m, _, c := moveFixture(t)
	moveRun(t, m, c, "apply")
	path := runtime.RuntimeKVPath(c.RuntimeDir, c.ResourceName)
	var synced []string
	var keptAtSync []string
	real := kvSyncDir
	kvSyncDir = func(dir string) error {
		synced = append(synced, dir)
		keptAtSync, _ = filepath.Glob(path + ".rolledback-*")
		return real(dir)
	}
	t.Cleanup(func() { kvSyncDir = real })
	var stages []string
	m.boundary = func(stage string) error { stages = append(stages, stage); return nil }
	moveRun(t, m, c, "rollback")
	if len(synced) != 1 || synced[0] != filepath.Dir(path) || len(keptAtSync) != 1 {
		t.Fatalf("directory not synced once after the rename: synced=%v kept=%v", synced, keptAtSync)
	}
	if want := []string{"rollback-compared", "rollback-committed", "rollback-renamed"}; !reflect.DeepEqual(stages, want) {
		t.Fatalf("stages: %v", stages)
	}
	// A failed sync is reported and is not a failure: Postgres is authoritative.
	moveRun(t, m, c, "apply")
	kvSyncDir = func(string) error { return errors.New("sync failed") }
	reports := moveRun(t, m, c, "rollback")
	if len(reports) != 1 || !strings.Contains(reports[0].Status, "not synced") {
		t.Fatalf("failed sync not reported: %+v", reports)
	}
	moveState(t, m, c, "postgres", 7)
}

func TestKVRollbackCrashChild(t *testing.T) {
	stage := os.Getenv("KV_ROLLBACK_CRASH_STAGE")
	if stage == "" {
		return
	}
	m := KVMover{DB: admissionDB(t), DataDir: os.Getenv("KV_MOVE_CRASH_DIR"), boundary: func(s string) error {
		if s == stage {
			os.Exit(86)
		}
		return nil
	}}
	if err := m.Run(context.Background(), KVMoveOptions{Mode: "rollback", Sites: []string{os.Getenv("KV_MOVE_CRASH_SITE")}, Log: io.Discard}); err != nil {
		t.Fatal(err)
	}
	t.Fatal("crash boundary was not reached")
}

func TestKVRollbackCrashBeforeAndAfterCommit(t *testing.T) {
	for _, stage := range []string{"rollback-compared", "rollback-committed"} {
		t.Run(stage, func(t *testing.T) {
			m, _, c := moveFixture(t)
			moveRun(t, m, c, "apply")
			cmd := exec.Command(os.Args[0], "-test.run=^TestKVRollbackCrashChild$")
			cmd.Env = append(os.Environ(), "TESTDB_ISOLATE=0", "KV_ROLLBACK_CRASH_STAGE="+stage, "KV_MOVE_CRASH_DIR="+m.DataDir, "KV_MOVE_CRASH_SITE="+c.SiteID)
			output, err := cmd.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 86 {
				t.Fatalf("crash child: %v %s", err, output)
			}
			waitInstallLockFree(t, m.DB)
			if stage == "rollback-compared" {
				moveState(t, m, c, "sqlite", 0)
				moveRun(t, m, c, "rollback")
			} else {
				moveState(t, m, c, "postgres", 7)
				if _, err := os.Stat(runtime.RuntimeKVPath(c.RuntimeDir, c.ResourceName)); err != nil {
					t.Fatalf("expected ignored leftover: %v", err)
				}
				moveRun(t, m, c, "rollback") // no-op after commit
			}
			moveState(t, m, c, "postgres", 7)
			moveRun(t, m, c, "apply") // removes the ignored leftover
			moveRun(t, m, c, "verify")
			moveRun(t, m, c, "rollback")
		})
	}
}

func TestKVRollbackCheckpointsCommittedWAL(t *testing.T) {
	m, _, c := moveFixture(t)
	moveRun(t, m, c, "apply")
	path := runtime.RuntimeKVPath(c.RuntimeDir, c.ResourceName)
	conn, done, err := runtime.OpenRuntimeKV(context.Background(), path, true, false)
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	if err = conn.Exec(`PRAGMA wal_autocheckpoint=0; BEGIN; UPDATE kv SET visitor_id='wal-writer' WHERE key='A'; COMMIT`); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(path + "-wal"); err != nil || info.Size() == 0 {
		t.Fatalf("fixture has no committed WAL: %v", err)
	}
	moveRun(t, m, c, "rollback")
	var writer string
	if err = m.DB.QueryRow(`SELECT writer_id FROM site_storage_kv WHERE site_id=$1 AND resource_name=$2 AND key='A'`, c.SiteID, c.ResourceName).Scan(&writer); err != nil || writer != "wal-writer" {
		t.Fatalf("committed WAL lost: %s %v", writer, err)
	}
	if info, err := os.Stat(path + "-wal"); err == nil && info.Size() != 0 {
		t.Fatal("WAL not truncated")
	}
}
