package accounts

import (
	"context"
	"database/sql"
)

func sweepSSOCount(ctx context.Context, q Querier) (int64, error) {
	var deleted int64
	for _, query := range []string{`DELETE FROM sso_hops WHERE expires_at<now()`, `DELETE FROM sso_signouts WHERE expires_at<now()`, `DELETE FROM sso_passes WHERE expires_at<now() OR idle_expires_at<now()`} {
		result, err := q.ExecContext(ctx, query)
		if err != nil {
			return deleted, err
		}
		n, err := result.RowsAffected()
		deleted += n
		if err != nil {
			return deleted, err
		}
	}
	return deleted, nil
}

func SweepVisitorAuthCount(ctx context.Context, database *sql.DB) (int64, error) {
	deleted, err := sweepSSOCount(ctx, database)
	if err != nil {
		return deleted, err
	}
	for _, query := range []string{
		`DELETE FROM oauth_states WHERE expires_at < now()`,
		`DELETE FROM visitor_establish_tokens WHERE expires_at < now()`,
		`DELETE FROM auth_tokens WHERE expires_at < now() - interval '1 day'`,
		`DELETE FROM visitor_sessions WHERE expires_at < now() OR idle_expires_at < now()`,
	} {
		result, err := database.ExecContext(ctx, query)
		if err != nil {
			return deleted, err
		}
		n, err := result.RowsAffected()
		deleted += n
		if err != nil {
			return deleted, err
		}
	}
	return deleted, nil
}
