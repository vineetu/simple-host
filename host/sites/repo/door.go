package repo

import (
	"context"
	"database/sql"

	"github.com/lib/pq"
)

// The sites table is read and written only in this package; the other
// modules call these (with GetSiteByID, GetSiteByUser, ListSitesByUser,
// GetSiteOwner). Products reach them through host/sites/product.go.

// SitesByIDs loads the id, account, name and Deleted of these sites (live
// and Recently deleted) in one query, keyed by id. Unknown ids are left out.
func SitesByIDs(ctx context.Context, q Querier, ids []string) (map[string]Site, error) {
	out := make(map[string]Site, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := q.QueryContext(ctx, `SELECT id::text, COALESCE(user_id::text, ''), name, deleted_at IS NOT NULL
		FROM sites WHERE id = ANY($1::uuid[])`, pq.Array(ids))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var s Site
		if err := rows.Scan(&s.ID, &s.UserID, &s.Name, &s.Deleted); err != nil {
			return nil, err
		}
		out[s.ID] = s
	}
	return out, rows.Err()
}

// LockSite locks a site's row inside tx (live or deleted); sql.ErrNoRows
// when there is none.
func LockSite(ctx context.Context, tx *sql.Tx, siteID string) error {
	var id string
	return tx.QueryRowContext(ctx, `SELECT id FROM sites WHERE id = $1 FOR UPDATE`, siteID).Scan(&id)
}

// Reads and writes on sites for host/ops reports and the summary backfill.

// AccountSiteIDs lists every site the account holds, live and Recently deleted.
func AccountSiteIDs(ctx context.Context, q Querier, userID string) ([]string, error) {
	return siteIDs(q.QueryContext(ctx, `SELECT id::text FROM sites WHERE user_id = $1`, userID))
}

// AccountsWithSites is the subset of ids that hold at least one site (live or deleted).
func AccountsWithSites(ctx context.Context, q Querier, ids []string) (map[string]bool, error) {
	out := map[string]bool{}
	got, err := siteIDs(q.QueryContext(ctx, `SELECT DISTINCT user_id::text FROM sites WHERE user_id = ANY($1::uuid[])`, pq.Array(ids)))
	for _, id := range got {
		out[id] = true
	}
	return out, err
}

// SiteHolderCount counts the accounts holding a site, leaving out except.
func SiteHolderCount(ctx context.Context, q Querier, except []string) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, `SELECT count(DISTINCT user_id) FROM sites WHERE NOT (user_id::text = ANY($1))`, pq.Array(except)).Scan(&n)
	return n, err
}

// SitesCreatedPerDay counts sites created per UTC day in [from, to], deleted ones included.
func SitesCreatedPerDay(ctx context.Context, q Querier, from, to string) (map[string]int64, error) {
	rows, err := q.QueryContext(ctx, `SELECT created_at::date::text, COUNT(*) FROM sites WHERE created_at >= $1::date AND created_at < $2::date + 1 GROUP BY 1`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var day string
		var n int64
		if err := rows.Scan(&day, &n); err != nil {
			return nil, err
		}
		out[day] = n
	}
	return out, rows.Err()
}

// ClearKeepVersions drops a site's fixed version retention back to the default.
func ClearKeepVersions(ctx context.Context, tx *sql.Tx, id string) (sql.Result, error) {
	return tx.ExecContext(ctx, `UPDATE sites SET keep_versions=0 WHERE id=$1`, id)
}

// SitesWithoutSummary lists live sites with no summary yet, oldest first, at most limit (0 = all).
func SitesWithoutSummary(ctx context.Context, q Querier, limit int) ([]Site, error) {
	rows, err := q.QueryContext(ctx, `SELECT id, user_id, name, active_version FROM sites WHERE deleted_at IS NULL AND summary_title IS NULL ORDER BY created_at, id LIMIT NULLIF($1, 0)`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Site
	for rows.Next() {
		var s Site
		if err := rows.Scan(&s.ID, &s.UserID, &s.Name, &s.ActiveVersion); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func siteIDs(rows *sql.Rows, err error) ([]string, error) {
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

// AllSiteAddresses is every site row (Recently deleted included) with its
// id, owner, name and custom domain: what log attribution maps hosts with.
func AllSiteAddresses(ctx context.Context, q *sql.DB) ([]Site, error) {
	rows, err := q.QueryContext(ctx, `SELECT id, user_id, name, custom_domain FROM sites`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Site
	for rows.Next() {
		var s Site
		if err := rows.Scan(&s.ID, &s.UserID, &s.Name, &s.CustomDomain); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// OldestLiveSiteByName maps each live site name to the oldest live site of
// that name (the retired per-name host).
func OldestLiveSiteByName(ctx context.Context, q *sql.DB) (map[string]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT DISTINCT ON (name) id, name FROM sites
		WHERE deleted_at IS NULL ORDER BY name, created_at ASC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		out[name] = id
	}
	return out, rows.Err()
}
