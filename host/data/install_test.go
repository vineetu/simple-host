package data

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/data/repo"
	runtime "github.com/vsriram/simple-host/host/storage"
)

func TestEnsureInstallIdentity(t *testing.T) {
	db := coreDB(t)
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
