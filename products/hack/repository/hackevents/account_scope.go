package hackevents

import (
	"context"

	"github.com/vsriram/simple-host/host/sqlkit"
	hackmodel "github.com/vsriram/simple-host/products/hack/model"
)

type TeamWriteState = hackmodel.TeamWriteState

const teamWriteStateCols = `t.id, e.id, e.slug, t.slug, e.account_id, e.stage, e.taken_down_at IS NOT NULL,
	t.site_taken_down_at IS NOT NULL,
	` + EffectiveDeadlineSQL + `,
	clock_timestamp(), t.pinned_at, t.pinned_version,
	COALESCE(t.deadline_override > e.submission_deadline, false),
	e.submission_deadline, t.deadline_override`

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
