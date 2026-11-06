package storage

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
// internal/handler/privatecollections.go:visitorEmail, the canonical,
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

// DistinctKVWriters returns the visitor_id values with at least one key in
// this site's KV storage (every resource), excluding the owner's own keys
// (writer_id ”).
func DistinctKVWriters(ctx context.Context, db *sql.DB, siteID string) ([]string, error) {
	return distinctWriters(ctx, db, `SELECT DISTINCT writer_id FROM site_storage_kv WHERE site_id = $1 AND writer_id <> ''`, siteID)
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
