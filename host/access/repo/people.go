package repo

import (
	"context"
	"database/sql"

	"github.com/lib/pq"
)

// People: what the other modules read and change of an account. Only this
// package queries the users table; everyone else calls these (or
// GetUserByID, GetUserByHandle, GetUserByUsername, UserSuspended and
// LockProfile). Products reach them through host/access/product.go.

// UsersByIDs loads the accounts with these ids in one query, keyed by id.
// Unknown ids are left out.
func UsersByIDs(ctx context.Context, q Querier, ids []string) (map[string]User, error) {
	out := make(map[string]User, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := q.QueryContext(ctx, `SELECT `+userColumns+` FROM users WHERE id = ANY($1::uuid[])`, pq.Array(ids))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out[u.ID] = u
	}
	return out, rows.Err()
}

// SortByDisplayName returns ids ordered by display name, then id, as the
// database orders them (lists people see in name order). Unknown ids are
// left out.
func SortByDisplayName(ctx context.Context, q Querier, ids []string) ([]string, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	return scanStrings(ctx, q, `SELECT id::text FROM users WHERE id = ANY($1::uuid[])
		ORDER BY COALESCE(display_name, ''), id`, pq.Array(ids))
}

// SuspendedAccountIDs lists every suspended account: their sites are down.
func SuspendedAccountIDs(ctx context.Context, q Querier) ([]string, error) {
	return scanStrings(ctx, q, `SELECT id::text FROM users WHERE suspended_at IS NOT NULL`)
}

// HandleTaken reports whether label is an account's handle, or (aliases)
// an old handle one keeps.
func HandleTaken(ctx context.Context, q Querier, label string, aliases bool) (bool, error) {
	var taken bool
	err := q.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM users WHERE handle = $1)
		    OR ($2 AND EXISTS (SELECT 1 FROM handle_aliases WHERE handle = $1))`, label, aliases).Scan(&taken)
	return taken, err
}

// AccountIDsByEmail lists the accounts whose sign-in address is email,
// ignoring case (older accounts may differ only in case).
func AccountIDsByEmail(ctx context.Context, q Querier, email string) ([]string, error) {
	return scanStrings(ctx, q, `SELECT id::text FROM users WHERE lower(username) = lower($1)`, email)
}

// HomeSiteID is the site the account chose as its home page, if any.
func HomeSiteID(ctx context.Context, q Querier, userID string) (sql.NullString, error) {
	var id sql.NullString
	err := q.QueryRowContext(ctx, `SELECT home_site_id FROM users WHERE id = $1`, userID).Scan(&id)
	return id, err
}

// ClearHomeSite lets go of a site as anyone's home page (it was deleted).
func ClearHomeSite(ctx context.Context, q Querier, siteID string) error {
	_, err := q.ExecContext(ctx, `UPDATE users SET home_site_id = NULL WHERE home_site_id = $1`, siteID)
	return err
}

// DeleteAccountRow deletes an account whose deletion its product decided (a
// Simple Hack event's account): its sites and memberships cascade with it.
func DeleteAccountRow(ctx context.Context, tx *sql.Tx, accountID string) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, accountID)
	return err
}

// SignedInAmong (for host/data) finds visitor IDs recorded for a site, including live sessions.
func SignedInAmong(ctx context.Context, db *sql.DB, siteID string, ids []string) (map[string]bool, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT user_id::text FROM site_visitor_signins WHERE site_id = $1 AND user_id::text = ANY($2)
		UNION SELECT user_id::text FROM visitor_sessions WHERE site_id = $1 AND user_id::text = ANY($2)`, siteID, pq.Array(ids))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	found := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		found[id] = true
	}
	return found, rows.Err()
}

// CandidateVisitorIDsForEmail (for host/data) returns the distinct visitor ids, among
// people recorded by the server as having signed in on this exact site,
// whose username or any OAuth identity email case-insensitively matches
// email (the same users/oauth_identities join ListSiteViewers uses, the
// other direction). This is a candidate list, not a resolved identity: a
// username or an OAuth identity's own email is not necessarily the one
// address a visitor currently signs in with (see
// host/handler/privatecollections.go:visitorEmail, the canonical,
// forward resolver — self-signup password accounts and more than one OAuth
// identity both make username/oauth_identities an imprecise index). The
// caller must re-resolve each candidate's current, canonical email the
// same way and only trust an exact, unambiguous match. The caller
// lower-cases email first.
func CandidateVisitorIDsForEmail(ctx context.Context, db *sql.DB, siteID, email string) ([]string, error) {
	if email == "" {
		return nil, nil
	}
	rows, err := db.QueryContext(ctx, `
		SELECT DISTINCT s.user_id::text FROM site_visitor_signins s
		WHERE s.site_id = $1 AND (
			EXISTS (SELECT 1 FROM users u WHERE u.id = s.user_id AND lower(u.username) = $2)
			OR EXISTS (SELECT 1 FROM oauth_identities oi WHERE oi.user_id = s.user_id AND lower(oi.email) = $2)
		)`, siteID, email)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// AdminAccountIDs lists the operator accounts (for host/ops reports).
func AdminAccountIDs(ctx context.Context, q Querier) ([]string, error) {
	return accountIDs(q.QueryContext(ctx, `SELECT id::text FROM users WHERE is_admin`))
}

// AccountsCreatedBetween maps each account created on a UTC day in [from, to]
// to that day (for host/ops growth).
func AccountsCreatedBetween(ctx context.Context, q Querier, from, to string) (map[string]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT id::text, created_at::date::text FROM users WHERE created_at >= $1::date AND created_at < $2::date + 1`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, day string
		if err := rows.Scan(&id, &day); err != nil {
			return nil, err
		}
		out[id] = day
	}
	return out, rows.Err()
}

func accountIDs(rows *sql.Rows, err error) ([]string, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// HandleOwners maps every account handle to its account id.
func HandleOwners(ctx context.Context, q Querier) (map[string]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT id, handle FROM users WHERE handle IS NOT NULL AND handle <> ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, handle string
		if err := rows.Scan(&id, &handle); err != nil {
			return nil, err
		}
		out[handle] = id
	}
	return out, rows.Err()
}

// scanStrings is the single text column query returns, nil when there are no rows.
func scanStrings(ctx context.Context, q Querier, query string, args ...any) ([]string, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// LockUser loads the account and holds it (FOR SHARE) until the caller's
// transaction ends: a suspension cannot commit between reading it and
// acting on it. Use it in the transaction that acts.
func LockUser(ctx context.Context, tx *sql.Tx, id string) (User, error) {
	return scanUser(tx.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE id = $1 FOR SHARE`, id))
}
