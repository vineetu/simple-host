package server_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"

	appstorage "github.com/vsriram/simple-host/host/app/storage"
	storagerepo "github.com/vsriram/simple-host/host/repository/storage"
	"github.com/vsriram/simple-host/host/server"
	"github.com/vsriram/simple-host/host/server/servertest"
	"github.com/vsriram/simple-host/host/storage"
)

func TestKVMaintenanceServerLifetime(t *testing.T) {
	dsn := os.Getenv("DB_DSN")
	if dsn == "" {
		t.Skip("DB_DSN unset")
	}
	cfg := servertest.Config(t, dsn)
	app, err := server.Build(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	lock, err := storage.LockMaintenance(cfg.DataDir, true)
	if lock != nil {
		lock.Close()
	}
	if !errors.Is(err, storage.ErrDataDirInUse) {
		t.Errorf("running server did not hold shared lock: %v", err)
	}
	app.Close()
	lock, err = storage.LockMaintenance(cfg.DataDir, true)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	app, err = server.Build(cfg, nil)
	if app != nil {
		app.Close()
	}
	if !errors.Is(err, storage.ErrMaintenanceRunning) {
		t.Fatalf("server startup during maintenance: %v", err)
	}
}

// The server creates the install identity once, when neither the database nor
// the data directory has one, and holds the install advisory lock shared for
// as long as it lives. A mismatch is logged and does not stop it.
func TestInstallIdentityAndLockServerLifetime(t *testing.T) {
	dsn := os.Getenv("DB_DSN")
	if dsn == "" {
		t.Skip("DB_DSN unset")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if _, err = db.Exec(`DELETE FROM install_identity`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Exec(`DELETE FROM install_identity`) })
	cfg := servertest.Config(t, dsn)
	app, err := server.Build(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	inDB, onDisk, err := appstorage.InstallIdentity(ctx, db, cfg.DataDir)
	if err != nil || inDB == "" || inDB != onDisk {
		t.Fatalf("identity after the first start: %q %q %v", inDB, onDisk, err)
	}
	// While it serves, no command can take the database exclusively.
	lock, err := storagerepo.LockInstall(ctx, db, true)
	if lock != nil {
		lock.Close()
	}
	if !errors.Is(err, storagerepo.ErrDatabaseInUse) {
		t.Errorf("running server does not hold the install lock: %v", err)
	}
	app.Close()

	// A second start changes neither copy.
	app, err = server.Build(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	app.Close()
	if againDB, againDisk, _ := appstorage.InstallIdentity(ctx, db, cfg.DataDir); againDB != inDB || againDisk != onDisk {
		t.Fatalf("identity changed on restart: %q %q", againDB, againDisk)
	}

	// The same database with another data directory: the server starts and
	// logs, writes no identity there, and kv-move's check refuses.
	other := servertest.Config(t, dsn)
	app, err = server.Build(other, nil)
	if err != nil {
		t.Fatalf("identity mismatch stopped the server: %v", err)
	}
	app.Close()
	if _, onOther, _ := appstorage.InstallIdentity(ctx, db, other.DataDir); onOther != "" {
		t.Fatalf("identity written into a foreign data directory: %q", onOther)
	}
	if err = appstorage.CheckInstallIdentity(ctx, db, other.DataDir); !errors.Is(err, appstorage.ErrInstallIdentity) {
		t.Fatalf("mismatch not refused: %v", err)
	}

	// Closed servers have released the lock; a command holding it stops a start.
	lock, err = storagerepo.LockInstall(ctx, db, true)
	if err != nil {
		t.Fatalf("install lock not released by closed servers: %v", err)
	}
	app, err = server.Build(cfg, nil)
	if app != nil {
		app.Close()
	}
	if !errors.Is(err, storagerepo.ErrDatabaseMaintenance) || !strings.Contains(err.Error(), "maintenance command") {
		t.Fatalf("server startup during database maintenance: %v", err)
	}
	lock.Close()
	app, err = server.Build(cfg, nil)
	if err != nil {
		t.Fatalf("server startup after maintenance: %v", err)
	}
	app.Close()
}
