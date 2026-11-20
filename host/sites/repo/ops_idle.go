package repo

import (
	"context"
	"database/sql"
	"time"
)

// Idle-site cleanup (owner decision 2026-09-27, simplified 2026-11-19: no emails; handler/idle.go).
//
// A site's last activity is the latest of: when it was created, its newest
// version, the last hour a real person (analytics class 'person') viewed it,
// its newest collection entry, the last change to its saved data or settings
// (sites.updated_at: state writes, restore, offline, visibility), and when its
// owner last chose "Keep it" or restored it. Since 2026-11-02 the owner's own
// visits from a marked device are filed as class 'owner' (analytics.go,
// ClassOwner) and still count as activity here, because a site its owner
// keeps opening is in use.
//
// Never considered: sites in Recently deleted, taken down, owned by a
// suspended or admin account, with the Keep flag, with a custom domain or a
// claimed <name>.<SITE_DOMAIN> (current, earlier or retired), taken offline
// by their owner (a deliberate choice to keep it, just not serve it), preview
// sites (they expire on their own), and the operator's exempt accounts
// (IdleExempt: IDLE_CLEANUP_EXEMPT_HANDLES, matched against each account's
// current handle and its old ones, so an exempt account that changes its
// handle stays exempt; the plugin reviewer account) and
// event accounts (users.event_account).

func queryIdleSites(ctx context.Context, database *sql.DB, ex IdleExempt, extra string, args ...any) ([]IdleSite, error) {
	all := append(idleExemptArgs(ex), args...)
	rows, err := database.QueryContext(ctx, `
		SELECT id, user_id, name, username, handle, last_activity FROM (
			SELECT s.id, s.user_id::text AS user_id, s.name, u.username, COALESCE(u.handle, '') AS handle,
			       `+idleLastActivity+` AS last_activity
			  FROM sites s JOIN users u ON u.id = s.user_id
			 WHERE s.deleted_at IS NULL AND `+idleExemptEligible(ex, 1)+`
		) x WHERE `+extra, all...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []IdleSite
	for rows.Next() {
		var s IdleSite
		if err := rows.Scan(&s.SiteID, &s.UserID, &s.Name, &s.OwnerEmail, &s.OwnerHandle, &s.LastActivity); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ListIdleSitesToRemove: eligible sites idle since before idleBefore, longest
// idle first, at most limit (0 = all).
func ListIdleSitesToRemove(ctx context.Context, database *sql.DB, ex IdleExempt, idleBefore time.Time, limit int) ([]IdleSite, error) {
	q := `last_activity < $3 ORDER BY last_activity`
	if limit > 0 {
		return queryIdleSites(ctx, database, ex, q+` LIMIT $4`, idleBefore, limit)
	}
	return queryIdleSites(ctx, database, ex, q, idleBefore)
}

// SetSiteKeep sets the Keep flag: a kept site is never removed as idle.
func SetSiteKeep(ctx context.Context, database *sql.DB, siteID string, keep bool) error {
	res, err := database.ExecContext(ctx, `UPDATE sites SET idle_keep = $2 WHERE id = $1 AND deleted_at IS NULL`, siteID, keep)
	return oneRow(res, err)
}

// ListSiteKeep returns the ids of the user's live sites marked Keep (all live
// sites when userID is "").
func ListSiteKeep(ctx context.Context, database *sql.DB, userID string) (map[string]bool, error) {
	q := `SELECT id FROM sites WHERE deleted_at IS NULL AND idle_keep`
	args := []any{}
	if userID != "" {
		q += ` AND user_id = $1`
		args = append(args, userID)
	}
	rows, err := database.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// IdleEvidence says whether visit data can be trusted to call a site idle:
// the earliest analytics hour on record (visits before it are unknown) and
// when the log ingester last ran. Zero times when there is none.
func IdleEvidence(ctx context.Context, database *sql.DB) (since, lastIngest time.Time, err error) {
	var s, l sql.NullTime
	if err = database.QueryRowContext(ctx, `SELECT (SELECT min(hour) FROM site_view_hourly), (SELECT max(updated_at) FROM analytics_ingest_state)`).Scan(&s, &l); err != nil {
		return
	}
	return s.Time, l.Time, nil
}
