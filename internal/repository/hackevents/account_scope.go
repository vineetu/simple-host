package hackevents

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/vsriram/simple-host/internal/model"
	"github.com/vsriram/simple-host/internal/repository/sqlkit"
)

type TeamIdentity = model.TeamIdentity
type TeamWriteState = model.TeamWriteState

var ErrTeamKeyInactive = fmt.Errorf("team key no longer active: %w", sql.ErrNoRows)

const teamWriteStateCols = `t.id, e.id, e.slug, t.slug, e.account_id, e.stage, e.taken_down_at IS NOT NULL,
	(t.site_taken_down_at IS NOT NULL OR EXISTS (SELECT 1 FROM sites s WHERE s.user_id = e.account_id
	   AND s.name = t.slug AND s.deleted_at IS NULL AND s.suspended_at IS NOT NULL)),
	` + EffectiveDeadlineSQL + `,
	clock_timestamp(), t.pinned_at, t.pinned_version,
	COALESCE(t.deadline_override > e.submission_deadline, false),
	e.submission_deadline, t.deadline_override`

// ResolveTeamIdentity checks that memberID is a participant on teamID right
// now and returns what their team credential acts as. ErrTeamKeyInactive when
// not.
func ResolveTeamIdentity(ctx context.Context, q sqlkit.Querier, memberID, teamID string) (TeamIdentity, error) {
	var t TeamIdentity
	err := q.QueryRowContext(ctx, `
		SELECT e.id, e.slug, t.id, t.slug, e.account_id, m.user_id
		  FROM event_teams t
		  JOIN events e ON e.id = t.event_id
		  JOIN event_members m ON m.event_id = e.id AND m.team_id = t.id
		 WHERE t.id = $1::uuid AND m.user_id = $2::uuid AND m.role = 'participant'
		   AND m.approval_status = 'approved'`,
		teamID, memberID).Scan(&t.EventID, &t.EventSlug, &t.TeamID, &t.TeamSlug, &t.AccountID, &t.MemberID)
	if errors.Is(err, sql.ErrNoRows) {
		return t, ErrTeamKeyInactive
	}
	return t, err
}

// TeamWriteStateFor reads a team's write state. In a transaction with share
// set, the team row is held FOR SHARE until the transaction ends, so the
// deadline pin (PinDueTeams, FOR UPDATE) waits for a deploy that was let in
// before the deadline and then pins what it made live. The time compared is
// clock_timestamp(), not the transaction's start.
func TeamWriteStateFor(ctx context.Context, q sqlkit.Querier, teamID string, share bool) (TeamWriteState, error) {
	lock := ""
	if share {
		lock = " FOR SHARE OF t"
	}
	return scanTeamWriteState(q.QueryRowContext(ctx, `SELECT `+teamWriteStateCols+`
		  FROM event_teams t JOIN events e ON e.id = t.event_id
		 WHERE t.id = $1::uuid`+lock, teamID))
}

func scanTeamWriteState(row interface{ Scan(...any) error }) (TeamWriteState, error) {
	var s TeamWriteState
	err := row.Scan(
		&s.TeamID, &s.EventID, &s.EventSlug, &s.TeamSlug, &s.AccountID, &s.Stage, &s.EventTakenDown,
		&s.SiteTakenDown, &s.Deadline, &s.Now, &s.PinnedAt, &s.PinnedVersion, &s.HasOverride,
		&s.EventDeadline, &s.TeamDeadlineSet)
	return s, err
}

// teamKeyBinding returns the team a stored team key is bound to.
func TeamKeyBinding(ctx context.Context, q sqlkit.Querier, keyHash string) (teamID, memberID string, err error) {
	err = q.QueryRowContext(ctx, `
		SELECT b.team_id, b.user_id FROM event_team_keys b JOIN api_keys k ON k.id = b.key_id
		 WHERE k.key_hash = $1`, keyHash).Scan(&teamID, &memberID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", ErrTeamKeyInactive
	}
	return teamID, memberID, err
}
