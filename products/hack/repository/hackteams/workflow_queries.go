package hackteams

import (
	"context"
	"database/sql"

	"github.com/vsriram/simple-host/host/sqlkit"
	"github.com/vsriram/simple-host/products/hack/repository/hackevents"
)

const (
	adminSQL1 = `UPDATE event_teams SET looking_for = $2 WHERE id = $1`

	createSQL1 = `SELECT EXISTS (SELECT 1 FROM event_teams WHERE event_id = $1 AND lower(name) = lower($2))`

	findingSQL5 = `SELECT team_id, user_id, kind FROM event_team_requests WHERE event_id = $1 AND id = $2`

	findingSQL6 = `SELECT id FROM event_teams WHERE event_id = $1 AND slug = $2`

	findingSQL7 = `SELECT id FROM event_team_requests WHERE team_id = $1 AND user_id = $2`

	organiserSQL2 = `UPDATE event_teams SET code = $2 WHERE id = $1`

	peopleSQL1 = `SELECT looking_for FROM event_teams WHERE id = $1`
)

// openSQL1 is the event's unlocked teams still taking people, with $2 the
// event's stage freezing every team and $3 its submission deadline.
var openSQL1 = `SELECT t.id,t.slug,t.name,t.looking_for,COALESCE(t.track_id::text,'')
 FROM event_teams t
 WHERE t.event_id=$1 AND t.locked_at IS NULL AND NOT $2
   AND NOT COALESCE(` + hackevents.EffectiveDeadline("$3::timestamptz") + ` <= clock_timestamp(), false)
 ORDER BY t.created_at`

const savepointSQL = "SAVEPOINT hack_try"

const rollbackSavepointSQL = "ROLLBACK TO SAVEPOINT hack_try"

const releaseSavepointSQL = "RELEASE SAVEPOINT hack_try"

func ReadTeamRequest(ctx context.Context, q sqlkit.Querier, eventID, requestID string) *sql.Row {
	return q.QueryRowContext(ctx, findingSQL5, eventID, requestID)
}

func TeamIDBySlug(ctx context.Context, q sqlkit.Querier, eventID, slug string) *sql.Row {
	return q.QueryRowContext(ctx, findingSQL6, eventID, slug)
}

func TeamRequestID(ctx context.Context, q sqlkit.Querier, teamID, userID string) *sql.Row {
	return q.QueryRowContext(ctx, findingSQL7, teamID, userID)
}

func SetTeamLookingFor(ctx context.Context, q sqlkit.Querier, teamID, note string) (sql.Result, error) {
	return q.ExecContext(ctx, adminSQL1, teamID, note)
}

// ListFindingTeams lists the event's teams open to finding: id, slug, name,
// looking_for and track id ("" for none).
func ListFindingTeams(ctx context.Context, q *sql.DB, eventID string) (*sql.Rows, error) {
	ev, err := hackevents.GetEventByID(ctx, q, eventID)
	if err != nil {
		return nil, err
	}
	return q.QueryContext(ctx, openSQL1, eventID, hackevents.FrozenStage(ev.Stage), ev.SubmissionDeadline)
}

func SetTeamCode(ctx context.Context, q sqlkit.Querier, teamID, code string) (sql.Result, error) {
	return q.ExecContext(ctx, organiserSQL2, teamID, code)
}

func TeamNameExists(ctx context.Context, q sqlkit.Querier, eventID, name string) *sql.Row {
	return q.QueryRowContext(ctx, createSQL1, eventID, name)
}

func StartWorkflowSavepoint(ctx context.Context, q sqlkit.Querier) (sql.Result, error) {
	return q.ExecContext(ctx, savepointSQL)
}

func RollbackWorkflowSavepoint(ctx context.Context, q sqlkit.Querier) (sql.Result, error) {
	return q.ExecContext(ctx, rollbackSavepointSQL)
}

func ReleaseWorkflowSavepoint(ctx context.Context, q sqlkit.Querier) (sql.Result, error) {
	return q.ExecContext(ctx, releaseSavepointSQL)
}

func ReadTeamLookingFor(ctx context.Context, q sqlkit.Querier, teamID string) *sql.Row {
	return q.QueryRowContext(ctx, peopleSQL1, teamID)
}
