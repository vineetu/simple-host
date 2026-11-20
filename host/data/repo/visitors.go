package repo

import (
	"context"
	"database/sql"
)

// CandidateVisitorIDsForEmail returns the distinct visitor ids, among
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

// DistinctFileWriters returns the visitor_id values with at least one
// stored file in this site's files storage (every resource), excluding the
// owner's own uploads (writer_id ”).
func DistinctFileWriters(ctx context.Context, db *sql.DB, siteID string) ([]string, error) {
	return distinctWriters(ctx, db, `SELECT DISTINCT writer_id FROM site_storage_files WHERE site_id = $1 AND writer_id <> ''`, siteID)
}

func distinctWriters(ctx context.Context, db *sql.DB, query, siteID string) ([]string, error) {
	rows, err := db.QueryContext(ctx, query, siteID)
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

// CountVisitorAIWrite counts an attempted write, returning ErrNoRows at the cap.
func CountVisitorAIWrite(ctx context.Context, db *sql.DB, userID, siteID string, cap int) error {
	var count int64
	return db.QueryRowContext(ctx, `INSERT INTO visitor_ai_daily(user_id,site_id,day,writes) VALUES($1,$2,(now() AT TIME ZONE 'UTC')::date,1) ON CONFLICT(user_id,site_id,day) DO UPDATE SET writes=visitor_ai_daily.writes+1 WHERE visitor_ai_daily.writes<$3 RETURNING writes`, userID, siteID, cap).Scan(&count)
}

// RecordVisitorAIWrite appends a non-restorable visitor history marker.
func RecordVisitorAIWrite(ctx context.Context, db *sql.DB, siteID, resource, op, userID, app string) error {
	_, err := db.ExecContext(ctx, `INSERT INTO data_history(site_id,kind,name,op,actor_id,actor_kind,actor_app) VALUES($1,'storage',$2,$3,$4,'visitor',$5)`, siteID, resource, op, userID, app)
	return err
}

// PruneVisitorAIWrites deletes data_history rows older than 30 days, in
// batches along idx_data_history_created, and returns how many went.
func PruneVisitorAIWrites(ctx context.Context, db *sql.DB) (int64, error) {
	var total int64
	for {
		res, err := db.ExecContext(ctx, `DELETE FROM data_history WHERE id IN (SELECT id FROM data_history WHERE created_at < now() - interval '30 days' LIMIT 5000)`)
		if err != nil {
			return total, err
		}
		n, _ := res.RowsAffected()
		total += n
		if n < 5000 {
			return total, nil
		}
	}
}
