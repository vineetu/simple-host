package repo

import (
	"context"
	"database/sql"
	"errors"

	"github.com/lib/pq"
	"github.com/vsriram/simple-host/host/model"
)

// LockAccountForDelete locks the account row inside tx. sql.ErrNoRows when
// there is no such account.
func LockAccountForDelete(ctx context.Context, tx *sql.Tx, id string) (model.AccountForDelete, error) {
	var a model.AccountForDelete
	var handle sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT id, username, handle, COALESCE(is_admin, false), suspended_at IS NOT NULL
		FROM users WHERE id::text = $1 FOR UPDATE`, id).Scan(&a.ID, &a.Username, &handle, &a.IsAdmin, &a.Suspended)
	if err != nil {
		return a, err
	}
	a.Handle = handle.String
	return a, nil
}

// ListVisitorSignIns returns the sites this person holds visitor sessions on.
func ListVisitorSignIns(ctx context.Context, database *sql.DB, userID string) ([]model.VisitorSignIn, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT site_id::text, host, min(created_at), max(last_seen_at)
		  FROM visitor_sessions WHERE user_id = $1
		 GROUP BY site_id, host ORDER BY min(created_at)`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.VisitorSignIn
	for rows.Next() {
		var v model.VisitorSignIn
		if err := rows.Scan(&v.SiteID, &v.Host, &v.FirstSeen, &v.LastSeen); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ReleaseAccountNames is Access's part of an erase, before the account row
// goes: it returns the account's old handles, and keeps them and its handle
// held by nobody (the row's cascade would free them), and drops the sign-in
// codes sent to its email (keyed by email, not by account).
func ReleaseAccountNames(ctx context.Context, tx *sql.Tx, a model.AccountForDelete) ([]string, error) {
	aliases, err := scanStrings(ctx, tx, `SELECT handle FROM handle_aliases WHERE user_id = $1 ORDER BY handle`, a.ID)
	if err != nil {
		return nil, err
	}
	if a.Handle != "" {
		if _, err := tx.ExecContext(ctx, `INSERT INTO handle_aliases (handle, user_id) VALUES ($1, NULL)
			ON CONFLICT (handle) DO UPDATE SET user_id = NULL
			WHERE handle_aliases.user_id IS NULL OR handle_aliases.user_id = $2`, a.Handle, a.ID); err != nil {
			return nil, err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE handle_aliases SET user_id = NULL WHERE user_id = $1`, a.ID); err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM auth_tokens WHERE lower(email) = lower($1)`, a.Username)
	return aliases, err
}

func InsertIssuedAccount(ctx context.Context, tx *sql.Tx, name string) (string, error) {
	return InsertAccount(ctx, tx, name, "admin", "issued")
}

func InsertAccount(ctx context.Context, tx *sql.Tx, name, source, method string) (string, error) {
	var id string
	err := tx.QueryRowContext(ctx, `INSERT INTO users (username, signup_source, signup_method) VALUES ($1, $2, $3) ON CONFLICT (username) DO NOTHING RETURNING id`, name, source, method).Scan(&id)
	return id, err
}
func LockReissueAccount(ctx context.Context, tx *sql.Tx, id string) (model.User, error) {
	var u model.User
	err := tx.QueryRowContext(ctx, `SELECT id, username, handle, is_admin, suspended_at IS NOT NULL FROM users WHERE id::text=$1 FOR UPDATE`, id).Scan(&u.ID, &u.Username, &u.Handle, &u.IsAdmin, &u.Suspended)
	return u, err
}
func LockProfile(ctx context.Context, tx *sql.Tx, id string) (sql.NullString, error) {
	var h sql.NullString
	err := tx.QueryRowContext(ctx, "SELECT handle FROM users WHERE id=$1 FOR UPDATE", id).Scan(&h)
	return h, err
}
func SetUnpublishedHandle(ctx context.Context, tx *sql.Tx, id, handle string) error {
	_, err := tx.ExecContext(ctx, "UPDATE users SET handle=$2, handle_changed_at=now() WHERE id=$1", id, handle)
	return err
}
func SetDisplayName(ctx context.Context, tx *sql.Tx, id, name string) error {
	_, err := tx.ExecContext(ctx, "UPDATE users SET display_name=NULLIF($2,'') WHERE id=$1", id, name)
	return err
}

// IsUniqueConstraintViolation retains the transport's typed PostgreSQL check.
func IsUniqueConstraintViolation(err error) bool {
	var pgerr *pq.Error
	return errors.As(err, &pgerr) && pgerr.Code == "23505"
}
