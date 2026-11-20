package server_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"

	appstorage "github.com/vsriram/simple-host/host/data"
	"github.com/vsriram/simple-host/host/server"
	"github.com/vsriram/simple-host/host/server/servertest"
	"github.com/vsriram/simple-host/host/storage"
	"github.com/vsriram/simple-host/host/testdb"
)

func TestMaintenanceServerLifetime(t *testing.T) {
	dsn := os.Getenv("DB_DSN")
	if dsn == "" {
		t.Skip("DB_DSN unset")
	}
	cfg := servertest.Config(t, dsn)
	app, err := server.Build(cfg, nil, nil)
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
	app, err = server.Build(cfg, nil, nil)
	if app != nil {
		app.Close()
	}
	if !errors.Is(err, storage.ErrMaintenanceRunning) {
		t.Fatalf("server startup during maintenance: %v", err)
	}
}

// The server creates the install identity once, when neither the database nor
// the data directory has one. A mismatch is logged and does not stop it.
func TestInstallIdentityServerLifetime(t *testing.T) {
	dsn := testdb.Fresh(t)
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	cfg := servertest.Config(t, dsn)
	app, err := server.Build(cfg, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	inDB, onDisk, err := appstorage.InstallIdentity(ctx, db, cfg.DataDir)
	if err != nil || inDB == "" || inDB != onDisk {
		t.Fatalf("identity after the first start: %q %q %v", inDB, onDisk, err)
	}
	app.Close()

	// A second start changes neither copy.
	app, err = server.Build(cfg, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	app.Close()
	if againDB, againDisk, _ := appstorage.InstallIdentity(ctx, db, cfg.DataDir); againDB != inDB || againDisk != onDisk {
		t.Fatalf("identity changed on restart: %q %q", againDB, againDisk)
	}

	// The same database with another data directory: the server starts and
	// logs, writes no identity there, and the maintenance check refuses.
	other := servertest.Config(t, dsn)
	app, err = server.Build(other, nil, nil)
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
}
