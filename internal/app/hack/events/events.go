// Package events owns Hack's events workflow, moved out of internal/hackhttp
// one endpoint at a time (docs/architecture/target.md §2, §10 row 3.2). This
// slice (cleanup 3.2 slice 4) moves the first one: the public
// GET /v1/hack/directory listing. More of the events/members/teams workflow
// lands here in cleanup step 4.2.
//
// Its SQL stays in internal/db for now (db.ListDirectoryEvents): this
// package may import internal/db as a TRANSITIONAL edge until
// internal/repository/hackevents exists in step 4.x. That edge is not a
// precedent for other app packages — see 3.2-common.md and target.md §2's
// final import table, which has app/hack/events reaching a repository
// instead.
package events

import (
	"context"
	"database/sql"
	"time"

	"github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/rules/hackevents"
)

// Service owns the Hack events workflows this slice moves: today, just the
// public directory listing (Directory below).
type Service struct {
	db db.Querier
}

// New builds a Service over q (typically *sql.DB; any db.Querier, including
// a *sql.Tx, works for a future caller that already holds one open).
func New(q db.Querier) *Service {
	return &Service{db: q}
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
// (db.ListDirectoryEvents), grouped into Now/Upcoming/Past by
// rules/hackevents.DirectoryGroup, with each row's ApplicationsOpen already
// decided by rules/hackevents.ApplicationsOpenNow. Was hackhttp's
// getDirectory (hack_voting.go); now is the caller's clock reading, passed
// in rather than read here.
func (s *Service) Directory(ctx context.Context, now time.Time) (Directory, error) {
	evs, err := db.ListDirectoryEvents(ctx, s.db)
	if err != nil {
		return Directory{}, err
	}
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
