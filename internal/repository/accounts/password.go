package accounts

import (
	"context"
)

func PasswordAccount(ctx context.Context, q Querier, login string) (User, string, bool, error) {
	var id, hash string
	var must bool
	err := q.QueryRowContext(ctx, `SELECT u.id, COALESCE(p.password_hash, ''), COALESCE(p.must_change, false)
 FROM users u LEFT JOIN account_passwords p ON p.user_id=u.id
 WHERE lower(u.username)=$1`, login).Scan(&id, &hash, &must)
	if err != nil {
		return User{}, "", false, err
	}
	user, err := GetUserByID(ctx, q, id)
	return user, hash, must, err
}

// PasswordHashByID loads the stored hash for an account by id (unlocked),
// for a caller that already knows the account, such as changing your own
// password. sql.ErrNoRows: the account has no password row.
func PasswordHashByID(ctx context.Context, q Querier, id string) (string, error) {
	var hash string
	err := q.QueryRowContext(ctx, `SELECT password_hash FROM account_passwords WHERE user_id=$1`, id).Scan(&hash)
	return hash, err
}
func LockPassword(ctx context.Context, q Querier, id string) (string, bool, error) {
	var hash string
	var must bool
	err := q.QueryRowContext(ctx, `SELECT password_hash, must_change FROM account_passwords WHERE user_id=$1 FOR UPDATE`, id).Scan(&hash, &must)
	return hash, must, err
}
func SetPassword(ctx context.Context, q Querier, id, hash string, must bool) error {
	_, err := q.ExecContext(ctx, `INSERT INTO account_passwords(user_id,password_hash,must_change) VALUES($1,$2,$3)
 ON CONFLICT(user_id) DO UPDATE SET password_hash=$2,must_change=$3,updated_at=now()`, id, hash, must)
	return err
}
