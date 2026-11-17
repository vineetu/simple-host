package storage

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/vsriram/simple-host/host/repository/sqlkit"
	repo "github.com/vsriram/simple-host/host/repository/storage"
	runtime "github.com/vsriram/simple-host/host/storage"
)

// KVMoveOptions selects dry-run, apply, verify or rollback. Sites empty means all.
type KVMoveOptions struct {
	Mode, Scratch string
	Sites         []string
	Log           io.Writer
}

// KVMover owns the offline copy workflow. The hooks are used only by package
// tests to interrupt boundaries and corrupt a copy before its comparison.
type KVMover struct {
	DB         *sql.DB
	db         repo.KVMoveDB
	DataDir    string
	slots      *SiteSlots
	boundary   func(string) error
	copyHook   func(string) error
	verifyHook func()
	euid       func() int
}

// KVMoveReport contains counts and hashes, never keys, values or authors.
type KVMoveReport struct {
	Site             string `json:"site"`
	Resource         string `json:"resource"`
	Mode             string `json:"mode"`
	Entries          int64  `json:"entries"`
	LogicalBytes     int64  `json:"logical_bytes"`
	FileEntries      int64  `json:"file_entries"`
	FileLogicalBytes int64  `json:"file_logical_bytes"`
	SourceSHA256     string `json:"source_sha256,omitempty"`
	FileSHA256       string `json:"file_sha256,omitempty"`
	ByteOrder        bool   `json:"postgres_order_is_byte_order"`
	CaseKeys         int    `json:"case_variant_keys"`
	Status           string `json:"status"`
	Error            string `json:"error,omitempty"`
}

var ErrKVCopyDifference = errors.New("key-value copy differs")

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

// kvSyncDir is replaced by package tests that count directory syncs.
var kvSyncDir = runtime.SyncDir

func (m *KVMover) at(stage string) error {
	if m.boundary != nil {
		return m.boundary(stage)
	}
	return nil
}

// Preflight checks ownership and install identity without writing, so callers
// can refuse before creating even the report file. Run repeats these checks.
func (m *KVMover) Preflight(ctx context.Context, mode string) error {
	euid := os.Geteuid
	if m.euid != nil {
		euid = m.euid
	}
	if err := runtime.CheckDataDirOwner(m.DataDir, euid()); err != nil {
		return err
	}
	if mode != "dry-run" {
		return CheckInstallIdentity(ctx, m.DB, m.DataDir)
	}
	return nil
}

// Run takes the exclusive lock for apply and rollback, before opening any data file.
func (m *KVMover) Run(ctx context.Context, o KVMoveOptions) error {
	if o.Mode != "dry-run" && o.Mode != "apply" && o.Mode != "verify" && o.Mode != "rollback" {
		return fmt.Errorf("choose dry-run, apply, verify or rollback")
	}
	if o.Log == nil {
		return fmt.Errorf("a log is required")
	}
	if o.Mode == "dry-run" && o.Scratch == "" {
		return fmt.Errorf("dry-run requires scratch")
	}
	// Keep the locked connection local to this run; a later run acquires a new
	// hold rather than reusing a connection returned to the pool.
	run := *m
	run.db = m.DB
	m = &run
	// Before anything is created or opened: the right user, then the right
	// pair of database and directory. Both checks read only.
	if err := m.Preflight(ctx, o.Mode); err != nil {
		return err
	}
	if o.Mode == "dry-run" {
		if err := os.MkdirAll(o.Scratch, 0700); err != nil {
			return err
		}
	}
	if o.Mode == "apply" || o.Mode == "rollback" {
		lock, err := runtime.LockMaintenance(m.DataDir, true)
		if err != nil {
			return err
		}
		defer lock.Close()
		// The file lock covers this directory; the advisory lock covers the
		// database, whichever directory a live server uses it from.
		install, err := repo.LockInstall(ctx, m.DB, true)
		if err != nil {
			return err
		}
		defer install.Close()
		m.db = install.Conn()
		// Checked again under both locks: no server can have started between.
		if err := CheckInstallIdentity(ctx, m.db, m.DataDir); err != nil {
			return err
		}
	}
	if m.slots == nil {
		m.slots = NewSiteSlots(1, NewSQLiteGate(1, time.Second), time.Second)
	}
	names, err := repo.KVMoveNamespaces(ctx, m.db)
	if err != nil {
		return err
	}
	selected := map[string]bool{}
	for _, s := range o.Sites {
		selected[s] = true
	}
	var failures []error
	enc := json.NewEncoder(o.Log)
	for _, n := range names {
		if len(selected) > 0 && !selected[n.SiteID] {
			continue
		}
		if o.Mode == "rollback" && n.Store != "sqlite" {
			if err := enc.Encode(KVMoveReport{Site: n.SiteID, Resource: n.Name, Mode: o.Mode, Status: kvAlreadyRestored}); err != nil {
				return err
			}
			continue
		}
		if (o.Mode == "apply" || o.Mode == "dry-run") && n.Store != "postgres" {
			continue
		}
		r := KVMoveReport{Site: n.SiteID, Resource: n.Name, Mode: o.Mode, Status: "ok"}
		logged := false
		if o.Mode == "rollback" && n.HasRules {
			// Refused before the file is opened: nothing on disk or in the
			// database changes, and the other namespaces are still rolled back.
			err = repo.KVRulesRefusal(n)
			r.Status, r.Error = "refused", err.Error()
			failures = append(failures, err)
			if err := enc.Encode(r); err != nil {
				return err
			}
			continue
		}
		err = m.slots.Admit(ctx, n.SiteID, func(ctx context.Context, _ *Admission) error {
			if o.Mode == "verify" {
				return m.verify(ctx, n, &r)
			}
			if o.Mode == "rollback" {
				return m.rollback(ctx, n, &r)
			}
			return m.copy(ctx, n, o, &r, func() error {
				if err := enc.Encode(r); err != nil {
					return err
				}
				logged = true
				return nil
			})
		})
		if err != nil {
			if r.Status == "ok" {
				r.Status = "failed"
			}
			r.Error = err.Error()
			failures = append(failures, err)
		}
		if !logged {
			if err := enc.Encode(r); err != nil {
				return err
			}
		}
	}
	return errors.Join(failures...)
}

// The report statuses of a namespace rollback skips. Neither is a failure.
// A namespace whose marker is already postgres is restored; one still marked
// sqlite that has Postgres rows is still served from SQLite, and saying
// "restored" for it would mislead an operator who reads only the status.
const (
	kvAlreadyRestored = "skipped: already restored (the namespace is already on Postgres)"
	kvStillOnSQLite   = "skipped: still on SQLite; Postgres already holds rows for it (run --verify)"
)

func kvRollbackSkipStatus(store string) string {
	if store == "sqlite" {
		return kvStillOnSQLite
	}
	return kvAlreadyRestored
}

func (m *KVMover) rollback(ctx context.Context, n repo.KVNamespace, r *KVMoveReport) error {
	// Looked at before the file is opened, so a skipped namespace is untouched.
	current, exists, pgRows, err := repo.KVMoveCurrent(ctx, m.db, n)
	if err != nil {
		return err
	}
	if !exists {
		r.Status = "skipped: the namespace no longer exists"
		return nil
	}
	if current.Store != "sqlite" || pgRows != 0 {
		r.Status = kvRollbackSkipStatus(current.Store)
		r.Entries = pgRows
		return nil
	}
	path := runtime.RuntimeKVPath(filepath.Join(m.siteDir(n), "runtime"), n.Name)
	entries, meta, err := runtime.CheckpointKVSnapshot(ctx, path)
	if err != nil {
		return err
	}
	r.FileEntries, r.FileLogicalBytes = meta.Entries, meta.LogicalBytes
	r.FileSHA256 = kvDumpHash(entries)
	rows := make([]repo.KVMoveEntry, 0, len(entries))
	for _, e := range entries {
		created, err := time.Parse(time.RFC3339Nano, e.Created)
		if err != nil {
			return err
		}
		updated, err := time.Parse(time.RFC3339Nano, e.Updated)
		if err != nil {
			return err
		}
		rows = append(rows, repo.KVMoveEntry{Key: e.Key, Value: e.Value, Writer: e.Writer, Created: created, Updated: updated})
	}
	err = repo.KVMoveRollback(ctx, m.db, n, rows, func(restored []repo.KVMoveEntry) error {
		copied := make([]runtime.KVSnapshotEntry, 0, len(restored))
		for _, e := range restored {
			copied = append(copied, runtime.KVSnapshotEntry{Key: e.Key, Value: e.Value, Writer: e.Writer, Created: kvMoveStamp(e.Created), Updated: kvMoveStamp(e.Updated)})
			r.LogicalBytes += int64(len(e.Key) + len(e.Value))
		}
		r.Entries = int64(len(copied))
		r.SourceSHA256 = kvDumpHash(copied)
		if err := compareKVCopy(entries, copied); err != nil {
			return err
		}
		return m.at("rollback-compared")
	})
	if errors.Is(err, repo.ErrKVAlreadyRestored) {
		// Changed between the look above and the transaction; same answer.
		r.Status = kvAlreadyRestored
		if now, ok, _, err := repo.KVMoveCurrent(ctx, m.db, n); err == nil && ok {
			r.Status = kvRollbackSkipStatus(now.Store)
		}
		return nil
	}
	if err != nil {
		return err
	}
	if err = m.at("rollback-committed"); err != nil {
		return err
	}
	if err = os.Rename(path, path+".rolledback-"+time.Now().UTC().Format("20060102T150405.000000000Z")); err != nil {
		r.Status = "warning: Postgres restored; leftover SQLite file ignored (apply removes it)"
		return nil
	}
	if err = m.at("rollback-renamed"); err != nil {
		return err
	}
	// Persist the rename: without it a crash could bring the old name back as
	// a leftover active file.
	if err = kvSyncDir(filepath.Dir(path)); err != nil {
		r.Status = "warning: Postgres restored; the kept file's rename was not synced (" + err.Error() + ")"
	}
	return nil
}

func (m *KVMover) siteDir(n repo.KVNamespace) string {
	if n.Deleted {
		return filepath.Join(m.DataDir, "deleted", n.OwnerID, n.SiteID)
	}
	return filepath.Join(m.DataDir, "by-id", n.OwnerID, n.SiteName)
}

func removeKVCopy(path string) error {
	for _, s := range []string{"", "-wal", "-shm", "-journal"} {
		if err := os.Remove(path + s); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func (m *KVMover) copy(ctx context.Context, n repo.KVNamespace, o KVMoveOptions, r *KVMoveReport, logComparison func() error) error {
	var temp, final string
	if o.Mode == "dry-run" {
		dir, err := os.MkdirTemp(o.Scratch, "kv-move-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(dir)
		temp = filepath.Join(dir, "."+n.Name+".sqlite.tmp")
	} else {
		info, err := os.Stat(m.siteDir(n))
		if err != nil || !info.IsDir() {
			r.Status = "skipped"
			return fmt.Errorf("site directory cannot be resolved")
		}
		final = runtime.RuntimeKVPath(filepath.Join(m.siteDir(n), "runtime"), n.Name)
		temp = filepath.Join(filepath.Dir(final), "."+n.Name+".sqlite.tmp")
		for _, p := range []string{temp, final} {
			if err := removeKVCopy(p); err != nil {
				return err
			}
		}
	}
	if err := m.at("cleaned"); err != nil {
		return err
	}
	rows, used, err := repo.KVMoveSnapshot(ctx, m.db, n)
	if err != nil {
		return err
	}
	r.ByteOrder = true
	caseCounts := map[string]int{}
	entries := make([]runtime.KVSnapshotEntry, 0, len(rows))
	for i, e := range rows {
		if i > 0 && rows[i-1].Key > e.Key {
			r.ByteOrder = false
		}
		caseCounts[strings.ToLower(e.Key)]++
		entries = append(entries, runtime.KVSnapshotEntry{Key: e.Key, Value: e.Value, Writer: e.Writer, Created: kvMoveStamp(e.Created), Updated: kvMoveStamp(e.Updated)})
	}
	for _, count := range caseCounts {
		if count > 1 {
			r.CaseKeys += count
		}
	}
	r.Entries = int64(len(entries))
	r.LogicalBytes = used
	if err = m.at("snapshot"); err != nil {
		return err
	}
	if err = runtime.WriteKVSnapshot(ctx, temp, entries); err != nil {
		return err
	}
	if err = m.at("written"); err != nil {
		return err
	}
	if m.copyHook != nil {
		if err = m.copyHook(temp); err != nil {
			return err
		}
	}
	copied, meta, readErr := runtime.ReadKVSnapshot(ctx, temp)
	r.FileEntries = int64(len(copied))
	for _, e := range copied {
		r.FileLogicalBytes += int64(len(e.Key) + len(e.Value))
	}
	r.SourceSHA256 = kvDumpHash(entries)
	r.FileSHA256 = kvDumpHash(copied)
	if readErr != nil || meta.LogicalBytes != used || compareKVCopy(entries, copied) != nil {
		comparisonErr := errors.Join(ErrKVCopyDifference, readErr)
		r.Status = "failed"
		r.Error = comparisonErr.Error()
		logErr := logComparison()
		removeErr := removeKVCopy(temp)
		return errors.Join(comparisonErr, logErr, removeErr)
	}
	// Record the copy proof before publishing the file or changing the marker.
	if o.Mode == "apply" {
		r.Status = "copy compared"
	}
	if err = logComparison(); err != nil {
		return err
	}
	if err = m.at("compared"); err != nil {
		return err
	}
	if o.Mode == "dry-run" {
		return nil
	}
	f, err := os.OpenFile(temp, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	err = f.Sync()
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = m.at("synced"); err != nil {
		return err
	}
	if err = os.Rename(temp, final); err != nil {
		return err
	}
	if err = m.at("renamed"); err != nil {
		return err
	}
	// Persist the rename and the entries for runtime/kv and runtime themselves.
	// Sync parents even on reruns: an interrupted copy may have created them.
	dirPath := filepath.Dir(final)
	for _, stage := range []string{"directory-synced", "runtime-directory-synced", "site-directory-synced"} {
		dir, err := os.Open(dirPath)
		if err != nil {
			return err
		}
		err = dir.Sync()
		closeErr = dir.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		if err = m.at(stage); err != nil {
			return err
		}
		dirPath = filepath.Dir(dirPath)
	}
	if err = repo.KVMoveCommit(ctx, m.db, n, r.Entries, func() error { return m.at("before-commit") }); err != nil {
		return err
	}
	return m.at("committed")
}

func kvMoveStamp(t time.Time) string {
	return t.UTC().Truncate(time.Microsecond).Format("2006-01-02T15:04:05.000000Z")
}

func compareKVCopy(a, b []runtime.KVSnapshotEntry) error {
	if len(a) != len(b) {
		return ErrKVCopyDifference
	}
	byKey := map[string]runtime.KVSnapshotEntry{}
	for _, e := range a {
		byKey[e.Key] = e
	}
	seen := map[string]bool{}
	for _, e := range b {
		src, ok := byKey[e.Key]
		if !ok || seen[e.Key] || src.Value != e.Value || src.Writer != e.Writer {
			return ErrKVCopyDifference
		}
		seen[e.Key] = true
		var av, bv any
		ad, bd := json.NewDecoder(strings.NewReader(src.Value)), json.NewDecoder(strings.NewReader(e.Value))
		ad.UseNumber()
		bd.UseNumber()
		if ad.Decode(&av) != nil || bd.Decode(&bv) != nil || !reflect.DeepEqual(av, bv) {
			return ErrKVCopyDifference
		}
		for i, stamp := range []string{e.Created, e.Updated} {
			parsed, err := time.Parse(time.RFC3339Nano, stamp)
			want := src.Created
			if i == 1 {
				want = src.Updated
			}
			if err != nil || kvMoveStamp(parsed) != want {
				return ErrKVCopyDifference
			}
		}
	}
	return nil
}

func kvDumpHash(entries []runtime.KVSnapshotEntry) string {
	entries = append([]runtime.KVSnapshotEntry(nil), entries...)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Key < entries[j].Key })
	h := sha256.New()
	for _, e := range entries {
		if stamp, err := time.Parse(time.RFC3339Nano, e.Created); err == nil {
			e.Created = kvMoveStamp(stamp)
		}
		if stamp, err := time.Parse(time.RFC3339Nano, e.Updated); err == nil {
			e.Updated = kvMoveStamp(stamp)
		}
		for _, field := range []string{e.Key, e.Value, e.Writer, e.Created, e.Updated} {
			var size [8]byte
			binary.BigEndian.PutUint64(size[:], uint64(len(field)))
			h.Write(size[:])
			io.WriteString(h, field)
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

// A verify observation includes the catalog location and saved SQLite file data.
// Changes during the read trigger one retry, rather than a false corruption alarm.
type kvObservation struct {
	n      repo.KVNamespace
	exists bool
	rows   int64
	files  [2]os.FileInfo
}

func (m *KVMover) observe(ctx context.Context, n repo.KVNamespace) (kvObservation, error) {
	current, exists, rows, err := repo.KVMoveCurrent(ctx, m.db, n)
	if err != nil {
		return kvObservation{}, err
	}
	o := kvObservation{n: current, exists: exists, rows: rows}
	if !o.exists {
		return o, nil
	}
	path := runtime.RuntimeKVPath(filepath.Join(m.siteDir(o.n), "runtime"), o.n.Name)
	// Readers update shared-memory locks and may create an empty WAL. Neither
	// changes saved data; watch the main file and nonempty WAL only.
	for i, suffix := range []string{"", "-wal"} {
		o.files[i], err = os.Stat(path + suffix)
		if err != nil && !os.IsNotExist(err) {
			return o, err
		}
		if i == 1 && o.files[i] != nil && o.files[i].Size() == 0 {
			o.files[i] = nil
		}
	}
	return o, nil
}

func sameKVObservation(a, b kvObservation) bool {
	if a.n != b.n || a.exists != b.exists || a.rows != b.rows {
		return false
	}
	for i, x := range a.files {
		y := b.files[i]
		if x == nil || y == nil {
			if x != nil || y != nil {
				return false
			}
			continue
		}
		if !os.SameFile(x, y) || x.Size() != y.Size() || !x.ModTime().Equal(y.ModTime()) {
			return false
		}
	}
	return true
}

func (m *KVMover) verify(ctx context.Context, n repo.KVNamespace, r *KVMoveReport) error {
	for attempt := 0; attempt < 2; attempt++ {
		r.Status = "ok"
		before, err := m.observe(ctx, n)
		if err != nil {
			return err
		}
		var checkErr error
		if before.exists && before.n.Store == "sqlite" {
			path := runtime.RuntimeKVPath(filepath.Join(m.siteDir(before.n), "runtime"), n.Name)
			entries, meta, e := runtime.ReadKVSnapshot(ctx, path)
			checkErr = e
			r.Entries = meta.Entries
			r.LogicalBytes = meta.LogicalBytes
			r.FileEntries = int64(len(entries))
			r.FileLogicalBytes = 0
			for _, e := range entries {
				r.FileLogicalBytes += int64(len(e.Key) + len(e.Value))
			}
			r.FileSHA256 = kvDumpHash(entries)
			if before.rows != 0 {
				checkErr = errors.Join(checkErr, fmt.Errorf("SQLite namespace still has Postgres rows"))
			}
		} else if before.exists && before.n.Store == "postgres" && before.files[0] != nil {
			r.Status = "warning: leftover SQLite file ignored"
		}
		if m.verifyHook != nil {
			m.verifyHook()
		}
		after, err := m.observe(ctx, n)
		if err != nil {
			return err
		}
		if sameKVObservation(before, after) {
			return checkErr
		}
	}
	r.Status = "warning: changed during verify"
	return nil
}
