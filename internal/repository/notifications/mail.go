package notifications

import (
	"context"
	"database/sql"
	"github.com/vsriram/simple-host/internal/repository/sqlkit"
)

func SiteOwnerEmail(ctx context.Context, q sqlkit.Querier, id string) (string, error) {
	var email string
	err := q.QueryRowContext(ctx, `SELECT u.username FROM sites s JOIN users u ON u.id=s.user_id WHERE s.id=$1`, id).Scan(&email)
	return email, err
}
func ResetSuspensionEmail(ctx context.Context, q sqlkit.Querier, id string) error {
	_, err := q.ExecContext(ctx, `UPDATE site_notifications SET suspend_emailed_at=NULL WHERE id=$1`, id)
	return err
}

func SuspensionEmails(ctx context.Context, q *sql.DB) (ids []string, readErr error, err error) {
	rows, err := q.QueryContext(ctx, `SELECT id FROM site_notifications WHERE state='suspended' AND suspend_emailed_at IS NULL`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	return ids, rows.Err(), nil
}
