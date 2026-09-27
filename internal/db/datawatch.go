package db

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"time"
)

// ---- idempotency keys --------------------------------------------------------

// IdempotentResponse is what is kept of the first answer to a write sent with
// an Idempotency-Key, to answer a retry: its status, the ETag and the new
// version or item id (Ref), and a hash of the request body, so the same key
// with another body is refused. Never the response body. Status 0: that first
// request is still running.
type IdempotentResponse struct {
	Status   int
	ETag     string
	Ref      int64
	BodyHash []byte
}

// ClaimIdempotencyKey reserves scope for this request on siteID. claimed=false
// with a stored answer means a retry (Status 0: still running). A reservation
// left by a request that never finished is taken over after stale.
func ClaimIdempotencyKey(ctx context.Context, database *sql.DB, scope []byte, siteID string, bodyHash []byte, stale time.Duration) (bool, IdempotentResponse, error) {
	var got []byte
	err := database.QueryRowContext(ctx, `
		INSERT INTO idempotency_keys (scope, site_id, body_hash) VALUES ($1, $2, $3)
		ON CONFLICT (scope) DO UPDATE SET created_at = now(), body_hash = EXCLUDED.body_hash
		 WHERE idempotency_keys.status = 0 AND idempotency_keys.created_at < now() - make_interval(secs => $4)
		RETURNING scope`, scope, siteID, bodyHash, stale.Seconds()).Scan(&got)
	if err == nil {
		return true, IdempotentResponse{}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, IdempotentResponse{}, err
	}
	var r IdempotentResponse
	var etag sql.NullString
	var ref sql.NullInt64
	err = database.QueryRowContext(ctx, `
		SELECT status, etag, ref, body_hash FROM idempotency_keys WHERE scope = $1`, scope).Scan(&r.Status, &etag, &ref, &r.BodyHash)
	r.ETag, r.Ref = etag.String, ref.Int64
	return false, r, err
}

// SaveIdempotentResponse keeps the first answer's status, ETag and ref.
func SaveIdempotentResponse(ctx context.Context, database *sql.DB, scope []byte, status int, etag string, ref int64) error {
	_, err := database.ExecContext(ctx, `
		UPDATE idempotency_keys SET status = $2, etag = $3, ref = $4 WHERE scope = $1`,
		scope, status, nullIfEmpty(etag), ref)
	return err
}

// ReleaseIdempotencyKey drops a reservation whose request did not succeed, so
// a retry runs again.
func ReleaseIdempotencyKey(ctx context.Context, database *sql.DB, scope []byte) error {
	_, err := database.ExecContext(ctx, `DELETE FROM idempotency_keys WHERE scope = $1 AND status = 0`, scope)
	return err
}

// PurgeIdempotencyKeys forgets answers older than keep, and on any site
// holding more than maxPerSite, the oldest past that.
func PurgeIdempotencyKeys(ctx context.Context, database *sql.DB, keep time.Duration, maxPerSite int) (int64, error) {
	res, err := database.ExecContext(ctx, `DELETE FROM idempotency_keys WHERE created_at < now() - make_interval(secs => $1)`, keep.Seconds())
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	res, err = database.ExecContext(ctx, `
		DELETE FROM idempotency_keys WHERE scope IN (
			SELECT scope FROM (
				SELECT scope, row_number() OVER (PARTITION BY site_id ORDER BY created_at DESC, scope) AS rn
				  FROM idempotency_keys
				 WHERE site_id IN (SELECT site_id FROM idempotency_keys GROUP BY site_id HAVING count(*) > $1)
			) t WHERE rn > $1)`, maxPerSite)
	if err != nil {
		return n, err
	}
	m, _ := res.RowsAffected()
	return n + m, nil
}

// PurgeDataWatch removes watch counts older than keepDays.
func PurgeDataWatch(ctx context.Context, database *sql.DB, keepDays int) (int64, error) {
	res, err := database.ExecContext(ctx, `DELETE FROM data_watch WHERE day < (now() AT TIME ZONE 'UTC')::date - $1::int`, keepDays)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ---- the watch ---------------------------------------------------------------

// BumpDataWatch counts n uses of metric on siteID today (UTC).
func BumpDataWatch(ctx context.Context, database *sql.DB, siteID, metric string, n int64) error {
	_, err := database.ExecContext(ctx, `
		INSERT INTO data_watch (day, site_id, metric, count, last_at)
		VALUES ((now() AT TIME ZONE 'UTC')::date, $1, $2, $3, now())
		ON CONFLICT (day, site_id, metric) DO UPDATE SET count = data_watch.count + EXCLUDED.count, last_at = now()`, siteID, metric, n)
	return err
}

// DataWatchSite is one site's counts over the window.
type DataWatchSite struct {
	SiteID  string           `json:"site_id"`
	Site    string           `json:"site"`
	Handle  string           `json:"handle"`
	Metrics map[string]int64 `json:"metrics"`
	LastAt  time.Time        `json:"last_at"`
}

// ListDataWatch returns every site with a count in the last days days (UTC),
// most recent first, and the first day anything was ever counted (zero when
// nothing has been).
func ListDataWatch(ctx context.Context, database *sql.DB, days int) ([]DataWatchSite, time.Time, error) {
	var first sql.NullTime
	if err := database.QueryRowContext(ctx, `SELECT min(day)::timestamptz FROM data_watch`).Scan(&first); err != nil {
		return nil, time.Time{}, err
	}
	rows, err := database.QueryContext(ctx, `
		SELECT w.site_id::text, s.name, COALESCE(u.handle, ''), w.metric, sum(w.count), max(w.last_at)
		  FROM data_watch w
		  JOIN sites s ON s.id = w.site_id
		  LEFT JOIN users u ON u.id = s.user_id
		 WHERE w.day > (now() AT TIME ZONE 'UTC')::date - $1::int
		 GROUP BY w.site_id, s.name, u.handle, w.metric`, days)
	if err != nil {
		return nil, time.Time{}, err
	}
	defer rows.Close()
	bySite := map[string]*DataWatchSite{}
	var order []string
	for rows.Next() {
		var id, name, handle, metric string
		var n int64
		var last time.Time
		if err := rows.Scan(&id, &name, &handle, &metric, &n, &last); err != nil {
			return nil, time.Time{}, err
		}
		s := bySite[id]
		if s == nil {
			s = &DataWatchSite{SiteID: id, Site: name, Handle: handle, Metrics: map[string]int64{}}
			bySite[id] = s
			order = append(order, id)
		}
		s.Metrics[metric] += n
		if last.After(s.LastAt) {
			s.LastAt = last
		}
	}
	if err := rows.Err(); err != nil {
		return nil, time.Time{}, err
	}
	out := make([]DataWatchSite, 0, len(order))
	for _, id := range order {
		out = append(out, *bySite[id])
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastAt.After(out[j].LastAt) })
	return out, first.Time, nil
}
