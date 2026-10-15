package db

import (
	"context"
	"database/sql"
	"time"

	"github.com/lib/pq"
)

// ConnectedApp is one AI app an account connected through the /mcp consent
// page, and when it first did.
type ConnectedApp struct {
	Name  string    `json:"name"`
	First time.Time `json:"first_at"`
}

// Activation is what an account did after joining, for the admin page's
// sign-ups by source drill-down. Apps come from oauth_grants (disconnecting an
// app deletes its grant, so a disconnected app is gone from here); sites count
// every site that ever had a published version, including deleted ones not
// yet purged; Visits are page views by people (not bots) to all of them.
type Activation struct {
	Apps           []ConnectedApp
	SitesPublished int
	FirstPublish   sql.NullTime
	Visits         int64
	LastActive     sql.NullTime
}

// ActivationFor loads Activation for each of userIDs, in four grouped
// queries. An account with nothing to show has a zero Activation.
func ActivationFor(ctx context.Context, q *sql.DB, userIDs []string) (map[string]*Activation, error) {
	out := make(map[string]*Activation, len(userIDs))
	for _, id := range userIDs {
		out[id] = &Activation{}
	}
	if len(userIDs) == 0 {
		return out, nil
	}
	ids := pq.Array(userIDs)

	rows, err := q.QueryContext(ctx, `
		SELECT g.user_id, c.client_name, MIN(g.created_at)
		  FROM oauth_grants g JOIN oauth_clients c ON c.client_id = g.client_id
		 WHERE g.user_id = ANY($1::uuid[])
		 GROUP BY g.user_id, c.client_name
		 ORDER BY MIN(g.created_at)`, ids)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		var app ConnectedApp
		if err := rows.Scan(&id, &app.Name, &app.First); err != nil {
			rows.Close()
			return nil, err
		}
		if a := out[id]; a != nil {
			a.Apps = append(a.Apps, app)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = q.QueryContext(ctx, `
		SELECT s.user_id, COUNT(*), MIN(s.created_at)
		  FROM sites s
		 WHERE s.user_id = ANY($1::uuid[])
		   AND EXISTS (SELECT 1 FROM versions v WHERE v.site_id = s.id AND v.status IN ('active', 'ready'))
		 GROUP BY s.user_id`, ids)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		var n int
		var first sql.NullTime
		if err := rows.Scan(&id, &n, &first); err != nil {
			rows.Close()
			return nil, err
		}
		if a := out[id]; a != nil {
			a.SitesPublished, a.FirstPublish = n, first
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = q.QueryContext(ctx, `
		SELECT s.user_id, COALESCE(SUM(h.views), 0)
		  FROM site_view_hourly h JOIN sites s ON s.id = h.site_id
		 WHERE s.user_id = ANY($1::uuid[]) AND h.class = 'person'
		 GROUP BY s.user_id`, ids)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		var n int64
		if err := rows.Scan(&id, &n); err != nil {
			rows.Close()
			return nil, err
		}
		if a := out[id]; a != nil {
			a.Visits = n
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Last active: the latest use of any key (the dashboard signs in with
	// one), of any connected app, or of a deploy.
	rows, err = q.QueryContext(ctx, `
		SELECT user_id, MAX(at) FROM (
		  SELECT user_id, last_used_at AS at FROM api_keys WHERE user_id = ANY($1::uuid[])
		  UNION ALL
		  SELECT user_id, last_used_at FROM oauth_grants WHERE user_id = ANY($1::uuid[])
		  UNION ALL
		  SELECT s.user_id, v.created_at FROM versions v JOIN sites s ON s.id = v.site_id WHERE s.user_id = ANY($1::uuid[])
		) x WHERE at IS NOT NULL GROUP BY user_id`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var at sql.NullTime
		if err := rows.Scan(&id, &at); err != nil {
			return nil, err
		}
		if a := out[id]; a != nil {
			a.LastActive = at
		}
	}
	return out, rows.Err()
}
