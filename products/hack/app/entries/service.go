package entries

import (
	"context"
	"database/sql"

	model "github.com/vsriram/simple-host/products/hack/model"
)

type Service struct {
	db       *sql.DB
	siteLive SiteLiveFunc
}

// SiteLiveFunc reports whether a team's site is live (supplied by core.Links,
// which owns the site lookup).
type SiteLiveFunc func(ctx context.Context, ev model.Event, teamSlug string, siteTakenDown bool) bool

// New builds a Service. siteLive may be nil for callers that never settle.
func New(database *sql.DB, siteLive SiteLiveFunc) *Service {
	return &Service{db: database, siteLive: siteLive}
}
