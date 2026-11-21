package teams

import (
	"context"
	"database/sql"
	"sort"

	model "github.com/vsriram/simple-host/products/hack/model"
	teamrepo "github.com/vsriram/simple-host/products/hack/repository/hackteams"
)

// LockEventStage locks the event row FOR SHARE (event, then members, then teams) and returns its current stage.
func LockEventStage(ctx context.Context, tx *sql.Tx, eventID string) (string, error) {
	var stage string
	err := teamrepo.LockEventStageShared(ctx, tx, eventID).Scan(&stage)
	return stage, err
}

// LockCallerTeam locks the event, the caller's member row and their team row; sql.ErrNoRows when on no team. stage is returned even on error.
func LockCallerTeam(ctx context.Context, tx *sql.Tx, eventID, userID string) (team model.EventTeam, admin bool, stage string, err error) {
	stage, err = LockEventStage(ctx, tx, eventID)
	if err != nil {
		return model.EventTeam{}, false, stage, err
	}
	var teamID sql.NullString
	if err = teamrepo.LockApprovedMemberTeam(ctx, tx,
		eventID, userID).Scan(&teamID, &admin); err != nil {
		return model.EventTeam{}, false, stage, err
	}
	if !teamID.Valid {
		return model.EventTeam{}, false, stage, sql.ErrNoRows
	}
	if _, err = teamrepo.LockCallerTeamRow(ctx, tx, teamID.String); err != nil {
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
	if ids[0] == ids[1] {
		if _, err = teamrepo.LockSingleMemberRow(ctx, tx, eventID, ids[0]); err != nil {
			return "", false, stage, err
		}
	} else if _, err = teamrepo.LockTwoMemberRows(ctx, tx, eventID, ids[0], ids[1]); err != nil {
		return "", false, stage, err
	}
	var team sql.NullString
	if err = teamrepo.ReadApprovedMemberTeam(ctx, tx,
		eventID, callerID).Scan(&team, &callerAdmin); err != nil {
		return "", false, stage, err
	}
	if !team.Valid {
		return "", false, stage, sql.ErrNoRows
	}
	if _, err = teamrepo.LockTargetTeamRow(ctx, tx, team.String); err != nil {
		return "", false, stage, err
	}
	return team.String, callerAdmin, stage, nil
}
