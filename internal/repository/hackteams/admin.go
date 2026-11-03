package hackteams

import (
	"context"
	"database/sql"
	"errors"

	"github.com/vsriram/simple-host/internal/repository/hackevents"
)

// EnsureTeamAdmin promotes the longest-standing member (team_joined_at, then
// user_id) to admin when the team has members but none of them is an admin
// -- after a departure, an organiser's move, or a removal. Lock order:
// every current member of the team (sorted by user_id), then the team row
// -- the successor isn't known ahead of time, so every member is locked
// before the UPDATE that follows ever needs one of their rows. Locking the
// team row first instead (as this used to) can deadlock against that same
// member's own concurrent departure or admin change, which locks their
// member row before waiting on the team row (Codex review, 2026-11-01).
// A team with no members, or already gone, is a no-op.
// TODO(4.3): narrow the locking querier to *sql.Tx once the membership callbacks take *sql.Tx.
func EnsureTeamAdmin(ctx context.Context, q Querier, teamID string) error {
	var locked int
	if err := q.QueryRowContext(ctx, `
		SELECT count(*) FROM (SELECT 1 FROM event_members WHERE team_id = $1 ORDER BY user_id FOR UPDATE) x`,
		teamID).Scan(&locked); err != nil {
		return err
	}
	if _, err := LockTeam(ctx, q, teamID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	var hasAdmin bool
	if err := q.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM event_members WHERE team_id = $1 AND team_admin)`, teamID).Scan(&hasAdmin); err != nil {
		return err
	}
	if hasAdmin {
		return nil
	}
	// NULLS LAST: Postgres sorts NULL first by default, which would hand
	// admin to a member with no team_joined_at ahead of one with an actual,
	// earlier tenure (Grok security review, 2026-11-01). Every current
	// writer sets team_joined_at, but this ordering must not depend on
	// that staying true.
	var eventID, userID string
	err := q.QueryRowContext(ctx, `
		UPDATE event_members SET team_admin = true
		 WHERE team_id = $1 AND user_id = (
		   SELECT user_id FROM event_members WHERE team_id = $1
		    ORDER BY team_joined_at NULLS LAST, user_id LIMIT 1)
		RETURNING event_id, user_id`,
		teamID).Scan(&eventID, &userID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	// The successor hears they now answer the team's requests (2026-11-02).
	return hackevents.NotifyUsers(ctx, q, eventID, hackevents.TeamAdminNotice, teamID, "", "", userID)
}

// SetTeamAdmin makes or removes an admin. The caller has already decided the
// actor may do this (an existing admin, or the organiser); this enforces the
// one invariant the write itself owns: unmaking the last admin is refused.
// userID must already be a member of teamID (sql.ErrNoRows otherwise, so a
// wrong path parameter reads as not-found rather than confirming membership
// elsewhere). Lock order: userID's own member row, then the team row --
// the same order every hack writer uses (member, then team), so this never
// deadlocks against userID's own concurrent leave/removal, which locks
// their row first too. A caller naming a second party (the participant
// route, admin acting on a teammate) must lock both member rows itself,
// sorted, before calling this (Codex review, 2026-11-01): locking only the
// actor's row here, then the team row, then this target row, reproduces
// the same deadlock against the symmetric request with actor and target
// swapped.
func SetTeamAdmin(ctx context.Context, q *sql.Tx, eventID, teamID, userID string, admin bool) error {
	m, err := LockMember(ctx, q, eventID, userID)
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
		var targetIsAdmin bool
		var admins int
		if err := q.QueryRowContext(ctx, `
			SELECT COALESCE(bool_or(user_id = $2 AND team_admin), false), COUNT(*) FILTER (WHERE team_admin)
			  FROM event_members WHERE team_id = $1`, teamID, userID).Scan(&targetIsAdmin, &admins); err != nil {
			return err
		}
		if targetIsAdmin && admins <= 1 {
			return ErrHackLastTeamAdmin
		}
	}
	var was bool
	if err := q.QueryRowContext(ctx, `SELECT team_admin FROM event_members WHERE event_id = $1 AND user_id = $2`, eventID, userID).Scan(&was); err != nil {
		return err
	}
	if _, err = q.ExecContext(ctx, `UPDATE event_members SET team_admin = $3 WHERE event_id = $1 AND user_id = $2`, eventID, userID, admin); err != nil {
		return err
	}
	switch {
	case admin && !was:
		if err := hackevents.SupersedeTeamNotices(ctx, q, teamID, userID, hackevents.TeamAdminNotice); err != nil {
			return err
		}
		return hackevents.NotifyUsers(ctx, q, eventID, hackevents.TeamAdminNotice, teamID, "", "", userID)
	case !admin && was:
		// No longer true: drop the line and any email not yet sent.
		return hackevents.SupersedeTeamNotices(ctx, q, teamID, userID, hackevents.TeamAdminNotice)
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
		return 0, 0, hackevents.SupersedeTeamNotices(ctx, q, teamID, "", hackevents.NoticeTeamLocked)
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
		if err = hackevents.NotifyTeamMembers(ctx, q, team.EventID, teamID, hackevents.NoticeTeamLocked, "", actor); err != nil {
			return 0, 0, err
		}
	}
	return requests, invites, nil
}
