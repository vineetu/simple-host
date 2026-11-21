package teams

import (
	"context"
	"database/sql"

	eventapp "github.com/vsriram/simple-host/products/hack/app/events"
	model "github.com/vsriram/simple-host/products/hack/model"
	eventrepo "github.com/vsriram/simple-host/products/hack/repository/hackevents"
	"github.com/vsriram/simple-host/products/hack/repository/hackmembers"
	teamrepo "github.com/vsriram/simple-host/products/hack/repository/hackteams"
	"github.com/vsriram/simple-host/products/hack/rules/hackevents"
)

// FillTeams plans, or with apply makes, the organiser's team placements
// under the same locks. Fill teams does not require team finding.
func (s *Service) FillTeams(ctx context.Context, eventID, userID string, requestedSize *int, apply bool) ([]model.FillPlacement, []model.FillSkippedTeam, int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, 0, internalError(err)
	}
	defer tx.Rollback()
	ev, size, err := lockForFill(ctx, tx, eventID, requestedSize)
	if err != nil {
		return nil, nil, 0, err
	}
	plan, skipped, err := teamrepo.PlanFillTeams(ctx, tx, ev, size)
	if err != nil {
		return nil, nil, 0, internalError(err)
	}
	if apply {
		if err = applyFill(ctx, tx, ev, userID, plan); err != nil {
			return nil, nil, 0, err
		}
	}
	return plan, skipped, size, commit(tx)
}

// lockForFill locks the event and rechecks its stage, settles the team
// size, then locks every prospective member before the teams, as joining
// does.
func lockForFill(ctx context.Context, tx *sql.Tx, eventID string, requestedSize *int) (model.Event, int, error) {
	ev, err := eventrepo.GetEventForUpdate(ctx, tx, eventID)
	if err != nil {
		return ev, 0, internalError(err)
	}
	if err = participantStage(ev.Stage); err != nil {
		return ev, 0, err
	}
	size, err := fillSize(ev.TeamSizeMax, requestedSize)
	if err != nil {
		return ev, 0, err
	}
	if err = hackmembers.LockEventMembers(ctx, tx, ev.ID); err != nil {
		return ev, 0, internalError(err)
	}
	if err = teamrepo.LockEventTeams(ctx, tx, ev.ID); err != nil {
		return ev, 0, internalError(err)
	}
	return ev, size, nil
}

// fillSize is the requested team size, by default min(4, team_size_max)
// to avoid a 20-person team of strangers; it must fit the event's limit.
func fillSize(max int, requested *int) (int, error) {
	size := min(4, max)
	if requested != nil {
		size = *requested
	}
	if size < 1 || size > max {
		return 0, &Error{Code: "invalid_team_size", Message: "size must be between 1 and the event's team size"}
	}
	return size, nil
}

// applyFill creates the plan's new teams, places its members and syncs
// site access.
func applyFill(ctx context.Context, tx *sql.Tx, ev model.Event, userID string, plan []model.FillPlacement) error {
	for _, p := range plan {
		if p.New {
			if err := insertFillTeam(ctx, tx, ev, userID, p); err != nil {
				return internalError(err)
			}
		}
		for _, m := range p.Members {
			// MoveParticipantToTeam ensures the first person placed is an admin.
			if _, err := teamrepo.MoveParticipantToTeam(ctx, tx, ev.ID, m.UserID, p.Team, ev.TeamSizeMax); err != nil {
				return err
			}
		}
	}
	if err := eventapp.SyncAccess(ctx, tx, ev.ID); err != nil {
		return internalError(err)
	}
	return nil
}

// insertFillTeam creates a planned team under a fresh code and records its site name.
func insertFillTeam(ctx context.Context, tx *sql.Tx, ev model.Event, userID string, p model.FillPlacement) error {
	code, err := hackevents.RandomCode(teamCodeLength)
	if err != nil {
		return err
	}
	if _, err = teamrepo.InsertEventTeam(ctx, tx, ev.ID, p.Team, p.Name, code, userID); err != nil {
		return err
	}
	return eventrepo.RecordUsedNames(ctx, tx, ev.Slug, p.Team)
}
