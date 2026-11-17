package static

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vsriram/simple-host/host/model"
)

func snapshotFixture(t *testing.T) (Options, *Snapshot) {
	t.Helper()
	o := Options{DataDir: t.TempDir(), StateDir: t.TempDir(), Product: "host", Logf: func(string, ...any) {}}
	if err := os.WriteFile(filepath.Join(o.DataDir, ".install-id"), []byte("install-a\n"), 0600); err != nil {
		t.Fatal(err)
	}
	s := &Snapshot{LoadedAt: time.Now().UTC(), Table: model.SiteTable{Owners: []model.RouteOwner{{ID: "owner", Handle: "handle"}}, Sites: []model.RouteSite{{ID: "site", UserID: "owner", Name: "page"}}}}
	if err := saveSnapshot(o.StateDir, o.DataDir, o.Product, s, nil); err != nil {
		t.Fatal(err)
	}
	return o, s
}
func TestSnapshotRestartWithoutDatabase(t *testing.T) {
	for _, tc := range []string{"valid", "corrupt", "checksum", "version", "old", "foreign", "product", "missing-identity", "unreadable-identity"} {
		t.Run(tc, func(t *testing.T) {
			o, s := snapshotFixture(t)
			file := filepath.Join(o.StateDir, snapshotName)
			data, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			var saved savedSnapshot
			if err = json.Unmarshal(data, &saved); err != nil {
				t.Fatal(err)
			}
			switch tc {
			case "corrupt":
				data = []byte("{")
			case "checksum":
				saved.Header.Checksum = "bad"
			case "version":
				saved.Header.Version = 2
			case "old":
				saved.Header.LoadedAt = time.Now().Add(-8 * 24 * time.Hour)
			case "foreign":
				saved.Header.InstallID = "another-install"
			case "product":
				saved.Header.Product = "another-product"
			case "missing-identity", "unreadable-identity":
				identity := filepath.Join(o.DataDir, ".install-id")
				if err = os.Remove(identity); err != nil {
					t.Fatal(err)
				}
				if tc == "unreadable-identity" {
					if err = os.Mkdir(identity, 0700); err != nil {
						t.Fatal(err)
					}
				}
			}
			if tc != "corrupt" {
				data, err = json.Marshal(saved)
				if err != nil {
					t.Fatal(err)
				}
			}
			if err = os.WriteFile(file, data, 0600); err != nil {
				t.Fatal(err)
			}
			calls, logs := 0, 0
			o.Load = func(context.Context) (model.SiteTable, time.Time, error) {
				calls++
				return model.SiteTable{}, time.Time{}, errors.New("database down")
			}
			o.Logf = func(string, ...any) { logs++ }
			r := New(o)
			if calls != 0 {
				t.Fatal("startup tried to reach the database")
			}
			if tc == "valid" {
				if r.Current() == nil || r.Current().Table.Sites[0].ID != s.Table.Sites[0].ID {
					t.Fatal("valid saved copy refused")
				}
			} else if r.Current() != nil {
				t.Fatal("invalid saved copy accepted")
			}
			if tc == "foreign" && logs != 1 {
				t.Fatalf("foreign snapshot logs=%d", logs)
			}
		})
	}
}
func TestSnapshotNoWriteWithoutIdentity(t *testing.T) {
	o, s := snapshotFixture(t)
	if err := os.Remove(filepath.Join(o.DataDir, ".install-id")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(o.StateDir, snapshotName)); err != nil {
		t.Fatal(err)
	}
	o.Load = func(context.Context) (model.SiteTable, time.Time, error) { return s.Table, s.LoadedAt, nil }
	r := New(o)
	if r.Current() != nil {
		t.Fatal("loaded without identity")
	}
	if err := r.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r.Current() == nil {
		t.Fatal("fresh successful load was not published")
	}
	for _, p := range []string{filepath.Join(o.DataDir, ".install-id"), filepath.Join(o.StateDir, snapshotName)} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("unexpected identity/saved file: %v", err)
		}
	}
}
func TestSnapshotCrashBeforeRename(t *testing.T) {
	if dir := os.Getenv("D4_SNAPSHOT_CRASH_DIR"); dir != "" {
		s := &Snapshot{LoadedAt: time.Now(), Table: model.SiteTable{Owners: []model.RouteOwner{{ID: "replacement"}}}}
		_ = saveSnapshot(dir, dir, "host", s, func() error { os.Exit(23); return nil })
		os.Exit(24)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".install-id"), []byte("test-install"), 0600); err != nil {
		t.Fatal(err)
	}
	old := &Snapshot{LoadedAt: time.Now(), Table: model.SiteTable{Owners: []model.RouteOwner{{ID: "original"}}}}
	if err := saveSnapshot(dir, dir, "host", old, nil); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(dir, snapshotName))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestSnapshotCrashBeforeRename$")
	cmd.Env = append(os.Environ(), "D4_SNAPSHOT_CRASH_DIR="+dir)
	err = cmd.Run()
	var exited *exec.ExitError
	if !errors.As(err, &exited) || exited.ExitCode() != 23 {
		t.Fatalf("child did not stop before rename: %v", err)
	}
	after, err := os.ReadFile(filepath.Join(dir, snapshotName))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("interrupted write replaced previous file")
	}
	loaded, err := loadSnapshot(dir, dir, "host", time.Now())
	if err != nil || loaded.Table.Owners[0].ID != "original" {
		t.Fatalf("previous copy unusable: %v", err)
	}
}
func TestRefreshFailureStreakAndUnchangedTable(t *testing.T) {
	o, s := snapshotFixture(t)
	failure := true
	logs := 0
	o.Logf = func(format string, _ ...any) {
		if strings.Contains(format, "routing load failed") {
			logs++
		}
	}
	o.Load = func(context.Context) (model.SiteTable, time.Time, error) {
		if failure {
			return model.SiteTable{}, time.Time{}, errors.New("down")
		}
		return s.Table, time.Now(), nil
	}
	r := New(o)
	old := r.Current()
	for range 2 {
		if err := r.Refresh(context.Background()); err == nil {
			t.Fatal("failure hidden")
		}
	}
	if r.Current() != old || logs != 1 {
		t.Fatalf("failed load replaced copy or repeated log: %d", logs)
	}
	failure = false
	if err := r.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r.Current() != old {
		t.Fatal("identical table swapped")
	}
	failure = true
	_ = r.Refresh(context.Background())
	if logs != 2 {
		t.Fatal("next failure streak was not logged")
	}
	failure = false
	s.Table.Sites = append(s.Table.Sites, model.RouteSite{ID: "new", Name: "new"})
	if err := r.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r.Current() == old || len(r.Current().Table.Sites) != 2 {
		t.Fatal("changed table not swapped")
	}
	reloaded := New(o)
	if reloaded.Current() == nil || len(reloaded.Current().Table.Sites) != 2 {
		t.Fatal("changed table not saved")
	}
}
func TestSlowRefreshNeverOverlaps(t *testing.T) {
	o, _ := snapshotFixture(t)
	o.RefreshInterval = 10 * time.Millisecond
	entered := make(chan struct{}, 4)
	release := make(chan struct{})
	var active, max atomic.Int32
	o.Load = func(ctx context.Context) (model.SiteTable, time.Time, error) {
		n := active.Add(1)
		if n > max.Load() {
			max.Store(n)
		}
		defer active.Add(-1)
		entered <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
			return model.SiteTable{}, time.Time{}, ctx.Err()
		}
		return model.SiteTable{}, time.Now(), nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	r := New(o)
	go func() { defer close(done); r.Run(ctx) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("load did not start")
	}
	// Keep the first load across several intervals. There must be no second.
	select {
	case <-entered:
		t.Fatal("overlapping load")
	case <-time.After(4 * o.RefreshInterval):
	}
	release <- struct{}{}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("next interval never ran")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("loop did not stop")
	}
	if max.Load() != 1 {
		t.Fatalf("maximum concurrent loads=%d", max.Load())
	}
}

func TestMissingAddressLookupBoundedAndNoCopy(t *testing.T) {
	o, _ := snapshotFixture(t)
	calls := 0
	o.Lookup = func(ctx context.Context, _ model.RoutingAddress) (model.SiteTable, error) {
		calls++
		<-ctx.Done()
		return model.SiteTable{}, ctx.Err()
	}
	r := New(o)
	old := r.Current()
	start := time.Now()
	if ok, err := r.Lookup(context.Background(), model.RoutingAddress{Handle: "new", Name: "page"}); ok || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("lookup: %v %v", ok, err)
	}
	if calls != 1 || time.Since(start) > time.Second || r.Current() != old {
		t.Fatal("miss was unbounded, repeated, or replaced the copy")
	}
	if err := os.Remove(filepath.Join(o.StateDir, snapshotName)); err != nil {
		t.Fatal(err)
	}
	empty := New(o)
	if _, err := empty.Lookup(context.Background(), model.RoutingAddress{}); !errors.Is(err, ErrNoCopy) {
		t.Fatal("no-copy start did not refuse")
	}
	if calls != 1 {
		t.Fatal("no-copy start attempted an address lookup")
	}
}

func TestUnchangedRefreshRepairsSavedCopy(t *testing.T) {
	for _, failure := range []string{"save-failed", "missing-identity"} {
		t.Run(failure, func(t *testing.T) {
			o, s := snapshotFixture(t)
			file := filepath.Join(o.StateDir, snapshotName)
			if err := os.Remove(file); err != nil {
				t.Fatal(err)
			}
			identity := filepath.Join(o.DataDir, ".install-id")
			if failure == "save-failed" {
				// A directory where the saved file belongs makes rename fail.
				if err := os.Mkdir(file, 0700); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Remove(identity); err != nil {
				t.Fatal(err)
			}
			started := time.Now().UTC()
			o.Load = func(context.Context) (model.SiteTable, time.Time, error) { return s.Table, started, nil }
			r := New(o)
			if err := r.Refresh(context.Background()); err != nil {
				t.Fatal(err)
			}
			first := r.Current()
			if first == nil || !r.saveFailed {
				t.Fatal("expected a usable table with failed persistence")
			}
			if failure == "save-failed" {
				if err := os.Remove(file); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(identity, []byte("install-a\n"), 0600); err != nil {
				t.Fatal(err)
			}
			started = started.Add(time.Minute)
			if err := r.Refresh(context.Background()); err != nil {
				t.Fatal(err)
			}
			if r.Current() != first || r.saveFailed {
				t.Fatal("unchanged load did not repair persistence without swapping")
			}
			o.Load = func(context.Context) (model.SiteTable, time.Time, error) {
				return model.SiteTable{}, time.Time{}, errors.New("database down")
			}
			restarted := New(o)
			if restarted.Current() == nil || !restarted.Current().LoadedAt.Equal(started) {
				t.Fatal("repaired saved copy unusable without database")
			}
			if err := restarted.Refresh(context.Background()); err == nil || restarted.Current() == nil {
				t.Fatal("failed load discarded repaired copy")
			}
		})
	}
}

func TestUnchangedRefreshRenewsSavedCopy(t *testing.T) {
	o, s := snapshotFixture(t)
	s.LoadedAt = time.Now().UTC().Add(-snapshotMaxAge + time.Hour)
	if err := saveSnapshot(o.StateDir, o.DataDir, o.Product, s, nil); err != nil {
		t.Fatal(err)
	}
	started := time.Now().UTC()
	o.Load = func(context.Context) (model.SiteTable, time.Time, error) { return s.Table, started, nil }
	r := New(o)
	first := r.Current()
	if first == nil {
		t.Fatal("initial copy refused")
	}
	if err := r.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r.Current() != first {
		t.Fatal("unchanged routing data swapped")
	}
	// At this restart time the original saved file would have expired.
	restartAt := time.Now().Add(2 * time.Hour)
	renewed, err := loadSnapshot(o.StateDir, o.DataDir, o.Product, restartAt)
	if err != nil || !renewed.LoadedAt.Equal(started) {
		t.Fatalf("successful reload did not renew saved copy: %v", err)
	}
	o.Lookup = func(context.Context, model.RoutingAddress) (model.SiteTable, error) {
		return model.SiteTable{Sites: []model.RouteSite{{ID: "new", Name: "new"}}}, nil
	}
	r.options.Lookup = o.Lookup
	if ok, err := r.Lookup(context.Background(), model.RoutingAddress{Domain: "new.example.org"}); err != nil || !ok {
		t.Fatalf("lookup: %v %v", ok, err)
	}
	renewed, err = loadSnapshot(o.StateDir, o.DataDir, o.Product, restartAt)
	if err != nil || !renewed.LoadedAt.Equal(started) {
		t.Fatalf("miss lookup lost full-load freshness: %v", err)
	}
	o.Load = func(context.Context) (model.SiteTable, time.Time, error) {
		return model.SiteTable{}, time.Time{}, errors.New("database down")
	}
	restarted := New(o)
	if restarted.Current() == nil || !restarted.Current().LoadedAt.Equal(started) {
		t.Fatal("renewed file unusable at restart without database")
	}
	if err := restarted.Refresh(context.Background()); err == nil {
		t.Fatal("failed load hidden")
	}
	renewed, err = loadSnapshot(o.StateDir, o.DataDir, o.Product, started.Add(snapshotMaxAge+time.Second))
	if err == nil || renewed != nil {
		t.Fatal("failed full load or miss extended saved freshness")
	}
}
