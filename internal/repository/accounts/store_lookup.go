package accounts

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/vsriram/simple-host/internal/model"
)

func GetUserByHandle(ctx context.Context, db *sql.DB, handle string) (model.User, error) {
	const query = `
		SELECT id, username, is_admin, created_at, handle, display_name
		FROM users
		WHERE handle = $1
	`

	var user model.User
	err := db.QueryRowContext(ctx, query, handle).Scan(
		&user.ID,
		&user.Username,
		&user.IsAdmin,
		&user.CreatedAt,
		&user.Handle,
		&user.DisplayName,
	)
	return user, err
}
func ResolveHandleAlias(ctx context.Context, q Querier, old string) (string, error) {
	var handle string
	err := q.QueryRowContext(ctx, `
		SELECT u.handle FROM handle_aliases a JOIN users u ON u.id = a.user_id
		WHERE a.handle = $1 AND u.handle IS NOT NULL`, strings.ToLower(old)).Scan(&handle)
	return handle, err
}
func GetUserByHandleOrAlias(ctx context.Context, database *sql.DB, handle string) (model.User, error) {
	u, err := GetUserByHandle(ctx, database, handle)
	if !errors.Is(err, sql.ErrNoRows) {
		return u, err
	}
	current, aerr := ResolveHandleAlias(ctx, database, handle)
	if aerr != nil {
		return model.User{}, err
	}
	return GetUserByHandle(ctx, database, current)
}
