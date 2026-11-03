package entries

import (
	"context"
	"database/sql"

	"github.com/vsriram/simple-host/internal/db"
)

// Service owns Hack entry and judging workflows.
type Service struct {
	db       *sql.DB
	siteLive SiteLiveFunc
}

// SiteLiveFunc reports whether a team's site is live (supplied by hackhttp,
// which owns the site lookup).
type SiteLiveFunc func(ctx context.Context, ev db.Event, teamSlug string, siteTakenDown bool) bool

// New builds a Service. siteLive may be nil for callers that never settle.
func New(database *sql.DB, siteLive SiteLiveFunc) *Service {
	return &Service{db: database, siteLive: siteLive}
}
