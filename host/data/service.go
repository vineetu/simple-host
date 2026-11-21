package data

import (
	"context"
	"database/sql"
	"sync"
	"time"

	accountapp "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/data/notify"
	"github.com/vsriram/simple-host/host/sites"
	runtime "github.com/vsriram/simple-host/host/storage"
	ruleengine "github.com/vsriram/simple-host/host/storage/rules"
)

// Deps holds the storage service configuration.
type Deps struct {
	Bindings                                 BindingProvider
	BindingSchema                            ruleengine.BindingSchema
	ProductBindingSchema                     ruleengine.BindingSchema
	Disk                                     *runtime.DiskStorage
	SpamCheckAvailable                       bool
	SQLitePath                               string
	SQLiteGate                               *SQLiteGate
	SiteSlots                                *SiteSlots
	SQLiteTimeout                            time.Duration
	SQLiteMaxMemory                          int64
	Locks                                    *sync.Map
	CombinedPool                             bool
	SiteLimit, FilesLimit                    string
	SpamCheckedMaxBytes, SpamCheckedMaxLinks int
	VisitorSessionIdle                       time.Duration
	WriteLockWait, WriteTimeout              time.Duration
	FileObjectsLimit                         int
	// RecheckResource: a write checks, under the site lock, that its
	// resource is still the one it was authorized against (writeLock).
	RecheckResource bool
	// ExportKey seals and opens file download links.
	ExportKey []byte
	// Notifications and Hosts carry a committed write's event to its
	// subscribers (notify.go); a nil engine sends nothing.
	Notifications *notify.Engine
	Hosts         *sites.Hosts
}

// Service owns site storage workflows.
type Service struct {
	accounts   *accountapp.Service
	db         *sql.DB
	deps       Deps
	sqliteHook func(string) // injected only by package tests
	notified   func(Event)  // injected only by package tests
}

// New constructs a storage service.
func New(db *sql.DB, deps Deps) *Service {
	return &Service{accounts: accountapp.New(db), db: db, deps: deps}
}

// writeLock acquires the shared site lock and, when recheck is set and the
// service rechecks (Deps.RecheckResource), checks that the call's resource is
// still the one it was authorized against before starting the bounded write
// context. The recheck runs while the lock is held.
func (s *Service) writeLock(ctx context.Context, call Call, recheck bool) (context.Context, func(), error) {
	key, wait, timeout := call.OwnerID+"/"+call.SiteName, s.deps.WriteLockWait, s.deps.WriteTimeout
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
	if recheck && s.deps.RecheckResource && !s.resourceUnchanged(lockCtx, call) {
		m.Unlock()
		return nil, nil, &Error{Code: "resource_changed", Message: "resource changed; retry"}
	}
	writeCtx, writeCancel := context.WithTimeout(ctx, timeout)
	var once sync.Once
	return writeCtx, func() { once.Do(func() { writeCancel(); m.Unlock() }) }, nil
}

// resourceUnchanged reports whether call's resource still loads as the
// resource the call was authorized against.
func (s *Service) resourceUnchanged(ctx context.Context, call Call) bool {
	current, err := s.LoadResource(ctx, call.SiteID, call.ResourceName)
	return err == nil && current.Fingerprint() == call.Resource.Fingerprint()
}
