package teams

import (
	"context"
	"database/sql"
	"errors"
	"github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/rules/hackevents"
	"github.com/vsriram/simple-host/internal/rules/hackteams"
	"github.com/vsriram/simple-host/internal/rules/sites"
	"strings"
)

// CreateTeam creates a team and joins its creator atomically.
func (s *Service) CreateTeam(ctx context.Context, event db.Event, userID, name, note string, reserved func(string) bool) (db.EventTeam, error) {
	base := hackteams.DeriveTeamSlug(name)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return db.EventTeam{}, internalError(err)
	}
	defer tx.Rollback()
	// After the event deadline a new team would be frozen from birth.
	var closed bool
	if err = tx.QueryRowContext(ctx, createSQL2, event.ID).Scan(&closed); err != nil {
		return db.EventTeam{}, internalError(err)
	}
	if closed {
		return db.EventTeam{}, &Error{Code: "submissions_closed"}
	}
	taken, err := teamNameTaken(ctx, tx, event.ID, name)
	if err != nil {
		return db.EventTeam{}, internalError(err)
	}
	if taken {
		return db.EventTeam{}, &Error{Code: "team_name_taken"}
	}
	var team db.EventTeam
	for n := 0; n < 50; n++ {
		slug := hackteams.TeamSlugCandidate(base, n)
		taken, err := db.TeamSlugExists(ctx, tx, event.ID, slug)
		if err != nil {
			return team, internalError(err)
		}
		if taken || reserved(slug) || sites.ValidateShape(slug) != nil || sites.ValidateReserved(slug) != nil {
			continue
		}
		// Never reuse a site name: the new team must not inherit earlier work, data or visitors.
		used, err := db.TeamNameEverUsed(ctx, tx, event.Slug, event.AccountID, slug)
		if err != nil {
			return team, internalError(err)
		}
		if used {
			continue
		}
		var last error
		for attempt := 0; attempt < 8; attempt++ {
			code, err := hackevents.RandomCode(teamCodeLength)
			if err != nil {
				return team, internalError(err)
			}
			last = trySavepoint(ctx, tx, func() error {
				var terr error
				team, terr = db.CreateTeamAndJoin(ctx, tx, event.ID, userID, slug, name, code)
				return terr
			})
			if last == nil {
				break
			}
			if errors.Is(last, db.ErrHackAlreadyInTeam) || errors.Is(last, db.ErrHackNotParticipant) {
				return team, last
			}
			if isUnique(last) && strings.Contains(last.Error(), "event_teams_name_idx") {
				return team, &Error{Code: "team_name_taken"}
			}
			if isUnique(last) && attempt < 7 {
				continue
			}
			if isUnique(last) {
				break
			}
			return team, internalError(last)
		}
		if last == nil {
			// Deprecated 2026-10-31: open is accepted for one release but ignored;
			// event_teams.open is no longer written.
			if _, err = tx.ExecContext(ctx, createSQL3, team.ID, note); err != nil {
				return team, internalError(err)
			}
			if err = db.RecordUsedTeamName(ctx, tx, event.Slug, slug); err != nil {
				return team, internalError(err)
			}
			if err = tx.Commit(); err != nil {
				return team, internalError(err)
			}
			return team, nil
		}
	}
	return team, internalError(sql.ErrNoRows)
}
