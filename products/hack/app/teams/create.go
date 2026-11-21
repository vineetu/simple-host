package teams

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/vsriram/simple-host/host/rules/sites"
	eventapp "github.com/vsriram/simple-host/products/hack/app/events"
	model "github.com/vsriram/simple-host/products/hack/model"
	teamrepo "github.com/vsriram/simple-host/products/hack/repository/hackteams"
	"github.com/vsriram/simple-host/products/hack/rules/hackevents"
	"github.com/vsriram/simple-host/products/hack/rules/hackteams"
)

// Tries before giving up: slugs per team name, join codes per slug.
const (
	slugTries = 50
	codeTries = 8
)

// errCodesExhausted means every join code tried for one slug collided.
var errCodesExhausted = errors.New("team join codes exhausted")

// errTeamNameTaken refuses a team name another team in the event has.
func errTeamNameTaken() error {
	return &Error{Code: "team_name_taken", Message: "another team already has that name"}
}

// CreateTeam creates a team and joins its creator atomically; flatHosts caps the slug for <team>--<event> hosts.
func (s *Service) CreateTeam(ctx context.Context, event model.Event, userID, rawName, rawNote string, flatHosts bool, reserved func(string) bool) (model.EventTeam, error) {
	if err := participantStage(event.Stage); err != nil {
		return model.EventTeam{}, err
	}
	name, err := checkLine(eventapp.StripInvisible(rawName), "team_name", 1, 80)
	if err != nil {
		return model.EventTeam{}, err
	}
	note, err := checkLine(eventapp.StripInvisible(rawNote), "looking_for", 0, 120)
	if err != nil {
		return model.EventTeam{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.EventTeam{}, internalError(err)
	}
	defer tx.Rollback()
	if err := checkNewTeam(ctx, tx, event.ID, name); err != nil {
		return model.EventTeam{}, err
	}
	team, err := insertTeam(ctx, tx, event, userID, name, hackteams.MaxTeamSlugLen(event.Slug, flatHosts), reserved)
	if err != nil {
		return model.EventTeam{}, err
	}
	if err := finishNewTeam(ctx, tx, event, team, note); err != nil {
		return model.EventTeam{}, internalError(err)
	}
	return team, nil
}

// checkNewTeam refuses a team after the event deadline (it would be frozen
// from birth) or under a name already taken.
func checkNewTeam(ctx context.Context, tx *sql.Tx, eventID, name string) error {
	var closed bool
	if err := teamrepo.LockEventDeadlinePassed(ctx, tx, eventID).Scan(&closed); err != nil {
		return internalError(err)
	}
	if closed {
		return &Error{Code: "submissions_closed", Message: "the submission deadline has passed"}
	}
	taken, err := teamNameTaken(ctx, tx, eventID, name)
	if err != nil {
		return internalError(err)
	}
	if taken {
		return errTeamNameTaken()
	}
	return nil
}

// insertTeam creates the team under the first free slug derived from its
// name, joining the creator to it.
func insertTeam(ctx context.Context, tx *sql.Tx, event model.Event, userID, name string, maxLen int, reserved func(string) bool) (model.EventTeam, error) {
	base := hackteams.DeriveTeamSlug(name, maxLen)
	for n := 0; n < slugTries; n++ {
		slug := hackteams.TeamSlugCandidate(base, n, maxLen)
		free, err := teamSlugFree(ctx, tx, event, slug, reserved)
		if err != nil {
			return model.EventTeam{}, internalError(err)
		}
		if !free {
			continue
		}
		team, err := createWithCode(ctx, tx, event.ID, userID, slug, name)
		if !errors.Is(err, errCodesExhausted) {
			return team, err
		}
	}
	return model.EventTeam{}, internalError(sql.ErrNoRows)
}

// teamSlugFree reports whether slug is a valid site name no team or site in
// the event has ever used: a new team must not inherit earlier work, data
// or visitors.
func teamSlugFree(ctx context.Context, tx *sql.Tx, event model.Event, slug string, reserved func(string) bool) (bool, error) {
	if reserved(slug) || sites.ValidateShape(slug) != nil || sites.ValidateReserved(slug) != nil {
		return false, nil
	}
	taken, err := teamrepo.TeamSlugExists(ctx, tx, event.ID, slug)
	if err != nil || taken {
		return false, err
	}
	used, err := teamrepo.TeamNameEverUsed(ctx, tx, event.Slug, event.AccountID, slug, siteEverNamed)
	return err == nil && !used, err
}

// createWithCode inserts the team under slug with a fresh random join code,
// retrying a code collision; errCodesExhausted means every try collided.
func createWithCode(ctx context.Context, tx *sql.Tx, eventID, userID, slug, name string) (model.EventTeam, error) {
	for range codeTries {
		code, err := hackevents.RandomCode(teamCodeLength)
		if err != nil {
			return model.EventTeam{}, internalError(err)
		}
		var team model.EventTeam
		err = trySavepoint(ctx, tx, func() (err error) {
			team, err = teamrepo.CreateTeamAndJoin(ctx, tx, eventID, userID, slug, name, code)
			return err
		})
		switch {
		case err == nil:
			return team, nil
		case errors.Is(err, teamrepo.ErrHackAlreadyInTeam) || errors.Is(err, teamrepo.ErrHackNotParticipant):
			return model.EventTeam{}, err
		case !isUnique(err):
			return model.EventTeam{}, internalError(err)
		case strings.Contains(err.Error(), "event_teams_name_idx"):
			return model.EventTeam{}, errTeamNameTaken()
		}
	}
	return model.EventTeam{}, errCodesExhausted
}

// finishNewTeam records the new team's note and site name, syncs access
// and commits. The deprecated open flag is no longer written.
func finishNewTeam(ctx context.Context, tx *sql.Tx, event model.Event, team model.EventTeam, note string) error {
	if _, err := teamrepo.SetCreatedTeamLookingFor(ctx, tx, team.ID, note); err != nil {
		return err
	}
	if err := teamrepo.RecordUsedTeamName(ctx, tx, event.Slug, team.Slug); err != nil {
		return err
	}
	if err := eventapp.SyncAccess(ctx, tx, event.ID); err != nil {
		return err
	}
	return tx.Commit()
}
