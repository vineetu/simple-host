package teams

import (
	"context"

	eventapp "github.com/vsriram/simple-host/products/hack/app/events"
	model "github.com/vsriram/simple-host/products/hack/model"
	eventrepo "github.com/vsriram/simple-host/products/hack/repository/hackevents"
	teamrepo "github.com/vsriram/simple-host/products/hack/repository/hackteams"
	"github.com/vsriram/simple-host/products/hack/rules/hackteams"
)

// LeaveTeam takes a participant off their team; they stop editing its site.
func (s *Service) LeaveTeam(ctx context.Context, eventID, userID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = teamrepo.LeaveTeam(ctx, tx, eventID, userID, teamrepo.EnsureTeamAdmin); err != nil {
		return err
	}
	if err = eventapp.SyncAccess(ctx, tx, eventID); err != nil {
		return &Error{Code: "internal", Message: err.Error()}
	}
	if err = tx.Commit(); err != nil {
		return &Error{Code: "internal", Message: err.Error()}
	}
	return nil
}

// DeleteTeam deletes a team and queues member notices; its members stop
// editing its site.
func (s *Service) DeleteTeam(ctx context.Context, eventID, slug string) error {
	team, err := teamrepo.GetEventTeamBySlug(ctx, s.db, eventID, slug)
	if err != nil {
		return err
	}
	// The handler sends the team site to Recently deleted after commit.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return &Error{Code: "internal", Message: err.Error()}
	}
	defer tx.Rollback()
	if _, err = teamrepo.SetTeamRemovalNotices(ctx, tx, team.ID, team.Name+" was removed by the organiser."); err != nil {
		return &Error{Code: "internal", Message: err.Error()}
	}
	if err = eventrepo.NotifyTeamMembers(ctx, tx, eventID, team.ID, eventrepo.NoticeTeamRemoved, team.Name+" was removed by the organiser.", ""); err != nil {
		return &Error{Code: "internal", Message: err.Error()}
	}
	if err = teamrepo.DeleteEventTeam(ctx, tx, team.ID); err != nil {
		return &Error{Code: "internal", Message: err.Error()}
	}
	if err = eventapp.SyncAccess(ctx, tx, eventID); err != nil {
		return &Error{Code: "internal", Message: err.Error()}
	}
	if err = tx.Commit(); err != nil {
		return &Error{Code: "internal", Message: err.Error()}
	}
	return nil
}

// JoinTeam joins a team under the event lock and checks its deadline.
func (s *Service) JoinTeam(ctx context.Context, eventID, userID, code string, max int) (model.EventTeam, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.EventTeam{}, internalError(err)
	}
	defer tx.Rollback()
	// The handler's pre-BEGIN stage check is a cheap early rejection; recheck
	// under the event lock, before JoinTeamByCode locks member then team.
	stage, err := LockEventStage(ctx, tx, eventID)
	if err != nil {
		return model.EventTeam{}, internalError(err)
	}
	if !hackteams.ParticipantTeamStages(stage) {
		return model.EventTeam{}, &Error{Code: "teams_locked", Message: "teams cannot be changed at this stage"}
	}
	team, err := teamrepo.JoinTeamByCode(ctx, tx, eventID, userID, code, max, teamrepo.EnsureTeamAdmin)
	if err != nil {
		return team, err
	}
	// A team past its deadline takes nobody new: they could never publish or leave.
	st, err := teamrepo.TeamWriteStateFor(ctx, tx, team.ID, true)
	if err != nil {
		return team, internalError(err)
	}
	if st.Frozen() {
		return team, &Error{Code: "submissions_closed", Message: "the submission deadline has passed"}
	}
	if err = eventapp.SyncAccess(ctx, tx, eventID); err != nil {
		return team, internalError(err)
	}
	if err = tx.Commit(); err != nil {
		return team, internalError(err)
	}
	return team, nil
}
