package teams

import (
	"context"
	"database/sql"

	"github.com/vsriram/simple-host/host/sqlkit"
	model "github.com/vsriram/simple-host/products/hack/model"
	eventrepo "github.com/vsriram/simple-host/products/hack/repository/hackevents"
	teamrepo "github.com/vsriram/simple-host/products/hack/repository/hackteams"
	"github.com/vsriram/simple-host/products/hack/rules/hackteams"
)

// errMemberNotFound refuses an organiser write naming someone not on the team.
func errMemberNotFound() error { return &Error{Code: "member_not_found", Message: "member not found"} }

func teamBySlug(ctx context.Context, q sqlkit.Querier, eventID, rawSlug string) (model.EventTeam, error) {
	team, err := teamrepo.GetEventTeamBySlug(ctx, q, eventID, normSlug(rawSlug))
	return team, notFoundAs(err, ErrTeamNotFound)
}

func (s *Service) OrganiserLock(ctx context.Context, ev model.Event, rawSlug, actor string, locked bool) (int, int, error) {
	if err := organiserStage(ev.Stage, msgEnded); err != nil {
		return 0, 0, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, internalError(err)
	}
	defer tx.Rollback()
	team, err := teamBySlug(ctx, tx, ev.ID, rawSlug)
	if err != nil {
		return 0, 0, err
	}
	requests, invites, err := teamrepo.SetTeamLock(ctx, tx, team.ID, actor, locked)
	if err != nil {
		return 0, 0, notFoundAs(err, errMemberNotFound())
	}
	return requests, invites, commit(tx)
}

func (s *Service) OrganiserAdmin(ctx context.Context, ev model.Event, rawSlug, userID string, admin bool) error {
	if err := organiserStage(ev.Stage, msgEnded); err != nil {
		return err
	}
	if !isID(userID) {
		return errEventNotFound()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return internalError(err)
	}
	defer tx.Rollback()
	team, err := teamBySlug(ctx, tx, ev.ID, rawSlug)
	if err != nil {
		return err
	}
	if err = teamrepo.SetTeamAdmin(ctx, tx, ev.ID, team.ID, userID, admin); err != nil {
		return notFoundAs(err, errMemberNotFound())
	}
	return commit(tx)
}

func (s *Service) OrganiserLink(ctx context.Context, ev model.Event, rawSlug string) (model.EventTeam, error) {
	if err := organiserStage(ev.Stage, msgEnded); err != nil {
		return model.EventTeam{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.EventTeam{}, internalError(err)
	}
	defer tx.Rollback()
	team, err := teamBySlug(ctx, tx, ev.ID, rawSlug)
	if err != nil {
		return team, err
	}
	return rotateAndRead(ctx, tx, team)
}

func rotateAndRead(ctx context.Context, tx *sql.Tx, team model.EventTeam) (model.EventTeam, error) {
	if err := rotateTeamCode(ctx, tx, team.ID); err != nil {
		return team, internalError(err)
	}
	fresh, err := teamrepo.GetEventTeamByID(ctx, tx, team.ID)
	if err != nil {
		return team, internalError(err)
	}
	return fresh, commit(tx)
}

func commit(tx *sql.Tx) error {
	if err := tx.Commit(); err != nil {
		return internalError(err)
	}
	return nil
}

// participantTeam locks the caller's team for an admin write: the stage
// is rechecked under the event lock and the caller must be its admin.
func participantTeam(ctx context.Context, tx *sql.Tx, eventID, userID string) (model.EventTeam, error) {
	team, admin, stage, err := LockCallerTeam(ctx, tx, eventID, userID)
	if err != nil {
		return team, err
	}
	if !hackteams.ParticipantTeamStages(stage) {
		return team, errTeamsLocked()
	}
	if !admin {
		return team, teamrepo.ErrHackTeamAdminOnly
	}
	return team, nil
}

func checkFrozen(ctx context.Context, tx *sql.Tx, teamID string) error {
	st, err := teamrepo.TeamWriteStateFor(ctx, tx, teamID, false)
	if err != nil {
		return internalError(err)
	}
	if st.Frozen() {
		return errDeadlinePassed()
	}
	return nil
}

// beginAdminWrite checks the stage, opens the transaction and locks the
// caller's team as its admin, refusing a frozen team. The caller rolls back.
func (s *Service) beginAdminWrite(ctx context.Context, ev model.Event, userID string) (*sql.Tx, model.EventTeam, error) {
	if err := participantStage(ev.Stage); err != nil {
		return nil, model.EventTeam{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, model.EventTeam{}, internalError(err)
	}
	team, err := participantTeam(ctx, tx, ev.ID, userID)
	if err == nil {
		err = checkFrozen(ctx, tx, team.ID)
	}
	if err != nil {
		tx.Rollback()
		return nil, team, err
	}
	return tx, team, nil
}

// PatchTeam changes the caller's team name or finding note; nil leaves a
// field as it is (with neither it only checks the caller is the admin).
func (s *Service) PatchTeam(ctx context.Context, ev model.Event, userID string, name, note *string) (model.EventTeam, error) {
	if err := participantStage(ev.Stage); err != nil {
		return model.EventTeam{}, err
	}
	name, err := optionalLine(name, "team_name", 1, 80)
	if err != nil {
		return model.EventTeam{}, err
	}
	if note, err = optionalLine(note, "looking_for", 0, 120); err != nil {
		return model.EventTeam{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.EventTeam{}, internalError(err)
	}
	defer tx.Rollback()
	team, err := participantTeam(ctx, tx, ev.ID, userID)
	if err != nil {
		return team, err
	}
	if name != nil || note != nil {
		if err = checkFrozen(ctx, tx, team.ID); err != nil {
			return team, err
		}
	}
	if err = patchTeam(ctx, tx, ev.ID, team, name, note); err != nil {
		return team, err
	}
	return team, commit(tx)
}

func patchTeam(ctx context.Context, tx *sql.Tx, eventID string, team model.EventTeam, name, note *string) error {
	if name != nil {
		err := eventrepo.RenameEventTeam(ctx, tx, eventID, team.Slug, *name)
		if isUnique(err) {
			return errTeamNameTaken()
		}
		if err != nil {
			return internalError(err)
		}
	}
	if note != nil {
		if _, err := teamrepo.SetTeamLookingFor(ctx, tx, team.ID, *note); err != nil {
			return internalError(err)
		}
	}
	return nil
}

func (s *Service) ParticipantLock(ctx context.Context, ev model.Event, userID string, locked bool) (int, int, error) {
	tx, team, err := s.beginAdminWrite(ctx, ev, userID)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()
	requests, invites, err := teamrepo.SetTeamLock(ctx, tx, team.ID, userID, locked)
	if err != nil {
		return 0, 0, err
	}
	return requests, invites, commit(tx)
}

// ParticipantAdmin sets a teammate's admin flag under sorted member locks.
// A target not on the caller's team is event_not_found, never confirming
// where else someone belongs.
func (s *Service) ParticipantAdmin(ctx context.Context, ev model.Event, userID, target string, admin bool) error {
	if err := participantStage(ev.Stage); err != nil {
		return err
	}
	if !isID(target) {
		return errEventNotFound()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return internalError(err)
	}
	defer tx.Rollback()
	teamID, callerAdmin, stage, err := LockCallerAndTargetTeam(ctx, tx, ev.ID, userID, target)
	switch {
	case err != nil:
		return err
	case !hackteams.ParticipantTeamStages(stage):
		return errTeamsLocked()
	case !callerAdmin:
		return teamrepo.ErrHackTeamAdminOnly
	}
	if err = checkFrozen(ctx, tx, teamID); err != nil {
		return err
	}
	if err = teamrepo.SetTeamAdmin(ctx, tx, ev.ID, teamID, target, admin); err != nil {
		return err
	}
	return commit(tx)
}

func (s *Service) ParticipantLink(ctx context.Context, ev model.Event, userID string) (model.EventTeam, error) {
	tx, team, err := s.beginAdminWrite(ctx, ev, userID)
	if err != nil {
		return team, err
	}
	defer tx.Rollback()
	return rotateAndRead(ctx, tx, team)
}
