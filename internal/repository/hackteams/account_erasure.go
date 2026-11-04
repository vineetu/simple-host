package hackteams

import (
	"context"
	"database/sql"
)

func ErasureEventAccount(ctx context.Context, tx *sql.Tx, id string, out *string) error {
	return tx.QueryRowContext(ctx, `SELECT e.account_id FROM event_teams t JOIN events e ON e.id=t.event_id WHERE t.id=$1`, id).Scan(out)
}
func ErasureTeamExists(ctx context.Context, tx *sql.Tx, id string, out *bool) error {
	return tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM event_teams WHERE id=$1)`, id).Scan(out)
}
func ErasureMemberCount(ctx context.Context, tx *sql.Tx, id string, out *int) error {
	return tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM event_members WHERE team_id=$1`, id).Scan(out)
}
