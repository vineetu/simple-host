package teams

// TODO(4.2): move inline SQL to repository/hackteams.
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

func teamNameTaken(ctx context.Context, q db.Querier, eventID, name string) (bool, error) {
	var taken bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM event_teams WHERE event_id = $1 AND lower(name) = lower($2))`, eventID, name).Scan(&taken)
	return taken, err
}
func trySavepoint(ctx context.Context, q db.Querier, fn func() error) error {
	if _, err := q.ExecContext(ctx, "SAVEPOINT hack_try"); err != nil {
		return err
	}
	if err := fn(); err != nil {
		if _, rerr := q.ExecContext(ctx, "ROLLBACK TO SAVEPOINT hack_try"); rerr != nil {
			return rerr
		}
		return err
	}
	_, err := q.ExecContext(ctx, "RELEASE SAVEPOINT hack_try")
	return err
}

// CreateTeam creates a team and joins its creator atomically.
func (s *Service) CreateTeam(ctx context.Context, event db.Event, userID, name, note string, reserved func(string) bool) (db.EventTeam, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return db.EventTeam{}, internalError(err)
	}
	defer tx.Rollback()
	var closed bool
	if err = tx.QueryRowContext(ctx, `
		SELECT COALESCE(submission_deadline <= clock_timestamp(), false) FROM events WHERE id = $1 FOR SHARE`, event.ID).Scan(&closed); err != nil {
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
	base := hackteams.DeriveTeamSlug(name)
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
		used, err := db.TeamNameEverUsed(ctx, tx, event.Slug, event.AccountID, slug)
		if err != nil {
			return team, internalError(err)
		}
		if used {
			continue
		}
		var last error
		for attempt := 0; attempt < 8; attempt++ {
			code, err := hackevents.RandomCode(8)
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
			if _, err = tx.ExecContext(ctx, `UPDATE event_teams SET looking_for=$2 WHERE id=$1`, team.ID, note); err != nil {
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
