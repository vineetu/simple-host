package storage

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
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
