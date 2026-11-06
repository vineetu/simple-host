package accounts

import (
	"context"
	"github.com/vsriram/simple-host/internal/repository/sqlkit"
)

// LockDisplayName reads the account name while serializing first-name fills.
func LockDisplayName(ctx context.Context, q sqlkit.Querier, userID string) (string, error) {
	var name string
	err := q.QueryRowContext(ctx, `SELECT COALESCE(display_name,'') FROM users WHERE id=$1 FOR UPDATE`, userID).Scan(&name)
	return name, err
}

// FillEmptyDisplayName preserves a name already chosen on the account.
func FillEmptyDisplayName(ctx context.Context, q sqlkit.Querier, userID, name string) error {
	_, err := q.ExecContext(ctx, `UPDATE users SET display_name=$2 WHERE id=$1 AND COALESCE(btrim(display_name),'')=''`, userID, name)
	return err
}
