package hackteams

import (
	"context"
	"database/sql"
	"errors"

	"github.com/vsriram/simple-host/products/hack/repository/hackmembers"
)

// EnsureTeamAdmin promotes the longest-standing member (team_joined_at, then
// user_id) to admin when the team has members but none is an admin, after a
// departure, an organiser's move, or a removal. A team with no members, or
// already gone, is a no-op.
//
// Lock order: every current member of the team (sorted by user_id), then the
// team row. The successor is not known ahead of time, so every member is locked
// before the UPDATE needs one of their rows. Locking the team row first can
// deadlock against that member's own concurrent departure or admin change,
// which locks their member row before waiting on the team row.
//
// TODO(4.3): narrow the locking querier to *sql.Tx once the membership callbacks take *sql.Tx.
func EnsureTeamAdmin(ctx context.Context, q Querier, teamID string) error {
	if _, err := hackmembers.LockTeamMembers(ctx, q, teamID); err != nil {
		return err
	}
	if _, err := LockTeam(ctx, q, teamID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	if admins, err := hackmembers.Count(ctx, q, hackmembers.Filter{TeamID: teamID, TeamAdmin: true}); err != nil || admins > 0 {
		return err
	}
	eventID, userID, err := hackmembers.PromoteSuccessor(ctx, q, teamID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	// The successor hears they now answer the team's requests.
	return hackmembers.NotifyUsers(ctx, q, eventID, hackmembers.TeamAdminNotice, teamID, "", "", userID)
}

// SetTeamAdmin makes or removes an admin. The caller has already decided the
// actor may do this; this enforces the one invariant the write owns: unmaking
// the last admin is refused. userID must already be a member of teamID
// (sql.ErrNoRows otherwise, so a wrong path parameter reads as not-found rather
// than confirming membership elsewhere).
//
// Lock order: userID's own member row, then the team row, the same order every
// hack writer uses, so this never deadlocks against userID's concurrent
// leave/removal. A caller naming a second party (admin acting on a teammate)
// must lock both member rows itself, sorted, before calling: locking only the
// actor's row here, then the team row, then the target row, deadlocks against
// the symmetric request with actor and target swapped.
func SetTeamAdmin(ctx context.Context, q *sql.Tx, eventID, teamID, userID string, admin bool) error {
	m, err := lockMember(ctx, q, eventID, userID)
	if err != nil {
		return err
	}
	if !m.TeamID.Valid || m.TeamID.String != teamID {
		return sql.ErrNoRows
	}
	if _, err := LockTeam(ctx, q, teamID); err != nil {
		return err
	}
	if !admin {
		targetIsAdmin, admins, err := hackmembers.TeamAdmins(ctx, q, teamID, userID)
		if err != nil {
			return err
		}
		if targetIsAdmin && admins <= 1 {
			return ErrHackLastTeamAdmin
		}
	}
	// m was read under its lock, so team_admin is unchanged since.
	was := m.TeamAdmin
	if err = hackmembers.SetTeamAdmin(ctx, q, eventID, userID, admin); err != nil {
		return err
	}
	switch {
	case admin && !was:
		if err := hackmembers.SupersedeTeamNotices(ctx, q, teamID, userID, hackmembers.TeamAdminNotice); err != nil {
			return err
		}
		return hackmembers.NotifyUsers(ctx, q, eventID, hackmembers.TeamAdminNotice, teamID, "", "", userID)
	case !admin && was:
		// No longer true: drop the line and any email not yet sent.
		return hackmembers.SupersedeTeamNotices(ctx, q, teamID, userID, hackmembers.TeamAdminNotice)
	}
	return nil
}

// SetTeamLock locks or unlocks a team. Locking deletes every pending request
// and invite of the team and reports how many of each kind were cleared;
// unlocking a team kept after its last member left (locked for good) is
// refused. actor is recorded as locked_by only when locking.
func SetTeamLock(ctx context.Context, q *sql.Tx, teamID, actor string, locked bool) (requests, invites int, err error) {
	team, err := LockTeam(ctx, q, teamID)
	if err != nil {
		return 0, 0, err
	}
	if !locked {
		if team.KeptAfterLeave {
			return 0, 0, ErrHackTeamKept
		}
		if _, err = q.ExecContext(ctx, `UPDATE event_teams SET locked_at = NULL, locked_by = NULL WHERE id = $1`, teamID); err != nil {
			return 0, 0, err
		}
		return 0, 0, hackmembers.SupersedeTeamNotices(ctx, q, teamID, "", hackmembers.NoticeTeamLocked)
	}
	if err = q.QueryRowContext(ctx, `
		SELECT COUNT(*) FILTER (WHERE kind = 'request'), COUNT(*) FILTER (WHERE kind = 'invite')
		  FROM event_team_requests WHERE team_id = $1`, teamID).Scan(&requests, &invites); err != nil {
		return 0, 0, err
	}
	if _, err = q.ExecContext(ctx, `DELETE FROM event_team_requests WHERE team_id = $1`, teamID); err != nil {
		return 0, 0, err
	}
	if _, err = q.ExecContext(ctx, `
		UPDATE event_teams SET locked_at = COALESCE(locked_at, clock_timestamp()), locked_by = $2 WHERE id = $1`,
		teamID, actor); err != nil {
		return 0, 0, err
	}
	if !team.Locked() {
		// In the app only: a lock has nothing for the team to act on.
		if err = hackmembers.NotifyTeamMembers(ctx, q, team.EventID, teamID, hackmembers.NoticeTeamLocked, "", actor); err != nil {
			return 0, 0, err
		}
	}
	return requests, invites, nil
}
