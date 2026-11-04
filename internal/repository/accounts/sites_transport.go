package accounts

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/vsriram/simple-host/internal/model"
)

func GetShowcaseBio(ctx context.Context, q Querier, userID string) (string, error) {
	var bio string
	err := q.QueryRowContext(ctx, `SELECT showcase_bio FROM users WHERE id=$1`, userID).Scan(&bio)
	return bio, err
}

func SetShowcaseBio(ctx context.Context, q Querier, userID, bio string) error {
	res, err := q.ExecContext(ctx, `UPDATE users SET showcase_bio=$2 WHERE id=$1`, userID, bio)
	return oneRow(res, err)
}

func HandleAliasesByUser(ctx context.Context, database *sql.DB) (map[string][]string, error) {
	rows, err := database.QueryContext(ctx, `SELECT user_id, handle FROM handle_aliases WHERE user_id IS NOT NULL ORDER BY created_at, handle`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var id, h string
		if err := rows.Scan(&id, &h); err != nil {
			return nil, err
		}
		out[id] = append(out[id], h)
	}
	return out, rows.Err()
}

func oneRow(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func ListUsers(ctx context.Context, db *sql.DB, withSites bool) ([]model.User, error) {
	filter := ""
	if withSites {
		filter = " WHERE EXISTS (SELECT 1 FROM sites s WHERE s.user_id=users.id)"
	}
	rows, err := db.QueryContext(ctx, `
		SELECT id, username, is_admin, created_at, COALESCE(handle, ''), display_name,
		       suspended_at IS NOT NULL, COALESCE(suspended_reason, ''),
		       COALESCE(signup_source, ''), COALESCE(signup_agent, ''), COALESCE(signup_method, ''), signup_inferred,
		       signup_detail
		FROM users`+filter+`
		ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []model.User
	for rows.Next() {
		var u model.User
		var handle string
		var detail []byte
		if err := rows.Scan(&u.ID, &u.Username, &u.IsAdmin, &u.CreatedAt, &handle, &u.DisplayName, &u.Suspended, &u.SuspendedReason,
			&u.Signup.Source, &u.Signup.Agent, &u.Signup.Method, &u.Signup.Inferred, &detail); err != nil {
			return nil, err
		}
		if len(detail) > 0 {
			var d model.SignupDetail
			if json.Unmarshal(detail, &d) == nil {
				u.Signup.Detail = &d
			}
		}
		if handle != "" {
			u.Handle = sql.NullString{String: handle, Valid: true}
		}
		out = append(out, u)
	}
	return out, rows.Err()
}
