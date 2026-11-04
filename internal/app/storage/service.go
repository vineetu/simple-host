package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"sync"
	"time"

	runtime "github.com/vsriram/simple-host/internal/storage"
)

// Deps holds the storage service configuration.
type Deps struct {
	VisitorEmail                             func(context.Context, string) (string, error)
	StageResourceRuntime                     func() (string, func(), error)
	SpamCheckAvailable                       bool
	SQLitePath                               string
	SQLiteGate                               *runtime.SQLiteGate
	SQLiteTimeout                            time.Duration
	SQLiteMaxMemory                          int64
	Locks                                    *sync.Map
	CombinedPool                             bool
	SiteLimit, FilesLimit                    string
	SpamCheckedMaxBytes, SpamCheckedMaxLinks int
	VisitorSessionIdle                       time.Duration
	PasscodeAllows, ViewersAllow             func() bool
	WriteLockWait, WriteTimeout              time.Duration
	Recheck                                  func(context.Context) bool
	Committed                                func(context.Context)
	Notify                                   func(Call, string, map[string]any)
	FilePath                                 func(Call, string) string
	MarkChanged                              func()
	FileToken                                func(Call, string, time.Time) string
	ScanFile                                 func(Call, string, []byte)
	FileObjectsLimit                         int
	OpenFileToken                            func(string) ([]byte, error)
	FileResourceExists                       func(context.Context, string, string) bool
	EventOwners                              bool
	LegacyRoutes                             bool
	ResourceHistory                          func(context.Context, string, string) (any, error)
	TableWriteRequest                        func(Call, *Error, func(map[string]json.RawMessage) (TableResult, error))
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
