package db

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/lib/pq"
)

// LeaveSelf loads one membership row plus its approval status, for
// GET/POST .../leave (hack_leave.go): every field the case logic there
// needs (role, approval status, team, display name), in one row, for any
// role and any approval status -- unlike loadMember, pending and rejected
// are not refused here. lock=true takes FOR UPDATE, as POST .../leave does
// right after locking the event row (event, then member, then team is the
// lock order every hack writer uses).
func LeaveSelf(ctx context.Context, q Querier, eventID, userID string, lock bool) (EventMember, string, error) {
	query := `SELECT event_id, user_id, role, display_name, team_id, coc_accepted_at, joined_at, approval_status
	            FROM event_members WHERE event_id = $1 AND user_id = $2`
	if lock {
		query += ` FOR UPDATE`
	}
	var m EventMember
	var status string
	err := q.QueryRowContext(ctx, query, eventID, userID).Scan(
		&m.EventID, &m.UserID, &m.Role, &m.DisplayName, &m.TeamID, &m.CocAcceptedAt, &m.JoinedAt, &status)
	return m, status, err
}

// OtherOrganiser is one organiser of an event, besides whoever asked, as
// GET/POST .../leave shows them: successors (creator case) and
// organisers_staying (co_organiser case).
type OtherOrganiser struct {
	UserID, DisplayName, Email string
}

// OtherEventOrganisers is every organiser of the event except userID,
// ordered by display name. Unlocked: POST .../leave reads it after the
// event row is already locked, which is enough to serialise against another
// organiser's concurrent leave (section 1, build plan: "the server takes
// the event row FOR UPDATE, then works out the case").
func OtherEventOrganisers(ctx context.Context, q Querier, eventID, userID string) ([]OtherOrganiser, error) {
	rows, err := queryContext(ctx, q, `
		SELECT m.user_id, m.display_name, u.username FROM event_members m
		  JOIN users u ON u.id = m.user_id
		 WHERE m.event_id = $1 AND m.role = 'organiser' AND m.user_id <> $2
		 ORDER BY m.display_name, m.user_id`, eventID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []OtherOrganiser{}
	for rows.Next() {
		var o OtherOrganiser
		if err := rows.Scan(&o.UserID, &o.DisplayName, &o.Email); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// TeamKeptIfEmptied reports whether, if leaving emptied this team right now,
// it would be kept rather than deleted. This is NOT (TeamKeepableSQL) --
// exactly, literally, the predicate deleteTeamIfEmpty itself uses -- so
// GET .../leave's preview can never promise more than POST .../leave
// actually does (Codex review, SIM-340, P1: a stage transition to closed
// forces events.submission_deadline to now, but a team's own
// deadline_override extension survives that and can still be in the
// future, so "stage NOT IN (draft, open, building)" and "not keepable" are
// NOT the same condition -- a team with a live extension previews as
// frozen under the old stage-based check but was still deletable-if-empty
// under the write path's real check). Using stage or the raw deadline here
// instead of TeamKeepableSQL itself is exactly the bug that let the
// preview and the write disagree.
func TeamKeptIfEmptied(ctx context.Context, q Querier, teamID string) (bool, error) {
	var kept bool
	err := q.QueryRowContext(ctx, `
		SELECT NOT (`+TeamKeepableSQL+`)
		  FROM event_teams t JOIN events e ON e.id = t.event_id WHERE t.id = $1`, teamID).Scan(&kept)
	return kept, err
}

// LeaveMembership is the shared core of a person losing their event
// membership, whether they chose it (POST .../leave) or an organiser removed
// them (RemovePersonFromEvent, which keeps its own organiser refusal and its
// lock-then-call shape, then calls this for the rest). The caller has already
// locked the event row; this locks the member row, then the team row (same
// order every hack writer uses: event, member, team).
//
// It does what RemovePersonFromEvent always did -- reserve the event's name,
// drop pending team requests, delete the membership, clean up an emptied team
// -- plus unsent mail queued for this person in this event, so leaving (or
// being removed) stops an announcement or an entry receipt from going out
// after the fact. Returns the team id they left, empty when they were on none.
//
// selfLeave is true only for POST .../leave (never for RemovePersonFromEvent):
// when it empties a team that deleteTeamIfEmpty kept instead of deleting,
// that team's kept_after_leave is set (Codex review, SIM-340, P2). This is
// deliberately narrower than "every team NOT TeamKeepableSQL" -- a team an
// organiser emptied with Remove person stays excluded from judging, results
// and dashboards exactly as it always was; only a team THIS feature kept
// reappears there. Changing that for Remove person too is a separate owner
// decision, not implied by widening self-leave.
func LeaveMembership(ctx context.Context, q Querier, eventID, userID string, selfLeave bool) (string, error) {
	m, err := lockMember(ctx, q, eventID, userID)
	if err != nil {
		return "", err
	}
	if m.TeamID.Valid {
		if _, err := lockTeam(ctx, q, m.TeamID.String); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return "", err
		}
	}
	// An imported member may predate reservation-on-join. Keep their event's
	// name when they leave, even if no team was ever formed.
	if _, err := q.ExecContext(ctx, `INSERT INTO event_used_names (event_slug, team_slug)
		SELECT slug, '' FROM events WHERE id=$1 ON CONFLICT DO NOTHING`, eventID); err != nil {
		return "", err
	}
	if _, err := q.ExecContext(ctx, `DELETE FROM event_team_requests WHERE event_id=$1 AND user_id=$2`, eventID, userID); err != nil {
		return "", err
	}
	// Unsent mail queued for this person in this event never goes out once
	// they are gone, whether they left or were removed.
	if _, err := q.ExecContext(ctx, `DELETE FROM event_announcement_deliveries d USING event_announcements a
		WHERE d.announcement_id = a.id AND a.event_id = $1 AND d.user_id = $2 AND d.sent_at IS NULL`, eventID, userID); err != nil {
		return "", err
	}
	if _, err := q.ExecContext(ctx, `DELETE FROM event_entry_receipts WHERE event_id=$1 AND user_id=$2 AND sent_at IS NULL`, eventID, userID); err != nil {
		return "", err
	}
	if _, err := q.ExecContext(ctx, `DELETE FROM event_members WHERE event_id = $1 AND user_id = $2`, eventID, userID); err != nil {
		return "", err
	}
	if !m.TeamID.Valid {
		return "", nil
	}
	if err := deleteTeamIfEmpty(ctx, q, m.TeamID.String); err != nil {
		return "", err
	}
	if selfLeave {
		if err := markTeamKeptAfterLeave(ctx, q, m.TeamID.String); err != nil {
			return "", err
		}
	}
	return m.TeamID.String, nil
}

// markTeamKeptAfterLeave sets kept_after_leave on a team LeaveMembership's own
// deleteTeamIfEmpty call just kept instead of deleting: true only once the
// row still exists (it wasn't keepable, so deleteTeamIfEmpty's DELETE did not
// match it) and has no members. A no-op, not an error, when the team was
// deleted instead (0 rows match) or still has someone on it.
func markTeamKeptAfterLeave(ctx context.Context, q Querier, teamID string) error {
	_, err := q.ExecContext(ctx, `
		UPDATE event_teams SET kept_after_leave = true
		 WHERE id = $1 AND NOT EXISTS (SELECT 1 FROM event_members WHERE team_id = $1)`, teamID)
	return err
}

// RecordFormerJudge preserves every team a leaving judge or judging organiser
// already scored AND was eligible for under the event's current mode,
// assignments and panels, so leaving (section 2.3's recordFormerJudge)
// doesn't change what their scores counted toward. Call it before
// LeaveMembership deletes the member row (EligibleJudgeTeamsIgnoringConflicts
// needs the member row to still exist). A no-op when the leaver never scored
// anything eligible in this event.
//
// counted_team_ids is scored teams intersected with
// EligibleJudgeTeamsIgnoringConflicts, not EligibleJudgeTeams (Grok security
// review, SIM-340: that set already excludes a team with a live conflict, so
// intersecting against it here froze out any team an organiser had
// conflicted by mistake even after the conflict was removed again -- there
// was no way back in, because the team was never in the frozen set to begin
// with). scoreEligiblePredicate's former-judge branch still checks
// event_conflicts live on every read, so a real, standing conflict keeps
// excluding that team; only a conflict that no longer exists stops being
// held against it.
//
// The mode/assignment/panel intersection itself is ship-blocker B2: without
// it, every team the leaver ever scored froze in regardless of whether it
// was ever eligible, so a score that never counted (wrong mode, never
// assigned, no panel) started counting the moment they left.
func RecordFormerJudge(ctx context.Context, q Querier, eventID, userID, role, displayName string) error {
	eligible, err := EligibleJudgeTeamsIgnoringConflicts(ctx, q, eventID, userID)
	if err != nil {
		return err
	}
	rows, err := queryContext(ctx, q, `SELECT DISTINCT team_id FROM event_scores WHERE event_id=$1 AND judge_id=$2`, eventID, userID)
	if err != nil {
		return err
	}
	defer rows.Close()
	counted := []string{}
	for rows.Next() {
		var teamID string
		if err := rows.Scan(&teamID); err != nil {
			return err
		}
		if eligible[teamID] {
			counted = append(counted, teamID)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(counted) == 0 {
		return nil
	}
	_, err = q.ExecContext(ctx, `
		INSERT INTO event_former_judges (event_id, user_id, display_name, role, counted_team_ids, left_at)
		VALUES ($1, $2, $3, $4, $5, now())
		ON CONFLICT (event_id, user_id) DO UPDATE SET
			display_name = EXCLUDED.display_name, role = EXCLUDED.role,
			counted_team_ids = EXCLUDED.counted_team_ids, left_at = EXCLUDED.left_at`,
		eventID, userID, displayName, role, pq.Array(counted))
	return err
}

// FormerJudge is one event_former_judges row, for the judging dashboard.
type FormerJudge struct {
	UserID, DisplayName, Role string
	LeftAt                    time.Time
}

// ListFormerJudges is every former judge of the event, oldest departure first.
func ListFormerJudges(ctx context.Context, q Querier, eventID string) ([]FormerJudge, error) {
	rows, err := queryContext(ctx, q, `
		SELECT user_id, display_name, role, left_at FROM event_former_judges
		 WHERE event_id = $1 ORDER BY left_at ASC`, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []FormerJudge{}
	for rows.Next() {
		var f FormerJudge
		if err := rows.Scan(&f.UserID, &f.DisplayName, &f.Role, &f.LeftAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// FormerJudgeAssignedCount is how many of today's judge-eligible teams this
// former judge would be assigned under the event's current mode, ignoring
// that their role is gone -- the same universe a current judge's
// assigned_count counts (dashboard display only; EligibleJudgeTeams keeps the
// authoritative, frozen set that actually counts toward totals). The
// membership/kept_after_leave population here must match ListTeamsWithMembers
// exactly (Codex review, SIM-340, P2 follow-on): otherwise a scored team a
// leave kept empty shows 1 counted team but 0 assigned on the dashboard,
// since this count dropped it while ListTeamsWithMembers kept it.
func FormerJudgeAssignedCount(ctx context.Context, q Querier, eventID, judgeID string) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM event_teams t JOIN events e ON e.id = t.event_id
		 WHERE e.id = $1 AND (EXISTS (SELECT 1 FROM event_members em WHERE em.team_id = t.id) OR t.kept_after_leave)
		   AND NOT EXISTS (SELECT 1 FROM event_conflicts c WHERE c.event_id = e.id AND c.judge_id = $2 AND c.team_id = t.id)
		   AND (e.judge_assignment_mode = 'open'
		    OR (e.judge_assignment_mode IN ('automatic', 'manual') AND EXISTS (SELECT 1 FROM event_assignments a WHERE a.event_id = e.id AND a.judge_id = $2 AND a.team_id = t.id))
		    OR (e.judge_assignment_mode = 'panel' AND EXISTS (SELECT 1 FROM event_track_panels p WHERE p.event_id = e.id AND p.track_id = t.track_id AND p.judge_id = $2)))`,
		eventID, judgeID).Scan(&n)
	return n, err
}

// DeleteFormerJudgeScores removes a departed judge's score rows and their
// former-judge record. false means there was no such record: either nobody
// by that id ever left scores behind, or they are a member again (rejoining
// already deleted the record, via the clear_hack_former_judge trigger).
func DeleteFormerJudgeScores(ctx context.Context, q Querier, eventID, userID string) (bool, error) {
	var id string
	err := q.QueryRowContext(ctx, `
		SELECT user_id FROM event_former_judges WHERE event_id = $1 AND user_id = $2 FOR UPDATE`, eventID, userID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if _, err := q.ExecContext(ctx, `DELETE FROM event_scores WHERE event_id = $1 AND judge_id = $2`, eventID, userID); err != nil {
		return false, err
	}
	if _, err := q.ExecContext(ctx, `DELETE FROM event_former_judges WHERE event_id = $1 AND user_id = $2`, eventID, userID); err != nil {
		return false, err
	}
	return true, nil
}
