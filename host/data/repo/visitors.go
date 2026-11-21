package repo

import (
	"context"
	"database/sql"

	"github.com/vsriram/simple-host/host/model"
	sitesrepo "github.com/vsriram/simple-host/host/sites/repo"
)

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

// ListChangesElsewhere returns the changes this person made, signed in, to
// saved data on sites that are not theirs, oldest first.
func ListChangesElsewhere(ctx context.Context, database *sql.DB, userID string) ([]model.ChangeElsewhere, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT h.site_id::text, h.kind, h.name, h.item_id, h.op, h.created_at
		  FROM data_history h
		 WHERE h.actor_id = $1
		 ORDER BY h.created_at, h.id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var all []model.ChangeElsewhere
	var siteIDs []string
	for rows.Next() {
		var c model.ChangeElsewhere
		var item sql.NullInt64
		if err := rows.Scan(&c.SiteID, &c.Kind, &c.Name, &item, &c.Op, &c.At); err != nil {
			return nil, err
		}
		if item.Valid {
			v := item.Int64
			c.ItemID = &v
		}
		all = append(all, c)
		siteIDs = append(siteIDs, c.SiteID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sites, err := sitesrepo.SitesByIDs(ctx, database, siteIDs)
	if err != nil {
		return nil, err
	}
	var out []model.ChangeElsewhere
	for _, c := range all {
		if s, ok := sites[c.SiteID]; ok && s.UserID != userID {
			out = append(out, c)
		}
	}
	return out, nil
}
