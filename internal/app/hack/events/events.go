package events

import (
	"context"
	"database/sql"
	"time"

	eventrepo "github.com/vsriram/simple-host/internal/repository/hackevents"
	"github.com/vsriram/simple-host/internal/rules/hackevents"
)

// Service owns Hack event and organiser workflows.
type Service struct {
	db          *sql.DB
	resolveKey  KeyResolver
	checkHandle HandleChecker
}

// New builds a Service over database.
func New(database *sql.DB) *Service {
	return &Service{db: database}
}

// Row is one event as the directory shows it: the raw fields hackhttp needs
// to build its JSON row. The URL, icon URL, color and apply_url fields stay
// with the handler, which computes them from its own config (EventURL,
// iconURLSlug, hackEventColor, publicBaseURL); ApplicationsOpen is the one
// field this service computes, with the shared rule.
type Row struct {
	Slug, Title, Tagline, Stage, TimeZone string
	AccentColor                           string
	StartsAt, EndsAt                      sql.NullTime
	ApplicationsOpen                      bool
}

// Directory is GET /v1/hack/directory's three groups, in the grouping
// hackhttp's getDirectory has always used.
type Directory struct {
	Now, Upcoming, Past []Row
}

// Directory lists every directory-listed, non-draft, non-taken-down event
// (eventrepo.ListDirectoryEvents), grouped into Now/Upcoming/Past by
// rules/hackevents.DirectoryGroup, with each row's ApplicationsOpen already
// decided by rules/hackevents.ApplicationsOpenNow. Was hackhttp's
// getDirectory (hack_voting.go); clock is read once, after the query.
func (s *Service) Directory(ctx context.Context, clock func() time.Time) (Directory, error) {
	evs, err := eventrepo.ListDirectoryEvents(ctx, s.db)
	if err != nil {
		return Directory{}, err
	}
	// Read the clock after the query, as the handler always did, so an event
	// that starts or ends while the query runs is grouped the same way.
	now := clock()
	var out Directory
	for _, ev := range evs {
		row := Row{
			Slug: ev.Slug, Title: ev.Title, Tagline: ev.Tagline, Stage: ev.Stage,
			TimeZone: ev.TimeZone, AccentColor: ev.AccentColor,
			StartsAt: ev.StartsAt, EndsAt: ev.EndsAt,
			ApplicationsOpen: hackevents.ApplicationsOpenNow(ev.ApplicationsOpen, true, ev.Stage, ev.TakenDownAt.Valid),
		}
		switch hackevents.DirectoryGroup(ev.Stage, ev.EndsAt, ev.TimeZone, ev.StartsAt, now) {
		case "past":
			out.Past = append(out.Past, row)
		case "upcoming":
			out.Upcoming = append(out.Upcoming, row)
		default:
			out.Now = append(out.Now, row)
		}
	}
	return out, nil
}
