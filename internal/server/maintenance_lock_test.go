package server_test

import (
	"errors"
	"os"
	"testing"

	"github.com/vsriram/simple-host/internal/server"
	"github.com/vsriram/simple-host/internal/server/servertest"
	"github.com/vsriram/simple-host/internal/storage"
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
