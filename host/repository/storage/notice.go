package storage

import (
	"context"
	"database/sql"

	"github.com/lib/pq"
)

// NoticeAccount reads the address of the account that holds a site.
func NoticeAccount(ctx context.Context, db *sql.DB, id string) (address string, err error) {
	err = db.QueryRowContext(ctx, `SELECT username FROM users WHERE id=$1`, id).Scan(&address)
	return
}

type LegacyNoticeSite struct {
	ID, Name, OwnerID, Account, Address string
	State, Collections, Declared, Calls bool
}

// LegacyNoticeSites includes stored data and calls with no stored rows. It does
// not exclude accounts whose storage resources have already been converted.
func LegacyNoticeSites(ctx context.Context, db *sql.DB, called []string) ([]LegacyNoticeSite, error) {
	if called == nil {
		called = []string{}
	}
	rows, err := db.QueryContext(ctx, `SELECT s.id,s.name,s.user_id,COALESCE(u.handle,''),u.username,
 s.state IS NOT NULL AND s.state <> 'null'::jsonb,
 EXISTS(SELECT 1 FROM collection_items c WHERE c.site_id=s.id),
 EXISTS(SELECT 1 FROM collection_settings c WHERE c.site_id=s.id),
 s.id::text = ANY($1)
 FROM sites s JOIN users u ON u.id=s.user_id
 WHERE (s.state IS NOT NULL AND s.state <> 'null'::jsonb)
 OR EXISTS(SELECT 1 FROM collection_items c WHERE c.site_id=s.id)
 OR EXISTS(SELECT 1 FROM collection_settings c WHERE c.site_id=s.id)
 OR s.id::text = ANY($1) ORDER BY u.handle NULLS LAST,s.user_id,s.name,s.id`, pq.Array(called))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LegacyNoticeSite{}
	for rows.Next() {
		var s LegacyNoticeSite
		if err := rows.Scan(&s.ID, &s.Name, &s.OwnerID, &s.Account, &s.Address, &s.State, &s.Collections, &s.Declared, &s.Calls); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// PresetNoticeCount uses exactly the recipient query, including event-held resources.
func PresetNoticeCount(ctx context.Context, db *sql.DB) (int, error) {
	resources, err := ConvertScope(ctx, db, nil)
	return len(resources), err
}
