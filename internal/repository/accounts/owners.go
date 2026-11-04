package accounts

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/vsriram/simple-host/internal/model"
)

func listUsers(ctx context.Context, db *sql.DB, withSites bool) ([]model.User, error) {
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
func ListAllUsers(ctx context.Context, db *sql.DB) ([]model.User, error) {
	return listUsers(ctx, db, false)
}
func ListSiteOwners(ctx context.Context, db *sql.DB) ([]model.User, error) {
	return listUsers(ctx, db, true)
}

// GetUserByHandle looks up a user by their URL-safe handle.
