package teams

import (
	"context"
	"database/sql"
	eventrepo "github.com/vsriram/simple-host/products/hack/repository/hackevents"
	"sort"

	model "github.com/vsriram/simple-host/products/hack/model"
	"github.com/vsriram/simple-host/products/hack/repository/hackmembers"
	teamrepo "github.com/vsriram/simple-host/products/hack/repository/hackteams"
)

// LockEventStage locks the event row FOR SHARE (event, then members, then teams) and returns its current stage.
func LockEventStage(ctx context.Context, tx *sql.Tx, eventID string) (string, error) {
	ev, err := eventrepo.GetEventForShare(ctx, tx, eventID)
	return ev.Stage, err
}

// LockCallerTeam locks the event, the caller's member row and their team row; sql.ErrNoRows when on no team. stage is returned even on error.
func LockCallerTeam(ctx context.Context, tx *sql.Tx, eventID, userID string) (team model.EventTeam, admin bool, stage string, err error) {
	stage, err = LockEventStage(ctx, tx, eventID)
	if err != nil {
		return model.EventTeam{}, false, stage, err
	}
	m, err := approvedMember(ctx, tx, eventID, userID, hackmembers.ForUpdate)
	if err != nil {
		return model.EventTeam{}, false, stage, err
	}
	teamID, admin := m.TeamID, m.TeamAdmin
	if !teamID.Valid {
		return model.EventTeam{}, false, stage, sql.ErrNoRows
	}
	if err = teamrepo.LockTeamRow(ctx, tx, teamID.String); err != nil {
		return model.EventTeam{}, false, stage, err
	}
	team, err = teamrepo.GetEventTeamByID(ctx, tx, teamID.String)
	return team, admin, stage, err
}

// LockCallerAndTargetTeam locks the event, both member rows sorted by user id (avoiding a deadlock with the reverse request), then the caller's team row.
func LockCallerAndTargetTeam(ctx context.Context, tx *sql.Tx, eventID, callerID, targetID string) (teamID string, callerAdmin bool, stage string, err error) {
	stage, err = LockEventStage(ctx, tx, eventID)
	if err != nil {
		return "", false, stage, err
	}
	ids := []string{callerID, targetID}
	sort.Strings(ids)
	if err = hackmembers.LockMembers(ctx, tx, eventID, ids...); err != nil {
		return "", false, stage, err
	}
	m, err := approvedMember(ctx, tx, eventID, callerID, hackmembers.NoLock)
	if err != nil {
		return "", false, stage, err
	}
	team, callerAdmin := m.TeamID, m.TeamAdmin
	if !team.Valid {
		return "", false, stage, sql.ErrNoRows
	}
	if err = teamrepo.LockTeamRow(ctx, tx, team.String); err != nil {
		return "", false, stage, err
	}
	return team.String, callerAdmin, stage, nil
}

// approvedMember reads the person's membership, sql.ErrNoRows unless they are
// in good standing (an approved participant, or an organiser or judge).
func approvedMember(ctx context.Context, tx *sql.Tx, eventID, userID string, lock hackmembers.Lock) (model.EventMember, error) {
	m, err := hackmembers.Member(ctx, tx, eventID, userID, lock)
	if err == nil && !m.Approved() {
		err = sql.ErrNoRows
	}
	return m, err
}
