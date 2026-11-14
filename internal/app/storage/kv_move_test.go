package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	siterepo "github.com/vsriram/simple-host/internal/repository/sites"
	repo "github.com/vsriram/simple-host/internal/repository/storage"
	runtime "github.com/vsriram/simple-host/internal/storage"
)

func moveFixture(t *testing.T) (*KVMover, *Service, Call) {
	t.Helper()
	s, c := kvFixture(t, "postgres", false)
	m := &KVMover{DB: s.db, DataDir: t.TempDir()}
	c.RuntimeDir = filepath.Join(m.DataDir, "by-id", c.OwnerID, c.SiteName, "runtime")
	if err := os.MkdirAll(c.RuntimeDir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"A", "a", "%", "_", "x%_\\key", "é", "中"} {
		kvPut(t, s, c, key, `{"b":1.0e2,"a":[1,true,null]}`, "")
	}
	if _, err := s.db.Exec(`UPDATE site_storage_kv SET writer_id='test-visitor',created_at='2020-01-02 03:04:05.123456Z',updated_at='2021-02-03 04:05:06.654321Z' WHERE site_id=$1`, c.SiteID); err != nil {
		t.Fatal(err)
	}
	return m, s, c
}

func moveRun(t *testing.T, m *KVMover, c Call, mode string) []KVMoveReport {
	t.Helper()
	var log bytes.Buffer
	o := KVMoveOptions{Mode: mode, Sites: []string{c.SiteID}, Log: &log}
	if mode == "dry-run" {
		o.Scratch = t.TempDir()
	}
	if err := m.Run(context.Background(), o); err != nil {
		t.Fatalf("%s: %v; log: %s", mode, err, &log)
	}
	var reports []KVMoveReport
	d := json.NewDecoder(&log)
	for {
		var r KVMoveReport
		err := d.Decode(&r)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		reports = append(reports, r)
	}
	return reports
}

func moveState(t *testing.T, m *KVMover, c Call, want string, count int64) {
	t.Helper()
	store, exists, rows, err := repo.KVMoveState(context.Background(), m.DB, repo.KVNamespace{SiteID: c.SiteID, Name: c.ResourceName})
	if err != nil || !exists || store != want || rows != count {
		t.Fatalf("state: %s %v %d %v; want %s %d", store, exists, rows, err, want, count)
	}
}

func TestKVMoveDryRunApplyAndLaterWrites(t *testing.T) {
	m, s, c := moveFixture(t)
	var used int64
	if err := repo.KVUsage(context.Background(), m.DB, c.SiteID, &used); err != nil {
		t.Fatal(err)
	}
	reports := moveRun(t, m, c, "dry-run")
	if len(reports) != 1 || reports[0].Entries != 7 || reports[0].LogicalBytes != used || reports[0].FileEntries != 7 || reports[0].FileLogicalBytes != used || reports[0].CaseKeys != 2 || reports[0].SourceSHA256 != reports[0].FileSHA256 {
		t.Fatalf("dry-run: %+v", reports)
	}
	moveState(t, m, c, "postgres", 7)
	if _, err := os.Stat(runtime.RuntimeKVPath(c.RuntimeDir, "data")); !os.IsNotExist(err) {
		t.Fatalf("dry-run wrote under DATA_DIR: %v", err)
	}
	reports = moveRun(t, m, c, "apply")
	if len(reports) != 1 || reports[0].SourceSHA256 != reports[0].FileSHA256 {
		t.Fatalf("apply: %+v", reports)
	}
	moveState(t, m, c, "sqlite", 0)
	path := runtime.RuntimeKVPath(c.RuntimeDir, "data")
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(path + suffix); !os.IsNotExist(err) {
			t.Fatalf("copy has sidecar %s: %v", suffix, err)
		}
	}
	if got := moveRun(t, m, c, "apply"); len(got) != 0 {
		t.Fatalf("second apply: %+v", got)
	}
	var err error
	c.Resource, err = repo.LoadResource(context.Background(), s.db, c.SiteID, c.ResourceName)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"A", "a", "%", "_", "x%_\\key", "é", "中"} {
		got, err := s.GetKV(context.Background(), KVReadInput{Call: c, Key: key})
		if err != nil || got.Item.VisitorID != "test-visitor" {
			t.Fatalf("key %q: %+v %v", key, got, err)
		}
	}
	kvPut(t, s, c, "new", `[2,3]`, "")
	kvPut(t, s, c, "A", `{"changed":true}`, "")
	auth, err := s.DeleteKV(context.Background(), KVDeleteInput{Call: c, Key: "_"})
	if err != nil || auth != nil {
		t.Fatalf("delete: %+v %v", auth, err)
	}
	reports = moveRun(t, m, c, "verify")
	if len(reports) != 1 || reports[0].Entries != 7 || reports[0].Status != "ok" {
		t.Fatalf("verify after service writes: %+v", reports)
	}
}

func TestKVMoveCopyMismatchesKeepPostgres(t *testing.T) {
	for _, mutation := range []string{`UPDATE kv SET value='null' WHERE key='A'`, `UPDATE kv SET visitor_id='wrong' WHERE key='A'`, `UPDATE kv SET updated_at='2021-02-03T04:05:06.654322Z' WHERE key='A'`} {
		t.Run(mutation, func(t *testing.T) {
			m, _, c := moveFixture(t)
			if _, err := m.DB.Exec(`INSERT INTO site_storage_resources(site_id,name,kind) VALUES($1,'z-empty','kv')`, c.SiteID); err != nil {
				t.Fatal(err)
			}
			m.copyHook = func(path string) error {
				conn, done, err := runtime.OpenRuntimeKV(context.Background(), path, true, false)
				if err != nil {
					return err
				}
				defer done()
				return conn.Exec(mutation)
			}
			err := m.Run(context.Background(), KVMoveOptions{Mode: "apply", Sites: []string{c.SiteID}, Log: io.Discard})
			if !errors.Is(err, ErrKVCopyDifference) {
				t.Fatalf("mismatch not caught: %v", err)
			}
			moveState(t, m, c, "postgres", 7)
			empty := c
			empty.ResourceName = "z-empty"
			moveState(t, m, empty, "sqlite", 0)
			for _, name := range []string{"data.sqlite", ".data.sqlite.tmp"} {
				if _, err := os.Stat(filepath.Join(c.RuntimeDir, "kv", name)); !os.IsNotExist(err) {
					t.Fatalf("copy left behind: %v", err)
				}
			}
		})
	}
}

func TestKVMoveSyncDirectoriesBeforeCommit(t *testing.T) {
	m, _, c := moveFixture(t)
	// Neither runtime nor runtime/kv exists before the copy creates them.
	if err := os.Remove(c.RuntimeDir); err != nil {
		t.Fatal(err)
	}
	var stages []string
	m.boundary = func(stage string) error {
		stages = append(stages, stage)
		if stage == "committed" {
			moveState(t, m, c, "sqlite", 0)
		} else {
			moveState(t, m, c, "postgres", 7)
		}
		return nil
	}
	moveRun(t, m, c, "apply")
	want := []string{"cleaned", "snapshot", "written", "compared", "synced", "renamed", "directory-synced", "runtime-directory-synced", "site-directory-synced", "before-commit", "committed"}
	if !reflect.DeepEqual(stages, want) {
		t.Fatalf("copy ordering: %v; want %v", stages, want)
	}
	moveRun(t, m, c, "verify")
}

// A subprocess really exits at each boundary: deferred cleanup does not run.
// Its Postgres connections and flock are released by the kernel, as on a kill.
func TestKVMoveCrashChild(t *testing.T) {
	stage := os.Getenv("KV_MOVE_CRASH_STAGE")
	if stage == "" {
		return
	}
	m := KVMover{DB: admissionDB(t), DataDir: os.Getenv("KV_MOVE_CRASH_DIR"), boundary: func(s string) error {
		if s == stage {
			os.Exit(86)
		}
		return nil
	}}
	if err := m.Run(context.Background(), KVMoveOptions{Mode: "apply", Sites: []string{os.Getenv("KV_MOVE_CRASH_SITE")}, Log: io.Discard}); err != nil {
		t.Fatal(err)
	}
	t.Fatal("crash boundary was not reached")
}

func TestKVMoveCrashEveryBoundary(t *testing.T) {
	for _, stage := range []string{"cleaned", "snapshot", "written", "compared", "synced", "renamed", "directory-synced", "runtime-directory-synced", "site-directory-synced", "before-commit", "committed"} {
		t.Run(stage, func(t *testing.T) {
			m, _, c := moveFixture(t)
			if err := os.Remove(c.RuntimeDir); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(os.Args[0], "-test.run=^TestKVMoveCrashChild$")
			// Child uses this package's already isolated DB, rather than cloning it.
			cmd.Env = append(os.Environ(), "TESTDB_ISOLATE=0", "KV_MOVE_CRASH_STAGE="+stage, "KV_MOVE_CRASH_DIR="+m.DataDir, "KV_MOVE_CRASH_SITE="+c.SiteID)
			output, err := cmd.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 86 {
				t.Fatalf("crash child: %v %s", err, output)
			}
			if stage == "committed" {
				moveState(t, m, c, "sqlite", 0)
			} else {
				moveState(t, m, c, "postgres", 7)
			}
			moveRun(t, m, c, "apply")
			moveState(t, m, c, "sqlite", 0)
			moveRun(t, m, c, "verify")
		})
	}
}

func TestKVMoveEmptyTrashedAndOtherKinds(t *testing.T) {
	m, s, c := moveFixture(t)
	for _, kind := range []string{"kv", "files", "sqlite"} {
		name := "empty"
		if kind != "kv" {
			name = kind
		}
		if _, err := m.DB.Exec(`INSERT INTO site_storage_resources(site_id,name,kind) VALUES($1,$2,$3)`, c.SiteID, name, kind); err != nil {
			t.Fatal(err)
		}
	}
	for _, part := range []string{"files/files/keep.txt", "sqlite/sqlite.sqlite"} {
		path := filepath.Join(c.RuntimeDir, part)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("untouched"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	disk, err := runtime.NewDiskStorage(m.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	if err = disk.TrashSite(c.OwnerID, c.SiteName, c.SiteID); err != nil {
		t.Fatal(err)
	}
	if err = siterepo.MarkSiteDeleted(context.Background(), m.DB, c.SiteID, 24*time.Hour); err != nil {
		t.Fatal(err)
	}
	reports := moveRun(t, m, c, "apply")
	if len(reports) != 2 {
		t.Fatalf("moved non-KV or missed empty: %+v", reports)
	}
	moveRun(t, m, c, "verify")
	if err = disk.RestoreSite(c.OwnerID, c.SiteName, c.SiteID); err != nil {
		t.Fatal(err)
	}
	if err = siterepo.RestoreDeletedSite(context.Background(), m.DB, c.SiteID); err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{"files/files/keep.txt", "sqlite/sqlite.sqlite"} {
		b, err := os.ReadFile(filepath.Join(c.RuntimeDir, part))
		if err != nil || string(b) != "untouched" {
			t.Fatalf("other resource changed: %s %v", b, err)
		}
	}
	c.Resource, err = repo.LoadResource(context.Background(), m.DB, c.SiteID, c.ResourceName)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.GetKV(context.Background(), KVReadInput{Call: c, Key: "A"}); err != nil {
		t.Fatal(err)
	}
	moveRun(t, m, c, "verify")
}

func TestKVMoveMissingSiteDirectoryIsSkipped(t *testing.T) {
	m, _, c := moveFixture(t)
	if err := os.RemoveAll(filepath.Dir(c.RuntimeDir)); err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	if err := m.Run(context.Background(), KVMoveOptions{Mode: "apply", Sites: []string{c.SiteID}, Log: &log}); err == nil || !strings.Contains(log.String(), "cannot be resolved") {
		t.Fatalf("unresolved: %v %s", err, &log)
	}
	moveState(t, m, c, "postgres", 7)
}

func TestKVMoveVerifyHealthAndChanges(t *testing.T) {
	for _, problem := range []string{"missing", "corrupt", "table", "json", "meta", "postgres-rows"} {
		t.Run(problem, func(t *testing.T) {
			m, _, c := moveFixture(t)
			moveRun(t, m, c, "apply")
			path := runtime.RuntimeKVPath(c.RuntimeDir, "data")
			switch problem {
			case "missing":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "corrupt":
				if err := os.WriteFile(path, []byte("broken"), 0600); err != nil {
					t.Fatal(err)
				}
			case "postgres-rows":
				if _, err := m.DB.Exec(`INSERT INTO site_storage_kv(site_id,resource_name,key,value) VALUES($1,'data','stray','1')`, c.SiteID); err != nil {
					t.Fatal(err)
				}
			default:
				conn, done, err := runtime.OpenRuntimeKV(context.Background(), path, true, false)
				if err != nil {
					t.Fatal(err)
				}
				query := map[string]string{"table": "DROP TABLE kv", "json": "UPDATE kv SET value='oops'", "meta": "UPDATE kv_meta SET entries=999"}[problem]
				err = conn.Exec(query)
				done()
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := m.Run(context.Background(), KVMoveOptions{Mode: "verify", Sites: []string{c.SiteID}, Log: io.Discard}); err == nil {
				t.Fatal("unhealthy namespace accepted")
			}
		})
	}
	t.Run("changed-twice-is-warning", func(t *testing.T) {
		m, s, c := moveFixture(t)
		moveRun(t, m, c, "apply")
		var err error
		c.Resource, err = repo.LoadResource(context.Background(), m.DB, c.SiteID, c.ResourceName)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		m.verifyHook = func() { n++; kvPut(t, s, c, "changing", fmt.Sprint(n), "") }
		reports := moveRun(t, m, c, "verify")
		if n != 2 || !strings.Contains(reports[0].Status, "changed during verify") {
			t.Fatalf("retry: %d %+v", n, reports)
		}
		m.verifyHook = nil
		moveRun(t, m, c, "verify")
	})
	t.Run("postgres-leftover-warning", func(t *testing.T) {
		m, _, c := moveFixture(t)
		path := runtime.RuntimeKVPath(c.RuntimeDir, "data")
		if err := runtime.WriteKVSnapshot(context.Background(), path, nil); err != nil {
			t.Fatal(err)
		}
		reports := moveRun(t, m, c, "verify")
		if !strings.Contains(reports[0].Status, "warning") {
			t.Fatalf("leftover: %+v", reports)
		}
	})
}

func TestKVMoveApplyLockExclusion(t *testing.T) {
	m, _, c := moveFixture(t)
	shared, err := runtime.LockMaintenance(m.DataDir, false)
	if err != nil {
		t.Fatal(err)
	}
	err = m.Run(context.Background(), KVMoveOptions{Mode: "apply", Log: io.Discard})
	if !errors.Is(err, runtime.ErrDataDirInUse) {
		t.Fatalf("server shared lock: %v", err)
	}
	shared.Close()
	entered, release := make(chan struct{}), make(chan struct{})
	m.boundary = func(stage string) error {
		if stage == "cleaned" {
			close(entered)
			<-release
		}
		return nil
	}
	finished := make(chan error, 1)
	go func() {
		finished <- m.Run(context.Background(), KVMoveOptions{Mode: "apply", Sites: []string{c.SiteID}, Log: io.Discard})
	}()
	<-entered
	other := KVMover{DB: m.DB, DataDir: m.DataDir}
	err = other.Run(context.Background(), KVMoveOptions{Mode: "apply", Log: io.Discard})
	if !errors.Is(err, runtime.ErrDataDirInUse) {
		t.Errorf("overlapping apply: %v", err)
	}
	lock, err := runtime.LockMaintenance(m.DataDir, false)
	if lock != nil {
		lock.Close()
	}
	if !errors.Is(err, runtime.ErrMaintenanceRunning) {
		t.Errorf("shared lock while applying: %v", err)
	}
	close(release)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
}
