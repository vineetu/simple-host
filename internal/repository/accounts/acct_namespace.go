package accounts

import (
	"context"
	"database/sql"
	"errors"
	"github.com/vsriram/simple-host/internal/model"
	"strings"
	"time"
)

// RenameHandleTx is RenameHandle inside the caller's transaction (PATCH
// /v1/me, which already holds the account row). ErrDomainTaken when the new
// handle is not free as an address.
func RenameHandleTx(ctx context.Context, tx *sql.Tx, userID, newHandle string) (string, error) {
	old, err := LockProfile(ctx, tx, userID)
	if err != nil {
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

// HandleRenamedSince reports whether the account moved to a new handle after
// publishing (a rename that left an alias behind) since t. Handle changes
// before anything is published leave no alias and do not count.
func HandleRenamedSince(ctx context.Context, q Querier, userID string, t time.Time) (bool, time.Time, error) {
	var last sql.NullTime
	err := q.QueryRowContext(ctx, `
		SELECT u.handle_changed_at FROM users u
		WHERE u.id = $1 AND u.handle_changed_at > $2
		  AND EXISTS (SELECT 1 FROM handle_aliases a WHERE a.user_id = u.id AND a.created_at > $2)`, userID, t).Scan(&last)
	if errors.Is(err, sql.ErrNoRows) {
		return false, time.Time{}, nil
	}
	if err != nil {
		return false, time.Time{}, err
	}
	return true, last.Time, nil
}
