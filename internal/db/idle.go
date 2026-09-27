package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"
)

// Idle-site cleanup (owner decision 2026-09-27; handler/idle.go).
//
// A site's last activity is the latest of: when it was created, its newest
// version, the last hour a real person (analytics class 'person') viewed it,
// its newest collection entry, the last change to its saved data or settings
// (sites.updated_at: state writes, restore, offline, visibility), and when its
// owner last chose "Keep it" or restored it. A visit by the owner cannot be
// told apart from anyone else's (visitor addresses are only kept salted and
// hashed), so any person's visit counts.
//
// Never considered: sites in Recently deleted, taken down, owned by a
// suspended or admin account, with the Keep flag, with a custom domain or a
// claimed <name>.<SITE_DOMAIN> (current, earlier or retired), taken offline
// by their owner (a deliberate choice to keep it, just not serve it), preview
// sites (they expire on their own), and the operator's exempt accounts
// (IdleExempt: IDLE_CLEANUP_EXEMPT_HANDLES, the plugin reviewer account) and
// event accounts (users.event_account).

// IdleExempt is the operator's list of accounts the cleanup never touches.
type IdleExempt struct {
	Handles       []string // lowercased handles
	ReviewerEmail string   // REVIEW_ACCOUNT_EMAIL, "" when none
}

func (e IdleExempt) args() []any {
	h := e.Handles
	if h == nil {
		h = []string{}
	}
	return []any{pq.Array(h), strings.ToLower(strings.TrimSpace(e.ReviewerEmail))}
}

// IdleSite is one site the cleanup acts on (or would, in the dry run).
type IdleSite struct {
	SiteID       string
	UserID       string
	Name         string
	OwnerEmail   string
	OwnerHandle  string
	LastActivity time.Time
	WarnedAt     sql.NullTime
	RemoveAt     sql.NullTime // the removal date the warning email gave
}

// idleRemoveDue is true for a warned site whose promised removal date is
// before $<p+1>: the date stored with the warning, or (for warnings sent
// before it was stored) the warning plus $<p> seconds of grace. A later
// IDLE_GRACE_DAYS change therefore applies to new warnings only.
func idleRemoveDue(p int) string {
	return fmt.Sprintf(`COALESCE(s.idle_remove_at, s.idle_warned_at + ($%d * interval '1 second')) < $%d`, p, p+1)
}

func graceSeconds(grace time.Duration) int64 { return int64(grace.Seconds()) }

// idleLastActivity is the SQL expression for a site's last activity (s is
// the sites row).
const idleLastActivity = `GREATEST(
	s.created_at,
	COALESCE(s.updated_at, s.created_at),
	COALESCE(s.idle_kept_at, s.created_at),
	COALESCE((SELECT max(v.created_at) FROM versions v WHERE v.site_id = s.id), s.created_at),
	COALESCE((SELECT max(c.created_at) FROM collection_items c WHERE c.site_id = s.id), s.created_at),
	COALESCE((SELECT max(h.hour) + interval '1 hour' FROM site_view_hourly h WHERE h.site_id = s.id AND h.class = 'person' AND h.views > 0), s.created_at))`

// idleEligible is the WHERE clause every stage shares: the exemptions (s is
// the sites row, u its owner). $p is the exempt handles (text[]) and $p+1 the
// reviewer address, in IdleExempt.args order. Not in Recently deleted is
// checked by the caller (the removal runs after the row is marked deleted).
func idleEligible(p int) string {
	return fmt.Sprintf(`s.suspended_at IS NULL
	AND s.offline_at IS NULL
	AND u.suspended_at IS NULL
	AND NOT u.is_admin
	AND NOT s.idle_keep
	AND s.expires_at IS NULL
	AND s.custom_domain IS NULL
	AND s.previous_domain IS NULL
	AND NOT EXISTS (SELECT 1 FROM legacy_hostnames l WHERE l.site_id = s.id)
	AND NOT (lower(COALESCE(u.handle, '')) = ANY($%d::text[]))
	AND NOT ($%d <> '' AND lower(u.username) = $%d)
	AND NOT u.event_account`, p, p+1, p+1)
}

func queryIdleSites(ctx context.Context, database *sql.DB, ex IdleExempt, extra string, args ...any) ([]IdleSite, error) {
	all := append(ex.args(), args...)
	rows, err := database.QueryContext(ctx, `
		SELECT id, user_id, name, username, handle, last_activity, idle_warned_at, idle_remove_at FROM (
			SELECT s.id, s.user_id::text AS user_id, s.name, u.username, COALESCE(u.handle, '') AS handle,
			       `+idleLastActivity+` AS last_activity, s.idle_warned_at, s.idle_remove_at
			  FROM sites s JOIN users u ON u.id = s.user_id
			 WHERE s.deleted_at IS NULL AND `+idleEligible(1)+`
		) x WHERE `+extra, all...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []IdleSite
	for rows.Next() {
		var s IdleSite
		if err := rows.Scan(&s.SiteID, &s.UserID, &s.Name, &s.OwnerEmail, &s.OwnerHandle, &s.LastActivity, &s.WarnedAt, &s.RemoveAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ListIdleSitesToWarn: eligible sites idle since before idleBefore that have
// not been warned, longest idle first, at most limit (0 = all).
func ListIdleSitesToWarn(ctx context.Context, database *sql.DB, ex IdleExempt, idleBefore time.Time, limit int) ([]IdleSite, error) {
	q := `idle_warned_at IS NULL AND last_activity < $3 ORDER BY last_activity`
	if limit > 0 {
		return queryIdleSites(ctx, database, ex, q+` LIMIT $4`, idleBefore, limit)
	}
	return queryIdleSites(ctx, database, ex, q, idleBefore)
}

// ListIdleSitesToRemove: eligible warned sites whose removal date (see
// idleRemoveDue; grace for warnings from before it was stored) is before now,
// with no activity since the warning, earliest warned first, at most limit
// (0 = all).
func ListIdleSitesToRemove(ctx context.Context, database *sql.DB, ex IdleExempt, now time.Time, grace time.Duration, limit int) ([]IdleSite, error) {
	q := `idle_warned_at IS NOT NULL AND COALESCE(idle_remove_at, idle_warned_at + ($3 * interval '1 second')) < $4
		AND last_activity <= idle_warned_at ORDER BY idle_warned_at`
	if limit > 0 {
		return queryIdleSites(ctx, database, ex, q+` LIMIT $5`, graceSeconds(grace), now, limit)
	}
	return queryIdleSites(ctx, database, ex, q, graceSeconds(grace), now)
}

// ClearStaleIdleWarnings drops the warning (and its links) of every live site
// that was visited, deployed or written to since it was warned, or became
// exempt: it is no longer idle. Returns how many.
func ClearStaleIdleWarnings(ctx context.Context, database *sql.DB, ex IdleExempt) (int64, error) {
	res, err := database.ExecContext(ctx, `
		UPDATE sites s SET idle_warned_at = NULL, idle_token_hash = NULL
		  FROM users u
		 WHERE u.id = s.user_id AND s.idle_warned_at IS NOT NULL AND s.deleted_at IS NULL
		   AND (NOT (`+idleEligible(1)+`) OR `+idleLastActivity+` > s.idle_warned_at)`, ex.args()...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// MarkIdleWarned records, inside tx, that the owner is being warned, with the
// hash of the token their links carry and the removal date the email gives: only while the site is live, still
// unwarned, still eligible and idle since before idleBefore (all re-checked
// here, under the row lock). sql.ErrNoRows when it no longer qualifies. The
// caller sends the email before committing, so a warning is only on record
// once the email was accepted.
func MarkIdleWarned(ctx context.Context, tx *sql.Tx, ex IdleExempt, siteID string, tokenHash []byte, idleBefore, removeAt time.Time) error {
	args := append(ex.args(), siteID, tokenHash, idleBefore, removeAt)
	res, err := tx.ExecContext(ctx, `UPDATE sites s SET idle_warned_at = now(), idle_remove_at = $6, idle_token_hash = $4
		  FROM users u
		 WHERE s.id = $3 AND u.id = s.user_id AND s.deleted_at IS NULL AND s.idle_warned_at IS NULL
		   AND `+idleEligible(1)+` AND `+idleLastActivity+` < $5`, args...)
	return oneRow(res, err)
}

// MarkIdleRemoved records, in the same transaction that moves the site to
// Recently deleted, that the cleanup removed it, with its restore link's hash
// — only while the site still qualifies: its removal date is before now, no
// activity since, still eligible (re-checked here, so a Keep, a deploy or a
// new domain that landed since the list was read rolls the removal back).
// sql.ErrNoRows when it no longer qualifies.
func MarkIdleRemoved(ctx context.Context, q Querier, ex IdleExempt, siteID string, tokenHash []byte, now time.Time, grace time.Duration) error {
	args := append(ex.args(), siteID, tokenHash, graceSeconds(grace), now)
	res, err := q.ExecContext(ctx, `UPDATE sites s SET idle_removed_at = now(), idle_token_hash = $4
		  FROM users u
		 WHERE s.id = $3 AND u.id = s.user_id
		   AND s.idle_warned_at IS NOT NULL AND `+idleRemoveDue(5)+`
		   AND `+idleEligible(1)+` AND `+idleLastActivity+` <= s.idle_warned_at`, args...)
	return oneRow(res, err)
}

// IdleLink is the site a cleanup link's token belongs to.
type IdleLink struct {
	SiteID  string
	UserID  string
	Name    string
	Deleted bool
	Removed bool // removed by the cleanup (idle_removed_at set)
	// TakenDown: the operator took the site down or suspended its owner;
	// the links do nothing then.
	TakenDown bool
}

// GetIdleLink finds the site whose current cleanup token hashes to tokenHash.
// sql.ErrNoRows when none (used, replaced, or never issued).
func GetIdleLink(ctx context.Context, database *sql.DB, tokenHash []byte) (IdleLink, error) {
	var l IdleLink
	err := database.QueryRowContext(ctx, `SELECT s.id, s.user_id::text, s.name, s.deleted_at IS NOT NULL, s.idle_removed_at IS NOT NULL,
		       s.suspended_at IS NOT NULL OR u.suspended_at IS NOT NULL
		  FROM sites s JOIN users u ON u.id = s.user_id WHERE s.idle_token_hash = $1`, tokenHash).Scan(&l.SiteID, &l.UserID, &l.Name, &l.Deleted, &l.Removed, &l.TakenDown)
	return l, err
}

// KeepIdleSite resets a site's idle clock ("Keep it"): the warning and its
// links end, and the site counts as active from now.
func KeepIdleSite(ctx context.Context, q Querier, siteID string) error {
	res, err := q.ExecContext(ctx, `UPDATE sites SET idle_kept_at = now(), idle_warned_at = NULL, idle_removed_at = NULL, idle_token_hash = NULL WHERE id = $1`, siteID)
	return oneRow(res, err)
}

// CancelIdleLinks ends every emailed cleanup link of the account (its sign-in
// email changed: the links went to the old address). A live site that was
// warned loses the warning too, so the next run warns the new address.
func CancelIdleLinks(ctx context.Context, q Querier, userID string) error {
	_, err := q.ExecContext(ctx, `UPDATE sites
		   SET idle_token_hash = NULL,
		       idle_warned_at = CASE WHEN deleted_at IS NULL THEN NULL ELSE idle_warned_at END
		 WHERE user_id = $1 AND (idle_token_hash IS NOT NULL OR (deleted_at IS NULL AND idle_warned_at IS NOT NULL))`, userID)
	return err
}

// SetSiteKeep sets the Keep flag: a kept site is never warned or removed.
// Turning it on also ends any warning.
func SetSiteKeep(ctx context.Context, database *sql.DB, siteID string, keep bool) error {
	q := `UPDATE sites SET idle_keep = $2 WHERE id = $1 AND deleted_at IS NULL`
	if keep {
		q = `UPDATE sites SET idle_keep = $2, idle_warned_at = NULL, idle_token_hash = NULL WHERE id = $1 AND deleted_at IS NULL`
	}
	res, err := database.ExecContext(ctx, q, siteID, keep)
	return oneRow(res, err)
}

// SiteIdleFlags is a live site's Keep flag and pending warning.
type SiteIdleFlags struct {
	Keep     bool
	WarnedAt sql.NullTime
	RemoveAt sql.NullTime // the removal date the warning gave
}

// RemovalAt is when a warned site moves to Recently deleted: the date the
// warning gave, or the warning plus grace for warnings from before it was
// stored. Zero when not warned.
func (f SiteIdleFlags) RemovalAt(grace time.Duration) time.Time {
	switch {
	case !f.WarnedAt.Valid:
		return time.Time{}
	case f.RemoveAt.Valid:
		return f.RemoveAt.Time
	}
	return f.WarnedAt.Time.Add(grace)
}

// ListSiteIdleFlags returns the Keep flag and warning of each of the user's
// live sites (all live sites when userID is ""), by site id.
func ListSiteIdleFlags(ctx context.Context, database *sql.DB, userID string) (map[string]SiteIdleFlags, error) {
	q := `SELECT id, idle_keep, idle_warned_at, idle_remove_at FROM sites WHERE deleted_at IS NULL AND (idle_keep OR idle_warned_at IS NOT NULL)`
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
	out := map[string]SiteIdleFlags{}
	for rows.Next() {
		var id string
		var f SiteIdleFlags
		if err := rows.Scan(&id, &f.Keep, &f.WarnedAt, &f.RemoveAt); err != nil {
			return nil, err
		}
		out[id] = f
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
