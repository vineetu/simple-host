package storage

import (
	"context"
	"log"
	"runtime"
	"sync"
	"time"

	runtimeStorage "github.com/vsriram/simple-host/internal/storage"
)

// SQLiteGate limits concurrent SQLite work.
type SQLiteGate struct {
	Slots chan struct{}
	wait  time.Duration
}

// NewSQLiteGate creates a gate with the given capacity and wait time.
func NewSQLiteGate(n int, wait time.Duration) *SQLiteGate {
	if n == 0 {
		n = runtime.NumCPU()
	}
	return &SQLiteGate{Slots: make(chan struct{}, n), wait: wait}
}

// Acquire waits for a slot and returns its release function.
func (g *SQLiteGate) Acquire(ctx context.Context) (func(), error) {
	wait, cancel := context.WithTimeout(ctx, g.wait)
	defer cancel()
	select {
	case g.Slots <- struct{}{}:
		return func() { <-g.Slots }, nil
	case <-wait.Done():
		return nil, runtimeStorage.ErrSQLiteBusy
	}
}

// SiteSlots bounds SQLite operations in flight per site and holds the global
// gate. All request-built services share the program's instance.
type SiteSlots struct {
	admitHook func(bool) // tests assert admission is never nested on a goroutine

	mu      sync.Mutex
	sites   map[string]*siteSlot
	perSite int
	gate    *SQLiteGate
	wait    time.Duration
}
type siteSlot struct {
	slots chan struct{}
	refs  int // holders and waiters; remove idle sites
}

func NewSiteSlots(perSite int, gate *SQLiteGate, wait time.Duration) *SiteSlots {
	if perSite < 1 {
		perSite = 1
	}
	return &SiteSlots{sites: make(map[string]*siteSlot), perSite: perSite, gate: gate, wait: wait}
}

// Admission records whether a write has committed.
type Admission struct{ committed bool }

// Committed must be called immediately after commit succeeds, before cleanup.
func (a *Admission) Committed() { a.committed = true }

// Admit takes the site slot before the global gate, using a single wait budget.
// The wait deadline applies to admission only; fn keeps the caller's context.
func (s *SiteSlots) Admit(ctx context.Context, siteID string, fn func(context.Context, *Admission) error) (err error) {
	if s.admitHook != nil {
		s.admitHook(true)
		defer s.admitHook(false)
	}
	wait, cancel := context.WithTimeout(ctx, s.wait)
	defer cancel()
	s.mu.Lock()
	slot := s.sites[siteID]
	if slot == nil {
		slot = &siteSlot{slots: make(chan struct{}, s.perSite)}
		s.sites[siteID] = slot
	}
	slot.refs++
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		slot.refs--
		if slot.refs == 0 {
			delete(s.sites, siteID)
		}
		s.mu.Unlock()
	}()
	if wait.Err() != nil {
		return sqliteBusy()
	}
	select {
	case slot.slots <- struct{}{}:
		defer func() { <-slot.slots }()
	case <-wait.Done():
		return sqliteBusy()
	}
	if wait.Err() != nil {
		return sqliteBusy()
	}
	select {
	case s.gate.Slots <- struct{}{}:
		defer func() { <-s.gate.Slots }()
	case <-wait.Done():
		return sqliteBusy()
	}
	if wait.Err() != nil {
		return sqliteBusy()
	}
	cancel()
	a := &Admission{}
	defer func() {
		if p := recover(); p != nil {
			// Do not log panic payloads: they can contain SQL or caller data.
			log.Printf("storage: recovered SQLite panic (committed=%t)", a.committed)
			if a.committed {
				err = nil
			} else {
				err = sqlitePanicError(p)
			}
		}
	}()
	return sqliteFailure(fn(ctx, a))
}

func sqliteBusy() *Error {
	return &Error{Code: "storage_busy", Message: "storage is busy; retry shortly"}
}

// closeSQLite prevents cleanup from replacing either a committed result or the
// original failure. The close function itself must release its resources in defers.
func closeSQLite(done func()) {
	defer func() {
		if recover() != nil {
			log.Print("storage: recovered SQLite close panic")
		}
	}()
	done()
}
