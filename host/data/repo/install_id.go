package repo

import (
	"context"
	"database/sql"

	"github.com/vsriram/simple-host/host/sqlkit"
)

// InstallID returns the identity saved in the database, or "" when no row
// exists. It never writes.
func InstallID(ctx context.Context, db sqlkit.Querier) (string, error) {
	var id string
	err := db.QueryRowContext(ctx, `SELECT id FROM install_identity`).Scan(&id)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return id, err
}

// CreateInstallID saves id as the install's identity unless one is already
// there, and returns the identity now stored (another process may have won).
func CreateInstallID(ctx context.Context, db *sql.DB, id string) (string, error) {
	if _, err := db.ExecContext(ctx, `INSERT INTO install_identity(id) VALUES($1) ON CONFLICT DO NOTHING`, id); err != nil {
		return "", err
	}
	return InstallID(ctx, db)
}
