package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	repo "github.com/vsriram/simple-host/internal/repository/storage"
	runtime "github.com/vsriram/simple-host/internal/storage"
)

// KVMoveOptions selects one of dry-run, apply and verify. Sites empty means all.
type KVMoveOptions struct {
	Mode, Scratch string
	Sites         []string
	Log           io.Writer
}

// KVMover owns the offline copy workflow. The hooks are used only by package
// tests to interrupt boundaries and corrupt a copy before its comparison.
type KVMover struct {
	DB         *sql.DB
	DataDir    string
	slots      *SiteSlots
	boundary   func(string) error
	copyHook   func(string) error
	verifyHook func()
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

func (m *KVMover) at(stage string) error {
	if m.boundary != nil {
		return m.boundary(stage)
	}
	return nil
}

// Run takes the exclusive lock only for apply, before opening any data file.
func (m *KVMover) Run(ctx context.Context, o KVMoveOptions) error {
	if o.Mode != "dry-run" && o.Mode != "apply" && o.Mode != "verify" {
		return fmt.Errorf("choose dry-run, apply or verify")
	}
	if o.Log == nil {
		return fmt.Errorf("a log is required")
	}
	if o.Mode == "dry-run" && o.Scratch == "" {
		return fmt.Errorf("dry-run requires scratch")
	}
	if o.Mode == "dry-run" {
		if err := os.MkdirAll(o.Scratch, 0700); err != nil {
			return err
		}
	}
	if o.Mode == "apply" {
		lock, err := runtime.LockMaintenance(m.DataDir, true)
		if err != nil {
			return err
		}
		defer lock.Close()
	}
	if m.slots == nil {
		m.slots = NewSiteSlots(1, NewSQLiteGate(1, time.Second), time.Second)
	}
	names, err := repo.KVMoveNamespaces(ctx, m.DB)
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
		if o.Mode != "verify" && n.Store != "postgres" {
			continue
		}
		r := KVMoveReport{Site: n.SiteID, Resource: n.Name, Mode: o.Mode, Status: "ok"}
		logged := false
		err = m.slots.Admit(ctx, n.SiteID, func(ctx context.Context, _ *Admission) error {
			if o.Mode == "verify" {
				return m.verify(ctx, n, &r)
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
	rows, used, err := repo.KVMoveSnapshot(ctx, m.DB, n)
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
	if err = repo.KVMoveCommit(ctx, m.DB, n, r.Entries, func() error { return m.at("before-commit") }); err != nil {
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
	current, exists, rows, err := repo.KVMoveCurrent(ctx, m.DB, n)
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
