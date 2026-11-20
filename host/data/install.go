package data

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/vsriram/simple-host/host/data/repo"
	"github.com/vsriram/simple-host/host/sqlkit"
	runtime "github.com/vsriram/simple-host/host/storage"
)

// ErrInstallIdentity: the database and the data directory are not provably the
// same install. Every message that wraps it says which side is missing, or
// that the two differ.
var ErrInstallIdentity = errors.New("the database and the data directory are not the same install")

// InstallIdentity reads both copies of the install identity. It never writes.
// An empty string means that side has none.
func InstallIdentity(ctx context.Context, db sqlkit.Querier, dataDir string) (inDB, onDisk string, err error) {
	if inDB, err = repo.InstallID(ctx, db); err != nil {
		return "", "", fmt.Errorf("read the install identity from the database: %w", err)
	}
	if onDisk, err = runtime.ReadInstallID(dataDir); err != nil {
		return "", "", fmt.Errorf("read the install identity from the data directory: %w", err)
	}
	return inDB, onDisk, nil
}

// CheckInstallIdentity is the maintenance commands' check: both copies must be
// present and equal. With neither present nothing proves that DB_DSN and
// DATA_DIR belong together, so that is refused as well; the server creates
// both at its next start. It reads only.
func CheckInstallIdentity(ctx context.Context, db sqlkit.Querier, dataDir string) error {
	inDB, onDisk, err := InstallIdentity(ctx, db, dataDir)
	if err != nil {
		return err
	}
	file := runtime.InstallIDPath(dataDir)
	switch {
	case inDB == "" && onDisk == "":
		return fmt.Errorf("%w: neither the database nor %s holds an install identity; start the server once on this database and data directory, stop it, and run the command again", ErrInstallIdentity, file)
	case onDisk == "":
		return fmt.Errorf("%w: the database has an install identity and %s is missing; DB_DSN and DATA_DIR point at different installs, or the data directory was replaced", ErrInstallIdentity, file)
	case inDB == "":
		return fmt.Errorf("%w: %s exists and the database has no install identity; DB_DSN and DATA_DIR point at different installs, or the database was replaced", ErrInstallIdentity, file)
	case inDB != onDisk:
		return fmt.Errorf("%w: the identity in the database (%s) differs from the one in %s (%s); DB_DSN and DATA_DIR point at different installs", ErrInstallIdentity, inDB, file, onDisk)
	}
	return nil
}

// EnsureInstallIdentity is the server's start-up step. With no identity on
// either side it creates one random id and saves it in both places; with a
// matching pair it does nothing. With exactly one side present, or two that
// differ, it changes nothing and returns ErrInstallIdentity for the caller to
// log: the server still serves, and maintenance commands refuse.
func EnsureInstallIdentity(ctx context.Context, db *sql.DB, dataDir string) error {
	inDB, onDisk, err := InstallIdentity(ctx, db, dataDir)
	if err != nil {
		return err
	}
	if inDB != "" || onDisk != "" {
		return CheckInstallIdentity(ctx, db, dataDir)
	}
	var raw [16]byte
	if _, err = rand.Read(raw[:]); err != nil {
		return err
	}
	id := hex.EncodeToString(raw[:])
	// The row first: a second server starting on the same database reads the
	// winner's id back, and only a server whose id won writes the file.
	stored, err := repo.CreateInstallID(ctx, db, id)
	if err != nil {
		return fmt.Errorf("save the install identity in the database: %w", err)
	}
	if stored != id {
		return CheckInstallIdentity(ctx, db, dataDir)
	}
	if err = runtime.WriteInstallID(dataDir, id); err != nil {
		return fmt.Errorf("save the install identity in %s (the database already has it; the two now disagree until the file is written): %w", runtime.InstallIDPath(dataDir), err)
	}
	return nil
}
