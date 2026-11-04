package accounts

import (
	"context"

	"github.com/vsriram/simple-host/internal/repository/sqlkit"
)

// RecordVisitorSite records the last site used by a grant's visitor tools.
func RecordVisitorSite(ctx context.Context, q sqlkit.Querier, address, grantID, userID string) error {
	_, err := q.ExecContext(ctx, `UPDATE oauth_grants SET last_visitor_site=jsonb_build_object('address',$1::text,'at',now()), last_used_at=now() WHERE id=$2 AND user_id=$3`, address, grantID, userID)
	return err
}

// VisitorAccountExists reports whether a structural identifier names an account.
func VisitorAccountExists(ctx context.Context, q sqlkit.Querier, id string) (bool, error) {
	var exists bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=$1)`, id).Scan(&exists)
	return exists, err
}
