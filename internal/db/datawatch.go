package db

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"time"
)

// ---- idempotency keys --------------------------------------------------------

// IdempotentResponse is the first answer to a write sent with an
// Idempotency-Key, replayed for a retry. Status 0: that first request is still
// running.
type IdempotentResponse struct {
	Status      int
	ContentType string
	ETag        string
	Body        []byte
}

// ClaimIdempotencyKey reserves scope for this request. claimed=false with a
// stored answer means a retry: replay it (Status 0: still running). A
// reservation left by a request that never finished is taken over after
// stale.
func ClaimIdempotencyKey(ctx context.Context, database *sql.DB, scope []byte, stale time.Duration) (bool, IdempotentResponse, error) {
	var got []byte
	err := database.QueryRowContext(ctx, `
		INSERT INTO idempotency_keys (scope) VALUES ($1)
		ON CONFLICT (scope) DO UPDATE SET created_at = now()
		 WHERE idempotency_keys.status = 0 AND idempotency_keys.created_at < now() - make_interval(secs => $2)
		RETURNING scope`, scope, stale.Seconds()).Scan(&got)
	if err == nil {
		return true, IdempotentResponse{}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, IdempotentResponse{}, err
	}
	var r IdempotentResponse
	var ct, etag sql.NullString
	err = database.QueryRowContext(ctx, `
		SELECT status, content_type, etag, body FROM idempotency_keys WHERE scope = $1`, scope).Scan(&r.Status, &ct, &etag, &r.Body)
	r.ContentType, r.ETag = ct.String, etag.String
	return false, r, err
}

// SaveIdempotentResponse keeps the first answer for later retries.
func SaveIdempotentResponse(ctx context.Context, database *sql.DB, scope []byte, r IdempotentResponse) error {
	_, err := database.ExecContext(ctx, `
		UPDATE idempotency_keys SET status = $2, content_type = $3, etag = $4, body = $5 WHERE scope = $1`,
		scope, r.Status, r.ContentType, r.ETag, r.Body)
	return err
}

// ReleaseIdempotencyKey drops a reservation whose request did not succeed, so
// a retry runs again.
func ReleaseIdempotencyKey(ctx context.Context, database *sql.DB, scope []byte) error {
	_, err := database.ExecContext(ctx, `DELETE FROM idempotency_keys WHERE scope = $1 AND status = 0`, scope)
	return err
}

// PurgeIdempotencyKeys forgets answers older than keep.
func PurgeIdempotencyKeys(ctx context.Context, database *sql.DB, keep time.Duration) (int64, error) {
	res, err := database.ExecContext(ctx, `DELETE FROM idempotency_keys WHERE created_at < now() - make_interval(secs => $1)`, keep.Seconds())
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
		 WHERE w.day > (now() AT TIME ZONE 'UTC')::date - $1
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
