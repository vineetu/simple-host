package storage

import (
	"context"
	"database/sql"
	"sync"
	"time"
)

// Deps holds the storage service configuration.
type Deps struct {
	Locks                 *sync.Map
	CombinedPool          bool
	SiteLimit, FilesLimit string
}

// Service owns site storage workflows.
type Service struct {
	db   *sql.DB
	deps Deps
}

// New constructs a storage service.
func New(db *sql.DB, deps Deps) *Service { return &Service{db: db, deps: deps} }

// WriteLock acquires the shared site lock and rechecks the resource before
// starting the bounded write context. The recheck runs while the lock is held.
func (s *Service) WriteLock(ctx context.Context, key string, wait, timeout time.Duration, recheck func(context.Context) bool) (context.Context, func(), error) {
	lockCtx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	mu, _ := s.deps.Locks.LoadOrStore(key, &sync.Mutex{})
	m := mu.(*sync.Mutex)
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for !m.TryLock() {
		select {
		case <-lockCtx.Done():
			return nil, nil, &Error{Code: "storage_busy", Message: "storage is busy; retry shortly"}
		case <-tick.C:
		}
	}
	if recheck != nil && !recheck(lockCtx) {
		m.Unlock()
		return nil, nil, &Error{Code: "resource_changed", Message: "resource changed; retry"}
	}
	writeCtx, writeCancel := context.WithTimeout(ctx, timeout)
	var once sync.Once
	return writeCtx, func() { once.Do(func() { writeCancel(); m.Unlock() }) }, nil
}
