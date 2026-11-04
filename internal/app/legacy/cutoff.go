package legacy

import (
	"context"
	"database/sql"
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
	MaxEntries, WatchItemKB        int
	MaxBytes                       int64
	NewListMetric, LargeItemMetric string
	AdminAPIKey                    string
	KeyIdleExpiry                  time.Duration
	ResolveNamedSite               func(context.Context, string) (string, error)
	ResolveWriteSite               func(context.Context, string) (string, error)
	AuthorizeCollectionOrigin      func(string) bool
	CollectionReadAllowed          func(string, bool) bool
	CollectionPrivateRead          func(string, bool) error
	CollectionAuthorView           func(string, bool) bool
	CollectionAppendBoard          func(string, db.DataSettings)
	CollectionAppendPrivate        func(string, db.DataSettings)
	CollectionAppendActor          func(string, db.DataSettings) (db.Actor, bool)
	CollectionAppendPrepare        func(string, db.DataSettings, db.Actor) (AppendInput, bool)
	CollectionAppendRespond        func(db.DataSettings, AppendResult, error)
	CollectionJSONStorable         func([]byte) bool
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
