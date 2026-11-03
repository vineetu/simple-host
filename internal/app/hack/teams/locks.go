package teams

import (
	"context"
	"database/sql"
	"sort"

	model "github.com/vsriram/simple-host/internal/model"
	teamrepo "github.com/vsriram/simple-host/internal/repository/hackteams"
)

// LockEventStage locks the event row FOR SHARE and returns its stage,
// fresh, inside the transaction. setStage takes the event row FOR UPDATE
// as its very first step (and every team row FOR UPDATE right after), so
// this either waits for a setStage already running to commit and then
// sees its result, or runs first and makes setStage wait for it -- never a
// stage value read before this transaction even began. Lock order for
// every handler that calls this: the event row first, then member rows,
// then team rows, the same order setStage and every other hack writer
// uses (Grok security review, 2026-11-01: the former transport lockCallerTeam's comment used
// to claim the event was "already locked by loadMember's read", which it
// never was -- loadMember's read holds no lock at all).
func LockEventStage(ctx context.Context, tx *sql.Tx, eventID string) (string, error) {
	var stage string
	err := teamrepo.LockEventStageShared(ctx, tx, eventID).Scan(&stage)
	return stage, err
}

// LockCallerTeam locks the event row (see LockEventStage), then the
// caller's member row, then (if they are on a team) the team row.
// sql.ErrNoRows when the caller is on no team. stage is always returned
// (even on a later error) so the caller can re-check hackteams.ParticipantTeamStages
// with a value that is guaranteed current, not the one read before BEGIN.
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

// LockCallerAndTargetTeam locks the event row (see LockEventStage), then
// the caller's and target's membership rows together, sorted by user_id
// (the same person collapses to one row), then the caller's team row --
// always in that order. Used only where a second member is named (the
// admin toggle): locking just the caller's row, then the team row, then
// the target's row (as a plain LockCallerTeam call followed by a write on
// userID would) reproduces a deadlock against that same member's own
// concurrent request naming the caller back as its target (Codex review,
// 2026-11-01) -- Postgres aborts one side with a 500 instead of either
// succeeding or cleanly refusing. Returns the caller's team id
// (sql.ErrNoRows when they are on no team), whether they are its admin,
// and the event's current stage; it does not check that target is on the
// same team, since SetTeamAdmin does that under the same locks.
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
