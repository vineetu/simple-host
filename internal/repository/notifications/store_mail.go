package notifications

import (
	"context"
	"database/sql"
)

func StoreSiteOwnerEmail(ctx context.Context, database *sql.DB, siteID string) (string, error) {
	var email string
	err := database.QueryRowContext(ctx, `SELECT u.username FROM sites s JOIN users u ON u.id = s.user_id WHERE s.id = $1`, siteID).Scan(&email)
	return email, err
}
