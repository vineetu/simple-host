package teams

import (
	"context"
	"database/sql"
	"errors"

	eventapp "github.com/vsriram/simple-host/products/hack/app/events"

	"github.com/vsriram/simple-host/host/sqlkit"
	model "github.com/vsriram/simple-host/products/hack/model"
	eventrepo "github.com/vsriram/simple-host/products/hack/repository/hackevents"
	teamrepo "github.com/vsriram/simple-host/products/hack/repository/hackteams"
	"github.com/vsriram/simple-host/products/hack/rules/hackevents"
	"github.com/vsriram/simple-host/products/hack/rules/hackteams"
)

func isUnique(err error) bool { return hackevents.UniqueError(err) }

// rotateTeamCode stops removed members rejoining with the old code. An empty,
// deleted team has nothing to rotate.
func rotateTeamCode(ctx context.Context, q sqlkit.Querier, teamID string) error {
	for attempt := 0; attempt < 8; attempt++ {
		code, err := hackevents.RandomCode(teamCodeLength)
		if err != nil {
			return err
		}
		err = trySavepoint(ctx, q, func() error {
			_, err := teamrepo.SetTeamCode(ctx, q, teamID, code)
			return err
		})
		if err == nil || !isUnique(err) {
			return err
		}
	}
	return errors.New("could not find a free team code")
}

// RemovePerson removes someone from the event (never an organiser) and
// rotates their remaining team's code, atomically.
func (s *Service) RemovePerson(ctx context.Context, ev model.Event, userID string) error {
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
	// Lock the event first, matching self-leave. Removing a judge locks the
	// member then inserts a former-judge row, whose foreign key takes an event
	// key-share lock. Self-leave can hold the event update lock while waiting
	// for that member, causing a deadlock unless this locks the event first.
	if _, err = eventrepo.GetEventForShare(ctx, tx, ev.ID); err != nil {
		return internalError(err)
	}
	oldTeam := memberTeamID(ctx, tx, ev.ID, userID)
	err = teamrepo.RemovePersonFromEvent(ctx, tx, ev.ID, userID)
	if errors.Is(err, ErrCannotRemoveOrganiser) {
		return &Error{Code: "cannot_remove_organiser", Message: "an organiser cannot be removed from the event"}
	}
	if err != nil {
		return err
	}
	if err = rotateIfKept(ctx, tx, oldTeam); err != nil {
		return internalError(err)
	}
	return syncAndCommit(ctx, tx, ev.ID)
}

// rotateIfKept rotates the code of a team someone left, unless leaving
// deleted it.
func rotateIfKept(ctx context.Context, tx *sql.Tx, teamID string) error {
	if teamID == "" {
		return nil
	}
	var still bool
	if err := teamrepo.TeamRowExists(ctx, tx, teamID).Scan(&still); err != nil || !still {
		return err
	}
	return rotateTeamCode(ctx, tx, teamID)
}

func syncAndCommit(ctx context.Context, tx *sql.Tx, eventID string) error {
	if err := eventapp.SyncAccess(ctx, tx, eventID); err != nil {
		return internalError(err)
	}
	return commit(tx)
}

// MoveMember puts a participant on the team rawSlug names and rotates
// their old team's code, atomically. Someone who is no approved
// participant is event_not_found.
func (s *Service) MoveMember(ctx context.Context, ev model.Event, userID, rawSlug string) (model.EventTeam, error) {
	if err := organiserStage(ev.Stage, msgArchived); err != nil {
		return model.EventTeam{}, err
	}
	if !isID(userID) {
		return model.EventTeam{}, errEventNotFound()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.EventTeam{}, internalError(err)
	}
	defer tx.Rollback()
	oldTeam := memberTeamID(ctx, tx, ev.ID, userID)
	team, err := teamrepo.MoveParticipantToTeam(ctx, tx, ev.ID, userID, normSlug(rawSlug), ev.TeamSizeMax, teamrepo.EnsureTeamAdmin)
	if errors.Is(err, ErrNotParticipant) {
		return team, errEventNotFound()
	}
	if err != nil {
		return team, err
	}
	if oldTeam != "" && oldTeam != team.ID {
		if err = rotateTeamCode(ctx, tx, oldTeam); err != nil {
			return team, internalError(err)
		}
	}
	return team, syncAndCommit(ctx, tx, ev.ID)
}

// RemoveTeamMember takes userID off a team: an organiser off any team the
// path names, a participant only off their own, as its admin, before its
// deadline. It sends the notice and rotates the team code.
func (s *Service) RemoveTeamMember(ctx context.Context, ev model.Event, caller model.EventMember, actorID, userID, rawSlug string) error {
	if err := organiserStage(ev.Stage, msgArchived); err != nil {
		return err
	}
	if !isID(userID) {
		return errEventNotFound()
	}
	participant := caller.Role == "participant"
	slug, teamName, err := s.removalTeam(ctx, ev, caller, actorID, userID, normSlug(rawSlug))
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return internalError(err)
	}
	defer tx.Rollback()
	if participant {
		if err = checkRemover(ctx, tx, ev.ID, actorID, userID, slug); err != nil {
			return err
		}
	}
	if err = takeOffTeam(ctx, tx, ev.ID, userID, slug, teamName, participant); err != nil {
		return err
	}
	return syncAndCommit(ctx, tx, ev.ID)
}

// removalTeam is the team slug a removal acts on, with its name for the
// notice: as given for an organiser; for a participant their own team,
// which a slug in the path must match.
func (s *Service) removalTeam(ctx context.Context, ev model.Event, caller model.EventMember, actorID, userID, slug string) (string, string, error) {
	if caller.Role != "participant" {
		return slug, "", nil
	}
	if !hackteams.ParticipantTeamStages(ev.Stage) || !caller.TeamID.Valid || userID == actorID {
		return "", "", ErrFindingForbidden
	}
	team, err := teamrepo.GetEventTeamByID(ctx, s.db, caller.TeamID.String)
	if err != nil {
		return "", "", internalError(err)
	}
	if slug != "" && slug != team.Slug {
		return "", "", errEventNotFound()
	}
	return team.Slug, team.Name, nil
}

// checkRemover rechecks a participant remover's authority under locks: both member rows sorted by user id, then the team row.
func checkRemover(ctx context.Context, tx *sql.Tx, eventID, actorID, userID, slug string) error {
	if _, err := teamrepo.LockRemovalMembers(ctx, tx, eventID, actorID, userID); err != nil {
		return internalError(err)
	}
	var teamID, gotSlug string
	var admin bool
	if err := teamrepo.ReadRemovalActor(ctx, tx, eventID, actorID).Scan(&teamID, &admin); err != nil {
		return sql.ErrNoRows
	}
	if !admin {
		return teamrepo.ErrHackTeamAdminOnly
	}
	if err := teamrepo.LockTeamSlug(ctx, tx, teamID).Scan(&gotSlug); err != nil || gotSlug != slug {
		return sql.ErrNoRows
	}
	st, err := teamrepo.TeamWriteStateFor(ctx, tx, teamID, true)
	if err != nil {
		return internalError(err)
	}
	if st.Frozen() {
		return teamrepo.ErrHackTeamFrozen
	}
	return nil
}

// takeOffTeam removes userID from the team, queues the removal notice and
// its email, and rotates the team's code.
func takeOffTeam(ctx context.Context, tx *sql.Tx, eventID, userID, slug, teamName string, participant bool) error {
	oldTeam := memberTeamID(ctx, tx, eventID, userID)
	if err := teamrepo.RemoveParticipantFromTeam(ctx, tx, eventID, slug, userID, teamrepo.EnsureTeamAdmin); err != nil {
		return err
	}
	if participant {
		if _, err := teamrepo.SetMemberRemovalNotice(ctx, tx, eventID, userID, "A teammate took you off "+teamName+"."); err != nil {
			return internalError(err)
		}
	}
	// Email uses the same words as the removal notice.
	var notice string
	if err := teamrepo.ReadMemberRemovalNotice(ctx, tx, eventID, userID).Scan(&notice); err != nil {
		return internalError(err)
	}
	if notice != "" {
		if err := eventrepo.NotifyUsers(ctx, tx, eventID, eventrepo.NoticeTeamRemoved, oldTeam, "", notice, userID); err != nil {
			return internalError(err)
		}
	}
	if oldTeam == "" {
		return nil
	}
	if err := rotateTeamCode(ctx, tx, oldTeam); err != nil {
		return internalError(err)
	}
	return nil
}

func (s *Service) teamDeadlinePassed(ctx context.Context, teamID string) (bool, error) {
	var passed bool
	err := teamrepo.TeamDeadlinePassed(ctx, s.db, teamID).Scan(&passed)
	return passed, err
}

func teamNameTaken(ctx context.Context, q sqlkit.Querier, eventID, name string) (bool, error) {
	var taken bool
	err := teamrepo.TeamNameExists(ctx, q, eventID, name).Scan(&taken)
	return taken, err
}

// trySavepoint permits a retry after a unique violation, which otherwise aborts
// the whole transaction.
func trySavepoint(ctx context.Context, q sqlkit.Querier, fn func() error) error {
	if _, err := teamrepo.StartWorkflowSavepoint(ctx, q); err != nil {
		return err
	}
	if err := fn(); err != nil {
		if _, rerr := teamrepo.RollbackWorkflowSavepoint(ctx, q); rerr != nil {
			return rerr
		}
		return err
	}
	_, err := teamrepo.ReleaseWorkflowSavepoint(ctx, q)
	return err
}

// memberTeamID locks the old membership so a concurrent move waits and the
// team actually left is the one read.
func memberTeamID(ctx context.Context, q *sql.Tx, eventID, userID string) string {
	var team sql.NullString
	if err := teamrepo.LockMemberTeamID(ctx, q, eventID, userID).Scan(&team); err != nil || !team.Valid {
		return ""
	}
	return team.String
}
