package repo

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"
	"github.com/vsriram/simple-host/host/model"
)

// IdleExempt is the operator's list of accounts the cleanup never touches.
type IdleExempt = model.IdleExempt

func idleExemptArgs(e IdleExempt) []any {
	h := e.Handles
	if h == nil {
		h = []string{}
	}
	return []any{pq.Array(h), strings.ToLower(strings.TrimSpace(e.ReviewerEmail))}
}

// IdleSite is one site the cleanup acts on (or would, in the dry run).
type IdleSite = model.IdleSite

// idleLastActivity is the SQL expression for a site's last activity (s is
// the sites row).
const idleLastActivity = `GREATEST(
	s.created_at,
	COALESCE(s.updated_at, s.created_at),
	COALESCE(s.idle_kept_at, s.created_at),
	COALESCE((SELECT max(v.created_at) FROM versions v WHERE v.site_id = s.id), s.created_at),
	COALESCE((SELECT max(c.created_at) FROM collection_items c WHERE c.site_id = s.id), s.created_at),
	COALESCE((SELECT max(h.hour) + interval '1 hour' FROM site_view_hourly h WHERE h.site_id = s.id AND h.class IN ('person','owner') AND h.views > 0), s.created_at))`

// idleEligible is the WHERE clause every stage shares: the exemptions (s is
// the sites row, u its owner). $p is the exempt handles (text[]) and $p+1 the
// reviewer address, in IdleExempt.args order. Not in Recently deleted is
// checked by the caller (the removal runs after the row is marked deleted).
func idleEligible(p int) string {
	return fmt.Sprintf(sqlSiteLive("s.")+`
	AND u.suspended_at IS NULL
	AND NOT u.is_admin
	AND NOT s.idle_keep
	AND s.expires_at IS NULL
	AND s.custom_domain IS NULL
	AND s.previous_domain IS NULL
	AND NOT EXISTS (SELECT 1 FROM legacy_hostnames l WHERE l.site_id = s.id)
	AND NOT (lower(COALESCE(u.handle, '')) = ANY($%d::text[]))
	AND NOT EXISTS (SELECT 1 FROM handle_aliases ha WHERE ha.user_id = u.id AND ha.handle = ANY($%[1]d::text[]))
	AND NOT ($%d <> '' AND lower(u.username) = $%d)
	AND NOT u.event_account`, p, p+1, p+1)
}

// eligible is idleEligible plus the family exemption (a constant, not a
// parameter, so the callers' parameter numbers stay as they are).
func idleExemptEligible(e IdleExempt, p int) string {
	if !e.FamilySites {
		return idleEligible(p)
	}
	return idleEligible(p) + `
	AND NOT EXISTS (SELECT 1 FROM address_families f WHERE f.user_id = s.user_id AND f.verified_at IS NOT NULL
	    AND f.canonical AND left(s.name, length(f.site_prefix)) = f.site_prefix AND length(s.name) > length(f.site_prefix))`
}

// MarkIdleRemoved records, in the same transaction that moves the site to
// Recently deleted, that the cleanup removed it: only while the site still
// qualifies (still eligible and idle since before idleBefore, re-checked here,
// so a Keep, a deploy or a new domain that landed since the list was read
// rolls the removal back). sql.ErrNoRows when it no longer qualifies.
func MarkIdleRemoved(ctx context.Context, q Querier, ex IdleExempt, siteID string, idleBefore time.Time) error {
	args := append(idleExemptArgs(ex), siteID, idleBefore)
	res, err := q.ExecContext(ctx, `UPDATE sites s SET idle_removed_at = now()
		  FROM users u
		 WHERE s.id = $3 AND u.id = s.user_id
		   AND `+idleExemptEligible(ex, 1)+` AND `+idleLastActivity+` < $4`, args...)
	return oneRow(res, err)
}
