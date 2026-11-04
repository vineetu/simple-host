package legacy

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/vsriram/simple-host/internal/db"
)

// Adapter owns Host legacy saved-data compatibility operations.
type Adapter struct {
	database *sql.DB
	cutoff   time.Time
	deps     Deps
}

// Deps contains configuration shared by legacy operations.
type Deps struct {
	MaxEntries, WatchItemKB        func() int
	MaxBytes                       func() int64
	BoundPersonalHistory           func(context.Context, string)
	PersonalSiteMaxBytes           func() int64
	NewListMetric, LargeItemMetric string
	AdminAPIKey                    string
	KeyIdleExpiry                  time.Duration
	ResolveNamedSite               func(context.Context, string) (string, error)
	ResolveWriteSite               func(context.Context, string) (string, error)
	CollectionOwnerDomain          func(context.Context, string) (string, bool, error)
	CollectionBrowserSession       func(context.Context, string) CollectionBrowserSession
	VisitorSessionIdle             time.Duration
	CollectionJSONStorable         func([]byte) bool
	CollectionOrigin               func(string) (bool, map[string][]string)
	CollectionReadLimit            func(string) (bool, map[string][]string)
	CollectionAppendLimit          func() (bool, map[string][]string)
	CollectionOwnerKeyWrite        func(string) bool
	CollectionDomainElsewhere      func(string) (string, bool)
	CollectionSharedDomain         func(context.Context, string) (string, bool)
	CollectionKeyActor             func(string) db.Actor
	CollectionInternalSession      func(string) (db.VisitorSession, bool)
	CollectionResolveKey           func(context.Context, string) (db.User, bool, error)
	CollectionSessionValid         func(db.VisitorSession, string) bool
	CollectionLogAnon              func(string, string, string, string)
	CollectionOwnerBrowser         func(string) bool
	CollectionStrictSession        func(string) CollectionBrowserSession
	CollectionBoardLimit           func(string) bool
	BoardMax                       func() int
	CollectionPrivateHome          func(string) (CollectionPrivateHome, bool, error)
	CollectionPrivateWrongHost     func() bool
	PersonalPeopleMax              func() int
	PersonalNext                   func(json.RawMessage) (json.RawMessage, error)
	PersonalPreparePatch           func([]byte) (func(json.RawMessage) (json.RawMessage, error), error)
}

// New constructs an adapter for the given database and legacy cutoff.
func New(database *sql.DB, cutoff time.Time, deps Deps) *Adapter {
	return &Adapter{database: database, cutoff: cutoff, deps: deps}
}

// CutoffApplies reports whether a classified request is subject to the cutoff.
func (a *Adapter) CutoffApplies(legacyRoutes, options, match, skip bool) bool {
	return !a.cutoff.IsZero() && legacyRoutes && !options && match && !skip
}

// CutoffCandidate identifies a site a request could act on.
type CutoffCandidate struct {
	ID   string
	Name string
}

// ClosedFor reports a closure only if every resolved site is at or after the
// cutoff. Failed lookups are ignored, and any older site exempts the request.
func (a *Adapter) ClosedFor(ctx context.Context, candidates []CutoffCandidate) (string, bool) {
	closedName, found := "", false
	seen := map[string]bool{}
	for _, c := range candidates {
		if c.ID == "" || seen[c.ID] {
			continue
		}
		seen[c.ID] = true
		at, name, err := db.SiteCreatedAt(ctx, a.database, c.ID)
		if err != nil {
			continue
		}
		if at.Before(a.cutoff) {
			return "", false
		}
		if !found {
			closedName, found = name, true
			if closedName == "" {
				closedName = c.Name
			}
		}
	}
	return closedName, found
}
