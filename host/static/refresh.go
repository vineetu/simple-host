package static

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"os"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"github.com/vsriram/simple-host/host/model"
)

const DefaultRefreshInterval = 10 * time.Second

type Loader func(context.Context) (model.SiteTable, time.Time, error)

type Options struct {
	Lookup                     func(context.Context, model.RoutingAddress) (model.SiteTable, error)
	DataDir, StateDir, Product string
	// RefreshInterval is STATIC_REFRESH_SECONDS, wired by the process slice.
	RefreshInterval time.Duration
	Load            Loader
	Logf            func(string, ...any)
}

// Reloader owns the full-load loop. Current is immutable after publication;
// callers must not mutate it. Failed loads never replace a usable table.
type Reloader struct {
	publishMu sync.Mutex
	options   Options
	current   atomic.Pointer[Snapshot]
	// fullLoadedAt is protected by publishMu, independently of table swaps.
	fullLoadedAt           time.Time
	mu                     sync.Mutex
	loadFailed, saveFailed bool
}

// New validates the saved copy before exposing it. No database call is made,
// so a valid saved file works with the database entirely unavailable.
func New(options Options) *Reloader {
	if options.RefreshInterval <= 0 {
		options.RefreshInterval = DefaultRefreshInterval
	}
	if options.Logf == nil {
		options.Logf = log.Printf
	}
	r := &Reloader{options: options}
	snapshot, err := loadSnapshot(options.StateDir, options.DataDir, options.Product, time.Now())
	if err == nil {
		r.current.Store(snapshot)
		r.fullLoadedAt = snapshot.LoadedAt
	} else if !os.IsNotExist(err) {
		options.Logf("static: ignoring saved routing copy: %v", err)
	}
	return r
}
func (r *Reloader) Current() *Snapshot { return r.current.Load() }

// Refresh also permits deterministic tests and an initial load before Run.
// The mutex serializes it with the loop; there can never be concurrent loads.
func (r *Reloader) Refresh(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	table, started, err := r.options.Load(ctx)
	if err != nil {
		if !r.loadFailed {
			r.options.Logf("static: routing load failed: %v", err)
		}
		r.loadFailed = true
		return err
	}
	r.loadFailed = false
	r.publishMu.Lock()
	defer r.publishMu.Unlock()
	old := r.current.Load()
	if old == nil || !reflect.DeepEqual(old.Table, table) {
		old = &Snapshot{Table: table, LoadedAt: started}
		r.current.Store(old)
	}
	r.fullLoadedAt = started
	// Every successful full load renews persistence, including retries after a
	// failed save or a missing install identity. Keep unchanged routing data.
	next := &Snapshot{Table: old.Table, LoadedAt: started}
	if err = saveSnapshot(r.options.StateDir, r.options.DataDir, r.options.Product, next, nil); err != nil {
		if !r.saveFailed {
			r.options.Logf("static: routing copy was not saved: %v", err)
		}
		r.saveFailed = true
	} else {
		r.saveFailed = false
	}
	return nil
}

// Run starts with a full load, then waits one interval after each completed
// attempt. A slow load delays the next tick rather than queuing another load.
func (r *Reloader) Run(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		_ = r.Refresh(ctx)
		timer := time.NewTimer(r.options.RefreshInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// ErrNoCopy is distinct from a missing address: the process must answer 503
// with Retry-After until its first successful full load or valid saved copy.
var ErrNoCopy = errors.New("no routing copy available")

// Lookup is rule 8's one database lookup for an address absent from the copy.
// The HTTP boundary (D4-S4a) calls this once on a miss, then resolves again.
// It is independent of the periodic load, so a slow refresh cannot hold up a
// miss. A query failure never publishes anything and never guesses a folder.
func (r *Reloader) Lookup(ctx context.Context, address model.RoutingAddress) (bool, error) {
	if r.Current() == nil {
		return false, ErrNoCopy
	}
	ctx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	fragment, err := r.options.Lookup(ctx, address)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if len(fragment.Sites) == 0 && len(fragment.Groups) == 0 {
		return false, nil
	}
	r.publishMu.Lock()
	defer r.publishMu.Unlock()
	old := r.current.Load()
	table := old.Table
	table.Owners = append([]model.RouteOwner(nil), table.Owners...)
	table.Sites = append([]model.RouteSite(nil), table.Sites...)
	table.Families = append([]model.AddressFamily(nil), table.Families...)
	table.Groups = append([]model.RouteGroup(nil), table.Groups...)
	table.GroupSites = append([]model.RouteGroupSite(nil), table.GroupSites...)
	for _, g := range fragment.Groups {
		found := false
		for i, old := range table.Groups {
			if old.ID == g.ID {
				table.Groups[i], found = g, true
				break
			}
		}
		if !found {
			table.Groups = append(table.Groups, g)
		}
	}
	for _, site := range fragment.GroupSites {
		found := false
		for _, old := range table.GroupSites {
			if old == site {
				found = true
				break
			}
		}
		if !found {
			table.GroupSites = append(table.GroupSites, site)
		}
	}
	for _, u := range fragment.Owners {
		found := false
		for i, v := range table.Owners {
			if u.ID == v.ID {
				table.Owners[i] = u
				found = true
				break
			}
		}
		if !found {
			table.Owners = append(table.Owners, u)
		}
	}
	for _, s := range fragment.Sites {
		found := false
		for i, v := range table.Sites {
			if s.ID == v.ID {
				table.Sites[i] = s
				found = true
				break
			}
		}
		if !found {
			table.Sites = append(table.Sites, s)
		}
	}
	for _, f := range fragment.Families {
		found := false
		for i, v := range table.Families {
			if f.ID == v.ID {
				table.Families[i] = f
				found = true
				break
			}
		}
		if !found {
			table.Families = append(table.Families, f)
		}
	}
	// Retain the full load's age: a fresh single-site lookup cannot extend the
	// life of every other saved address.
	next := &Snapshot{Table: table, LoadedAt: r.fullLoadedAt}
	r.current.Store(next)
	if err = saveSnapshot(r.options.StateDir, r.options.DataDir, r.options.Product, next, nil); err != nil {
		if !r.saveFailed {
			r.options.Logf("static: routing copy was not saved: %v", err)
		}
		r.saveFailed = true
	} else {
		r.saveFailed = false
	}
	return true, nil
}
