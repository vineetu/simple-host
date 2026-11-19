package storage

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/vsriram/simple-host/host/repository/sqlkit"
	repo "github.com/vsriram/simple-host/host/repository/storage"
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

// InstallHold is a serving program's shared hold on the install advisory lock.
// The lock lives on one connection of its own, outside the request pool. If
// that connection ends (a database restart) the hold takes the lock again.
type InstallHold struct {
	db     *sql.DB
	mu     sync.Mutex
	lock   *repo.InstallLock
	cancel context.CancelFunc
	done   chan struct{}
}

// installHoldInterval is how often the hold checks its connection.
var installHoldInterval = 15 * time.Second

// HoldInstallShared takes the shared install lock on the database at dsn and
// keeps it until Close. It fails at once with repo.ErrDatabaseMaintenance when
// a maintenance command holds the lock exclusively.
func HoldInstallShared(ctx context.Context, dsn string) (*InstallHold, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	lock, err := repo.LockInstall(ctx, db, false)
	if err != nil {
		db.Close()
		return nil, err
	}
	loopCtx, cancel := context.WithCancel(context.Background())
	h := &InstallHold{db: db, lock: lock, cancel: cancel, done: make(chan struct{})}
	go h.keep(loopCtx)
	return h, nil
}

func (h *InstallHold) keep(ctx context.Context) {
	defer close(h.done)
	tick := time.NewTicker(installHoldInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		h.mu.Lock()
		if h.lock != nil && !h.lock.Alive(ctx) {
			h.lock.Close()
			h.lock = nil
			log.Printf("WARNING: the connection holding this server's install lock ended; taking the lock again")
		}
		if h.lock == nil && ctx.Err() == nil {
			lock, err := repo.LockInstall(ctx, h.db, false)
			switch {
			case err == nil:
				h.lock = lock
				log.Printf("install lock taken again")
			case errors.Is(err, repo.ErrDatabaseMaintenance):
				log.Printf("WARNING: a maintenance command is running on this database; serving pool connections must acquire their own shared lock before database work can resume")
			case ctx.Err() == nil:
				log.Printf("WARNING: could not take the install lock again: %v", err)
			}
		}
		h.mu.Unlock()
	}
}

// Held reports whether the hold has the lock right now.
func (h *InstallHold) Held() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.lock != nil
}

// Close stops the keeper, releases the lock and closes the connection.
func (h *InstallHold) Close() {
	h.cancel()
	<-h.done
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.lock != nil {
		h.lock.Close()
		h.lock = nil
	}
	h.db.Close()
}
