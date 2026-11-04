package accounts

import (
	"context"
	"database/sql"
	"strings"

	"github.com/vsriram/simple-host/internal/model"
)

func RenameHandle(ctx context.Context, database *sql.DB, userID, newHandle string) (string, error) {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	old, err := RenameHandleTx(ctx, tx, userID, newHandle)
	if err != nil {
		return "", err
	}
	return old, tx.Commit()
}
func RenameHandleTx(ctx context.Context, tx *sql.Tx, userID, newHandle string) (string, error) {
	var old sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT handle FROM users WHERE id = $1 FOR UPDATE`, userID).Scan(&old); err != nil {
		return "", err
	}
	// The old handle becomes an alias, a name in the shared namespace: hold its
	// lock too (both in one order, so two renames never wait on each other in
	// a circle), so a claim of <old>.<SITE_DOMAIN> cannot slip in between.
	if domain := CurrentPlatformDomain(); domain != "" && old.Valid && old.String != "" {
		names := []string{strings.ToLower(old.String), strings.ToLower(newHandle)}
		if names[1] < names[0] {
			names[0], names[1] = names[1], names[0]
		}
		for _, n := range names {
			if err := LockPlatformName(ctx, tx, n+"."+domain); err != nil {
				return "", err
			}
		}
	}
	ok, err := HandleAvailable(ctx, tx, userID, newHandle)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", model.ErrDomainTaken
	}
	if _, err := tx.ExecContext(ctx, `UPDATE users SET handle = $2, handle_changed_at = now() WHERE id = $1`, userID, newHandle); err != nil {
		return "", err
	}
	if old.Valid && old.String != "" && old.String != newHandle {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO handle_aliases (handle, user_id) VALUES ($1, $2)
			ON CONFLICT (handle) DO UPDATE SET user_id = EXCLUDED.user_id, created_at = now()`, old.String, userID); err != nil {
			return "", err
		}
	}
	// The new handle stops being anyone's alias.
	// Only this account's own old alias: another account's alias is never
	// removed here (with namespace checks off, HandleAvailable does not stop it).
	if _, err := tx.ExecContext(ctx, `DELETE FROM handle_aliases WHERE handle = $1 AND user_id = $2`, newHandle, userID); err != nil {
		return "", err
	}
	return old.String, nil
}
