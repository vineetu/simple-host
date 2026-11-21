package teams

import (
	"context"
	"errors"

	model "github.com/vsriram/simple-host/products/hack/model"
	eventrepo "github.com/vsriram/simple-host/products/hack/repository/hackevents"
	teamrepo "github.com/vsriram/simple-host/products/hack/repository/hackteams"
	"github.com/vsriram/simple-host/products/hack/rules/hackteams"
)

// LeaveTeam takes a participant off their team; they stop editing its site.
// A team past its deadline keeps its members.
func (s *Service) LeaveTeam(ctx context.Context, ev model.Event, userID string) error {
	if err := participantStage(ev.Stage); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	err = teamrepo.LeaveTeam(ctx, tx, ev.ID, userID, teamrepo.EnsureTeamAdmin)
	if errors.Is(err, ErrTeamFrozen) {
		return &Error{Code: "submissions_closed", Message: "your team's deadline has passed, so the team can't change; ask the organiser"}
	}
	if err != nil {
		return err
	}
	return syncAndCommit(ctx, tx, ev.ID)
}

// DeleteTeam deletes the team rawSlug names and queues member notices; its
// members stop editing its site.
func (s *Service) DeleteTeam(ctx context.Context, ev model.Event, rawSlug string) error {
	if err := organiserStage(ev.Stage, msgArchived); err != nil {
		return err
	}
	team, err := teamBySlug(ctx, s.db, ev.ID, rawSlug)
	if err != nil {
		return err
	}
	// The handler sends the team site to Recently deleted after commit.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return internalError(err)
	}
	defer tx.Rollback()
	notice := team.Name + " was removed by the organiser."
	if _, err = teamrepo.SetTeamRemovalNotices(ctx, tx, team.ID, notice); err != nil {
		return internalError(err)
	}
	if err = eventrepo.NotifyTeamMembers(ctx, tx, ev.ID, team.ID, eventrepo.NoticeTeamRemoved, notice, ""); err != nil {
		return internalError(err)
	}
	if err = teamrepo.DeleteEventTeam(ctx, tx, team.ID); err != nil {
		return internalError(err)
	}
	return syncAndCommit(ctx, tx, ev.ID)
}

// JoinTeam joins the team with code under the event lock and checks its
// deadline. An empty code is team_not_found.
func (s *Service) JoinTeam(ctx context.Context, ev model.Event, userID, code string) (model.EventTeam, error) {
	if err := participantStage(ev.Stage); err != nil {
		return model.EventTeam{}, err
	}
	if code == "" {
		return model.EventTeam{}, ErrTeamNotFound
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.EventTeam{}, internalError(err)
	}
	defer tx.Rollback()
	// Recheck the stage under the event lock, before JoinTeamByCode locks
	// member then team.
	stage, err := LockEventStage(ctx, tx, ev.ID)
	if err != nil {
		return model.EventTeam{}, internalError(err)
	}
	if !hackteams.ParticipantTeamStages(stage) {
		return model.EventTeam{}, errTeamsLocked()
	}
	team, err := teamrepo.JoinTeamByCode(ctx, tx, ev.ID, userID, code, ev.TeamSizeMax, teamrepo.EnsureTeamAdmin)
	if err != nil {
		return team, err
	}
	// A team past its deadline takes nobody new: they could never publish or leave.
	st, err := teamrepo.TeamWriteStateFor(ctx, tx, team.ID, true)
	if err != nil {
		return team, internalError(err)
	}
	if st.Frozen() {
		return team, errDeadlinePassed()
	}
	return team, syncAndCommit(ctx, tx, ev.ID)
}
