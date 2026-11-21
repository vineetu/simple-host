package repo

import (
	"context"
	"database/sql"

	"github.com/lib/pq"
	"github.com/vsriram/simple-host/host/model"
)

type ConnectedApp = model.ConnectedApp

type Activation = model.Activation

// ActivationFor loads Activation for each of userIDs, in four grouped
// queries (activationQueries). An account with nothing to show has a zero
// Activation.
func ActivationFor(ctx context.Context, q *sql.DB, userIDs []string) (map[string]*Activation, error) {
	out := make(map[string]*Activation, len(userIDs))
	for _, id := range userIDs {
		out[id] = &Activation{}
	}
	if len(userIDs) == 0 {
		return out, nil
	}
	for _, aq := range activationQueries {
		if err := activationRows(ctx, q, aq, pq.Array(userIDs), out); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// activationQuery is one grouped query by account: scan reads a row's
// account ID and returns how it fills that account's Activation.
type activationQuery struct {
	sql  string
	scan func(*sql.Rows) (string, func(*Activation), error)
}

var activationQueries = []activationQuery{
	{`
		SELECT g.user_id, c.client_name, MIN(g.created_at)
		  FROM oauth_grants g JOIN oauth_clients c ON c.client_id = g.client_id
		 WHERE g.user_id = ANY($1::uuid[])
		 GROUP BY g.user_id, c.client_name
		 ORDER BY MIN(g.created_at)`, func(rows *sql.Rows) (string, func(*Activation), error) {
		var id string
		var app ConnectedApp
		err := rows.Scan(&id, &app.Name, &app.First)
		return id, func(a *Activation) { a.Apps = append(a.Apps, app) }, err
	}},
	{`
		SELECT s.user_id, COUNT(*), MIN(s.created_at)
		  FROM sites s
		 WHERE s.user_id = ANY($1::uuid[])
		   AND EXISTS (SELECT 1 FROM versions v WHERE v.site_id = s.id AND v.status IN ('active', 'ready'))
		 GROUP BY s.user_id`, func(rows *sql.Rows) (string, func(*Activation), error) {
		var id string
		var n int
		var first sql.NullTime
		err := rows.Scan(&id, &n, &first)
		return id, func(a *Activation) { a.SitesPublished, a.FirstPublish = n, first }, err
	}},
	{`
		SELECT s.user_id, COALESCE(SUM(h.views), 0)
		  FROM site_view_hourly h JOIN sites s ON s.id = h.site_id
		 WHERE s.user_id = ANY($1::uuid[]) AND h.class = 'person'
		 GROUP BY s.user_id`, func(rows *sql.Rows) (string, func(*Activation), error) {
		var id string
		var n int64
		err := rows.Scan(&id, &n)
		return id, func(a *Activation) { a.Visits = n }, err
	}},
	// Last active: the latest use of any key (the dashboard signs in with
	// one), of any connected app, or of a deploy.
	{`
		SELECT user_id, MAX(at) FROM (
		  SELECT user_id, last_used_at AS at FROM api_keys WHERE user_id = ANY($1::uuid[])
		  UNION ALL
		  SELECT user_id, last_used_at FROM oauth_grants WHERE user_id = ANY($1::uuid[])
		  UNION ALL
		  SELECT s.user_id, v.created_at FROM versions v JOIN sites s ON s.id = v.site_id WHERE s.user_id = ANY($1::uuid[])
		) x WHERE at IS NOT NULL GROUP BY user_id`, func(rows *sql.Rows) (string, func(*Activation), error) {
		var id string
		var at sql.NullTime
		err := rows.Scan(&id, &at)
		return id, func(a *Activation) { a.LastActive = at }, err
	}},
}

// activationRows runs one activation query and fills out from its rows.
func activationRows(ctx context.Context, q *sql.DB, aq activationQuery, ids any, out map[string]*Activation) error {
	rows, err := q.QueryContext(ctx, aq.sql, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		id, fill, err := aq.scan(rows)
		if err != nil {
			return err
		}
		if a := out[id]; a != nil {
			fill(a)
		}
	}
	return rows.Err()
}
