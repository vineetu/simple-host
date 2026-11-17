package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	repo "github.com/vsriram/simple-host/host/repository/storage"
	runtime "github.com/vsriram/simple-host/host/storage"
)

// seedInstallIdentity makes dir and the test database one install, the way a
// server's first start does: the database's id (created if it has none) is
// written to dir.
func seedInstallIdentity(t *testing.T, db *sql.DB, dir string) string {
	t.Helper()
	id, err := repo.CreateInstallID(context.Background(), db, "test-install-"+filepath.Base(dir))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(runtime.InstallIDPath(dir), []byte(id+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	return id
}

// moveSnapshot is everything a refusal must leave alone: every file under the
// data directory (path, mode, size, content) and every row a move touches.
func moveSnapshot(t *testing.T, m *KVMover) string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(m.DataDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		line := fmt.Sprintf("%s %v %d", path, info.Mode(), info.Size())
		if !d.IsDir() {
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(b)
			line += " " + hex.EncodeToString(sum[:])
		} else {
			line = fmt.Sprintf("%s %v dir", path, info.Mode())
		}
		out = append(out, line)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	for _, q := range []string{
		`SELECT coalesce(string_agg(site_id::text||'/'||name||'/'||kind||'/'||kv_store||'/'||coalesce(rules::text,'-'), ',' ORDER BY site_id, name),'') FROM site_storage_resources`,
		`SELECT coalesce(string_agg(site_id::text||'/'||resource_name||'/'||key||'/'||value::text||'/'||writer_id||'/'||created_at::text||'/'||updated_at::text, ',' ORDER BY site_id, resource_name, key),'') FROM site_storage_kv`,
		`SELECT coalesce(string_agg(id||'/'||created_at::text, ','),'') FROM install_identity`,
	} {
		var got string
		if err := m.DB.QueryRow(q).Scan(&got); err != nil {
			t.Fatal(err)
		}
		out = append(out, got)
	}
	return strings.Join(out, "\n")
}

// otherPool is a second set of connections to the test database: a session
// advisory lock is re-entrant inside one session, so exclusion is only visible
// from another.
func otherPool(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("postgres", os.Getenv("DB_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// waitInstallLockFree waits for a killed child's database lock to go. Postgres
// drops it when it notices the connection ended, which is not instant; an
// operator rerunning after exit 2 waits the same way.
func waitInstallLockFree(t *testing.T, db *sql.DB) {
	t.Helper()
	for deadline := time.Now().Add(15 * time.Second); len(installLockHolders(t, db)) != 0; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the crashed child's database lock was never released")
		}
	}
}

func installLockHolders(t *testing.T, db *sql.DB) (pids []int) {
	t.Helper()
	rows, err := db.Query(`SELECT pid FROM pg_locks WHERE locktype='advisory' AND granted AND database=(SELECT oid FROM pg_database WHERE datname=current_database()) AND ((classid::bigint << 32) | objid::bigint) = $1 ORDER BY pid`, int64(repo.InstallLockKey))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var pid int
		if err := rows.Scan(&pid); err != nil {
			t.Fatal(err)
		}
		pids = append(pids, pid)
	}
	return pids
}

// Every identity refusal, in every mode that must refuse, changes nothing on
// disk or in the database.
func TestKVMoveRefusesWrongInstallIdentity(t *testing.T) {
	breaks := map[string]func(t *testing.T, m *KVMover){
		"directory differs": func(t *testing.T, m *KVMover) {
			if err := os.WriteFile(runtime.InstallIDPath(m.DataDir), []byte("another-install\n"), 0644); err != nil {
				t.Fatal(err)
			}
		},
		"database differs": func(t *testing.T, m *KVMover) {
			if _, err := m.DB.Exec(`UPDATE install_identity SET id='another-install'`); err != nil {
				t.Fatal(err)
			}
		},
		"missing in directory": func(t *testing.T, m *KVMover) {
			if err := os.Remove(runtime.InstallIDPath(m.DataDir)); err != nil {
				t.Fatal(err)
			}
		},
		"missing in database": func(t *testing.T, m *KVMover) {
			if _, err := m.DB.Exec(`DELETE FROM install_identity`); err != nil {
				t.Fatal(err)
			}
		},
		"missing in both": func(t *testing.T, m *KVMover) {
			if err := os.Remove(runtime.InstallIDPath(m.DataDir)); err != nil {
				t.Fatal(err)
			}
			if _, err := m.DB.Exec(`DELETE FROM install_identity`); err != nil {
				t.Fatal(err)
			}
		},
	}
	shared := admissionDB(t)
	for name, breakIt := range breaks {
		t.Run(name, func(t *testing.T) {
			// The row is shared by the package's tests: leave none behind.
			t.Cleanup(func() { shared.Exec(`DELETE FROM install_identity`) })
			for _, mode := range []string{"apply", "rollback", "verify"} {
				m, _, c := moveFixture(t)
				if mode != "apply" {
					moveRun(t, m, c, "apply")
				}
				// One namespace stays on Postgres and one is on SQLite (after
				// an apply), so each mode has work it would otherwise do.
				if _, err := m.DB.Exec(`INSERT INTO site_storage_resources(site_id,name,kind) VALUES($1,'second','kv')`, c.SiteID); err != nil {
					t.Fatal(err)
				}
				if _, err := m.DB.Exec(`INSERT INTO site_storage_kv(site_id,resource_name,key,value) VALUES($1,'second','k','1')`, c.SiteID); err != nil {
					t.Fatal(err)
				}
				good, err := os.ReadFile(runtime.InstallIDPath(m.DataDir))
				if err != nil {
					t.Fatal(err)
				}
				// No lock file yet: a refusal must not create one either.
				os.Remove(filepath.Join(m.DataDir, ".maintenance.lock"))
				breakIt(t, m)
				before := moveSnapshot(t, m)
				var log bytes.Buffer
				err = m.Run(context.Background(), KVMoveOptions{Mode: mode, Sites: []string{c.SiteID}, Log: &log})
				if !errors.Is(err, ErrInstallIdentity) {
					t.Fatalf("%s: not refused: %v", mode, err)
				}
				if log.Len() != 0 {
					t.Fatalf("%s: refusal wrote a report: %s", mode, &log)
				}
				if after := moveSnapshot(t, m); after != before {
					t.Fatalf("%s: refusal changed something:\nbefore %s\nafter  %s", mode, before, after)
				}
				// Put this fixture's pair back; the command then runs.
				m.DB.Exec(`DELETE FROM install_identity`)
				if _, err = repo.CreateInstallID(context.Background(), m.DB, strings.TrimSpace(string(good))); err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(runtime.InstallIDPath(m.DataDir), good, 0644); err != nil {
					t.Fatal(err)
				}
				moveRun(t, m, c, mode)
			}
		})
	}
}

// Dry run reads Postgres and writes only under --scratch; it is not tied to a
// data directory's identity.
func TestKVMoveDryRunNeedsNoIdentity(t *testing.T) {
	m, _, c := moveFixture(t)
	if err := os.Remove(runtime.InstallIDPath(m.DataDir)); err != nil {
		t.Fatal(err)
	}
	if got := moveRun(t, m, c, "dry-run"); len(got) != 1 {
		t.Fatalf("dry-run: %+v", got)
	}
}

func TestKVMoveRefusesWrongOwner(t *testing.T) {
	for _, mode := range []string{"dry-run", "apply", "rollback", "verify"} {
		m, _, c := moveFixture(t)
		if mode == "rollback" || mode == "verify" {
			moveRun(t, m, c, "apply")
		}
		os.Remove(filepath.Join(m.DataDir, ".maintenance.lock"))
		m.euid = func() int { return os.Geteuid() + 1 }
		scratch := filepath.Join(t.TempDir(), "scratch")
		before := moveSnapshot(t, m)
		var log bytes.Buffer
		err := m.Run(context.Background(), KVMoveOptions{Mode: mode, Scratch: scratch, Sites: []string{c.SiteID}, Log: &log})
		if !errors.Is(err, runtime.ErrDataDirOwner) || !strings.Contains(err.Error(), m.DataDir) {
			t.Fatalf("%s: wrong owner not refused: %v", mode, err)
		}
		if after := moveSnapshot(t, m); after != before || log.Len() != 0 {
			t.Fatalf("%s: refusal changed something or wrote a report (%s):\nbefore %s\nafter  %s", mode, &log, before, after)
		}
		if _, err := os.Stat(scratch); !os.IsNotExist(err) {
			t.Fatalf("%s: refusal created the scratch directory: %v", mode, err)
		}
		m.euid = nil
		moveRun(t, m, c, mode)
	}
	// A data directory that does not exist is refused, not created.
	m, _, _ := moveFixture(t)
	missing := filepath.Join(m.DataDir, "absent")
	other := KVMover{DB: m.DB, DataDir: missing}
	if err := other.Run(context.Background(), KVMoveOptions{Mode: "apply", Log: io.Discard}); !errors.Is(err, runtime.ErrDataDirOwner) {
		t.Fatalf("missing data directory: %v", err)
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("missing data directory was created: %v", err)
	}
}

// The install advisory lock: a serving program's shared hold refuses apply and
// rollback from ANY data directory; the command holds it exclusive while it
// runs and gives it back on success and on error.
func TestKVMoveInstallAdvisoryLock(t *testing.T) {
	m, _, c := moveFixture(t)
	other := otherPool(t)
	ctx := context.Background()
	if got := installLockHolders(t, other); len(got) != 0 {
		t.Fatalf("install lock held before the test: %v", got)
	}

	// A live server on this database, using another directory entirely.
	hold, err := HoldInstallShared(ctx, os.Getenv("DB_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	if !hold.Held() || len(installLockHolders(t, other)) != 1 {
		t.Fatal("shared hold not held")
	}
	second, err := HoldInstallShared(ctx, os.Getenv("DB_DSN"))
	if err != nil {
		t.Fatalf("two servers cannot share the lock: %v", err)
	}
	second.Close()
	before := moveSnapshot(t, m)
	for _, mode := range []string{"apply", "rollback"} {
		var log bytes.Buffer
		err = m.Run(ctx, KVMoveOptions{Mode: mode, Sites: []string{c.SiteID}, Log: &log})
		if !errors.Is(err, repo.ErrDatabaseInUse) || log.Len() != 0 {
			t.Fatalf("%s beside a live server on the database: %v %s", mode, err, &log)
		}
	}
	// The lock file is the one thing taking the first lock may create.
	os.Remove(filepath.Join(m.DataDir, ".maintenance.lock"))
	if after := moveSnapshot(t, m); after != before {
		t.Fatalf("refusal changed something:\nbefore %s\nafter  %s", before, after)
	}
	// Verify and dry run take no lock and run beside the server.
	moveRun(t, m, c, "verify")
	moveRun(t, m, c, "dry-run")
	hold.Close()
	if got := installLockHolders(t, other); len(got) != 0 {
		t.Fatalf("closed hold still holds the lock: %v", got)
	}

	// Held exclusive for the whole run; a server cannot start meanwhile.
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
		finished <- m.Run(ctx, KVMoveOptions{Mode: "apply", Sites: []string{c.SiteID}, Log: io.Discard})
	}()
	<-entered
	if got := installLockHolders(t, other); len(got) != 1 {
		t.Errorf("apply does not hold the install lock: %v", got)
	}
	if h, err := HoldInstallShared(ctx, os.Getenv("DB_DSN")); !errors.Is(err, repo.ErrDatabaseMaintenance) {
		if h != nil {
			h.Close()
		}
		t.Errorf("a server took the lock during apply: %v", err)
	}
	if l, err := repo.LockInstall(ctx, other, true); !errors.Is(err, repo.ErrDatabaseInUse) {
		if l != nil {
			l.Close()
		}
		t.Errorf("a second command took the lock during apply: %v", err)
	}
	close(release)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	if got := installLockHolders(t, other); len(got) != 0 {
		t.Fatalf("lock still held after apply: %v", got)
	}

	// Released after an error too.
	for _, mode := range []string{"rollback", "apply"} {
		stage := map[string]string{"rollback": "rollback-compared", "apply": "snapshot"}[mode]
		m.boundary = func(s string) error {
			if s == stage {
				if got := installLockHolders(t, other); len(got) != 1 {
					t.Errorf("%s does not hold the install lock: %v", mode, got)
				}
				return errors.New("injected failure")
			}
			return nil
		}
		err = m.Run(ctx, KVMoveOptions{Mode: mode, Sites: []string{c.SiteID}, Log: io.Discard})
		if err == nil || !strings.Contains(err.Error(), "injected failure") {
			t.Fatalf("%s: injected failure lost: %v", mode, err)
		}
		if got := installLockHolders(t, other); len(got) != 0 {
			t.Fatalf("lock still held after a failed %s: %v", mode, got)
		}
		l, err := repo.LockInstall(ctx, other, true)
		if err != nil {
			t.Fatalf("lock not free after a failed %s: %v", mode, err)
		}
		l.Close()
		m.boundary = nil
		moveRun(t, m, c, mode)
	}
}

func TestKVMoveStopsAfterInstallConnectionLoss(t *testing.T) {
	for _, stage := range []string{"snapshot", "before-commit", "rollback-compared"} {
		t.Run(stage, func(t *testing.T) {
			m, _, c := moveFixture(t)
			mode, store, count := "apply", "postgres", int64(7)
			if stage == "rollback-compared" {
				moveRun(t, m, c, "apply")
				mode, store, count = "rollback", "sqlite", 0
			}
			other := otherPool(t)
			ctx := context.Background()
			var replacement *repo.InstallLock
			defer func() {
				if replacement != nil {
					replacement.Close()
				}
			}()
			m.boundary = func(s string) error {
				if s != stage {
					return nil
				}
				holders := installLockHolders(t, other)
				if len(holders) != 1 {
					t.Fatalf("holders: %v", holders)
				}
				if _, err := other.Exec(`SELECT pg_terminate_backend($1)`, holders[0]); err != nil {
					t.Fatal(err)
				}
				waitInstallLockFree(t, other)
				var err error
				replacement, err = repo.LockInstall(ctx, other, true)
				if err != nil {
					t.Fatal(err)
				}
				if stage == "snapshot" {
					// Another owner edits a key without changing the row count.
					_, err = other.Exec(`UPDATE site_storage_kv SET value='"after lock loss"' WHERE site_id=$1 AND key='A'`, c.SiteID)
				}
				return err
			}
			if err := m.Run(ctx, KVMoveOptions{Mode: mode, Sites: []string{c.SiteID}, Log: io.Discard}); err == nil {
				t.Fatal("move committed after its lock connection was terminated")
			}
			moveState(t, m, c, store, count)
			if stage == "snapshot" {
				var value string
				if err := other.QueryRow(`SELECT value::text FROM site_storage_kv WHERE site_id=$1 AND key='A'`, c.SiteID).Scan(&value); err != nil || value != `"after lock loss"` {
					t.Fatalf("concurrent edit lost: %s %v", value, err)
				}
			}
		})
	}
}

// A database restart ends the connection and with it the lock; the hold takes
// it again.
func TestInstallHoldRetakesLockAfterConnectionLoss(t *testing.T) {
	db := admissionDB(t)
	old := installHoldInterval
	installHoldInterval = 20 * time.Millisecond
	t.Cleanup(func() { installHoldInterval = old })
	hold, err := HoldInstallShared(context.Background(), os.Getenv("DB_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	defer hold.Close()
	first := installLockHolders(t, db)
	if len(first) != 1 {
		t.Fatalf("holders: %v", first)
	}
	if _, err = db.Exec(`SELECT pg_terminate_backend($1)`, first[0]); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		now := installLockHolders(t, db)
		if len(now) == 1 && now[0] != first[0] && hold.Held() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("lock not taken again: holders %v (was %v)", now, first)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if l, err := repo.LockInstall(context.Background(), db, true); !errors.Is(err, repo.ErrDatabaseInUse) {
		if l != nil {
			l.Close()
		}
		t.Fatalf("exclusive lock beside the retaken hold: %v", err)
	}

	// The serving pool used by requests and workers holds each of its own
	// connections shared. Even if the lifetime hold dies, a live connection
	// prevents maintenance from starting.
	serving, err := repo.OpenServingDB(os.Getenv("DB_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	defer serving.Close()
	if err = serving.Ping(); err != nil {
		t.Fatal(err)
	}
	hold.mu.Lock() // Let maintenance win before the keeper's next check.
	var holdPID int
	if err = hold.lock.Conn().QueryRowContext(context.Background(), `SELECT pg_backend_pid()`).Scan(&holdPID); err != nil {
		hold.mu.Unlock()
		t.Fatal(err)
	}
	if _, err = db.Exec(`SELECT pg_terminate_backend($1)`, holdPID); err != nil {
		hold.mu.Unlock()
		t.Fatal(err)
	}
	if l, err := repo.LockInstall(context.Background(), db, true); !errors.Is(err, repo.ErrDatabaseInUse) {
		if l != nil {
			l.Close()
		}
		hold.mu.Unlock()
		t.Fatalf("live serving pool did not exclude maintenance after hold loss: %v", err)
	}
	for _, pid := range installLockHolders(t, db) {
		if _, err = db.Exec(`SELECT pg_terminate_backend($1)`, pid); err != nil {
			hold.mu.Unlock()
			t.Fatal(err)
		}
	}
	waitInstallLockFree(t, db)
	maintenance, err := repo.LockInstall(context.Background(), db, true)
	hold.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if maintenance != nil {
			maintenance.Close()
		}
	}()
	// Database access fails immediately, even before the keeper detects loss.
	// A dead pooled connection must not reconnect past the exclusive holder.
	for _, query := range []string{`SELECT 1`, `UPDATE install_identity SET id=id`} {
		if _, err = serving.Exec(query); err == nil {
			t.Fatalf("serving pool executed SQL during maintenance: %v", err)
		}
	}
	deadline = time.Now().Add(20 * time.Second)
	for hold.Held() {
		if time.Now().After(deadline) {
			t.Fatal("keeper did not detect the lost hold")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if tx, err := serving.Begin(); !errors.Is(err, repo.ErrDatabaseMaintenance) {
		if tx != nil {
			tx.Rollback()
		}
		t.Fatalf("serving pool began a transaction after failed reacquisition: %v", err)
	}
	maintenance.Close()
	maintenance = nil
	// Reconnection resumes safely once maintenance releases the lock.
	if err = serving.Ping(); err != nil {
		t.Fatalf("serving pool did not resume: %v", err)
	}
}

func TestEnsureInstallIdentity(t *testing.T) {
	db := admissionDB(t)
	ctx := context.Background()
	if _, err := db.Exec(`DELETE FROM install_identity`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Exec(`DELETE FROM install_identity`) })
	dir := t.TempDir()
	// Neither side: created once, the same on both, and then left alone.
	if err := EnsureInstallIdentity(ctx, db, dir); err != nil {
		t.Fatal(err)
	}
	inDB, onDisk, err := InstallIdentity(ctx, db, dir)
	if err != nil || len(inDB) != 32 || inDB != onDisk {
		t.Fatalf("identity: %q %q %v", inDB, onDisk, err)
	}
	if err = EnsureInstallIdentity(ctx, db, dir); err != nil {
		t.Fatal(err)
	}
	if err = CheckInstallIdentity(ctx, db, dir); err != nil {
		t.Fatal(err)
	}
	if againDB, againDisk, _ := InstallIdentity(ctx, db, dir); againDB != inDB || againDisk != onDisk {
		t.Fatalf("identity changed on a second start: %q %q", againDB, againDisk)
	}
	// The database has one and a fresh directory has none: reported, and the
	// server does not adopt the directory.
	fresh := t.TempDir()
	if err = EnsureInstallIdentity(ctx, db, fresh); !errors.Is(err, ErrInstallIdentity) || !strings.Contains(err.Error(), runtime.InstallIDPath(fresh)) {
		t.Fatalf("fresh directory on a used database: %v", err)
	}
	if _, err = os.Stat(runtime.InstallIDPath(fresh)); !os.IsNotExist(err) {
		t.Fatalf("identity written into a foreign directory: %v", err)
	}
	// The directory has one and the database none: reported, no row created.
	if _, err = db.Exec(`DELETE FROM install_identity`); err != nil {
		t.Fatal(err)
	}
	if err = EnsureInstallIdentity(ctx, db, dir); !errors.Is(err, ErrInstallIdentity) {
		t.Fatalf("used directory on a fresh database: %v", err)
	}
	if got, _ := repo.InstallID(ctx, db); got != "" {
		t.Fatalf("identity written into a foreign database: %q", got)
	}
	// Two that differ.
	if _, err = repo.CreateInstallID(ctx, db, "another-install"); err != nil {
		t.Fatal(err)
	}
	if err = EnsureInstallIdentity(ctx, db, dir); !errors.Is(err, ErrInstallIdentity) || !strings.Contains(err.Error(), "differs") {
		t.Fatalf("differing identities: %v", err)
	}
	if got, _ := runtime.ReadInstallID(dir); got != onDisk {
		t.Fatalf("file rewritten: %q", got)
	}
}
