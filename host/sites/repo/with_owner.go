package repo

import (
	"errors"

	"context"
	"database/sql"
	"time"

	"github.com/lib/pq"

	"github.com/vsriram/simple-host/host/model"
)

// Sites read with their owner. A site's owner handle and whether the owner
// is suspended belong with the site in one statement: a hold read in two
// steps could change in between, and these are the hottest lookups. This
// file is the one place outside Access that reads users
// (scripts/split/table-owner-exceptions.txt); ownerFrom, ownerCols and
// sqlTakenDown name the account columns Sites knows about.

const ownerFrom = ` FROM sites s JOIN users o ON o.id = s.user_id`

// ownerCols scan into ownerDest.
const ownerCols = `o.username, COALESCE(o.handle, ''), o.suspended_at IS NOT NULL, COALESCE(o.suspended_reason, '')`

// ownerDest is where ownerCols go; the username only where withUsername
// (the admin views), as before.
func ownerDest(s *Site, withUsername bool) []any {
	username := &s.OwnerUsername
	if !withUsername {
		username = new(string)
	}
	return []any{username, &s.OwnerHandle, &s.OwnerSuspended, &s.OwnerSuspendedReason}
}

// siteCols are one site's own fields, in siteDest's order.
const siteCols = `s.id, s.user_id, s.name, s.active_version, COALESCE(s.site_url, ''), s.created_at, s.updated_at,
	s.custom_domain, s.domain_status, s.visibility, s.suspended_at IS NOT NULL, COALESCE(s.suspended_reason, ''),
	s.deleted_at IS NOT NULL, s.offline_at IS NOT NULL, s.keep_versions, s.passcode_enc IS NOT NULL, s.access = 'specific'`

func siteDest(s *Site) []any {
	return []any{&s.ID, &s.UserID, &s.Name, &s.ActiveVersion, &s.SiteURL, orZero{&s.CreatedAt}, orZero{&s.UpdatedAt},
		&s.CustomDomain, &s.DomainStatus, &s.Visibility, &s.SiteSuspended, &s.SiteSuspendedReason,
		&s.Deleted, &s.Offline, &s.KeepVersions, &s.Passcode, &s.NamedViewers}
}

// orZero scans a nullable timestamp (created_at, updated_at may be NULL on
// old rows) as the zero time.
type orZero struct{ t *time.Time }

func (z orZero) Scan(v any) error {
	var n sql.NullTime
	err := n.Scan(v)
	*z.t = n.Time
	return err
}

// listCols are siteCols plus the domain, deploy and summary fields site
// lists show, in listDest's order.
const listCols = siteCols + `, s.domain_last_error, s.domain_bound_at, s.domain_verified_at, COALESCE(s.previous_domain, ''),
	COALESCE(s.domain_cert_status, ''), COALESCE(s.domain_token, ''),
	(SELECT max(v.created_at) FROM versions v WHERE v.site_id = s.id AND v.status = 'active'),
	COALESCE(s.summary_title, ''), COALESCE(s.summary_description, ''), COALESCE(s.summary_headings, ''), COALESCE(s.summary_text, '')`

func listDest(s *Site) []any {
	return append(siteDest(s), &s.DomainLastError, &s.DomainBoundAt, &s.DomainVerifiedAt, &s.PreviousDomain,
		&s.DomainCertStatus, &s.DomainToken, &s.LastDeployedAt,
		&s.SummaryTitle, &s.SummaryDescription, &s.SummaryHeadings, &s.SummaryText)
}

func getSite(ctx context.Context, q Querier, withUsername bool, where string, args ...any) (Site, error) {
	var s Site
	err := q.QueryRowContext(ctx, `SELECT `+siteCols+`, `+ownerCols+ownerFrom+` WHERE `+where, args...).
		Scan(append(siteDest(&s), ownerDest(&s, withUsername)...)...)
	return s, err
}

func listSites(ctx context.Context, q Querier, withUsername bool, where string, args ...any) ([]Site, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+listCols+`, `+ownerCols+ownerFrom+` WHERE `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Site
	for rows.Next() {
		var s Site
		if err := rows.Scan(append(listDest(&s), ownerDest(&s, withUsername)...)...); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// GetSiteByID loads one site by id with its owner's handle and suspension
// state (admin actions address sites by id). It also finds a site in
// Recently deleted, with Deleted set.
func GetSiteByID(ctx context.Context, q Querier, siteID string) (Site, error) {
	return getSite(ctx, q, true, `s.id::text = $1`, siteID)
}

// GetSiteByUser loads the account's live site of that name.
func GetSiteByUser(ctx context.Context, db *sql.DB, userID, name string) (Site, error) {
	return getSite(ctx, db, false, `s.user_id = $1 AND s.name = $2 AND s.deleted_at IS NULL`, userID, name)
}

// ListSitesByUser lists the account's live sites, most recently changed first.
func ListSitesByUser(ctx context.Context, db *sql.DB, userID string) ([]Site, error) {
	return listSites(ctx, db, false, `s.user_id = $1 AND s.deleted_at IS NULL
		ORDER BY s.updated_at DESC, s.created_at DESC, s.name ASC`, userID)
}

// ListAllSites lists all live sites with their owners and export metadata.
func ListAllSites(ctx context.Context, db *sql.DB) ([]Site, error) {
	return listSites(ctx, db, true, `s.deleted_at IS NULL ORDER BY s.updated_at DESC, s.created_at DESC, s.name ASC`)
}

// ListSitemapSites reads platform-eligible rows in one query, independent of
// profile listing. The caller checks live files and the site's own indexing
// preferences.
func ListSitemapSites(ctx context.Context, db *sql.DB) ([]Site, error) {
	return listSites(ctx, db, false, `s.deleted_at IS NULL AND s.offline_at IS NULL AND NOT `+sqlTakenDown+`
		AND `+sqlNotGated+` AND s.expires_at IS NULL ORDER BY s.id`)
}

// VisitorSharedSites lists, in name order, the live sites email is a named
// viewer of at this moment: membership and site in one statement, so a
// viewer removed and the site renamed meanwhile never shows its new name.
func VisitorSharedSites(ctx context.Context, q Querier, email string) ([]Site, error) {
	return listSites(ctx, q, false, `s.deleted_at IS NULL
		AND EXISTS (SELECT 1 FROM site_viewers v WHERE v.site_id = s.id AND v.email = $1) ORDER BY s.name`, email)
}

// GetSiteOwner is a live site's owner's handle and id, and its name.
func GetSiteOwner(ctx context.Context, q Querier, siteID string) (handle, userID, name string, err error) {
	s, err := getSite(ctx, q, false, `s.id::text = $1 AND s.deleted_at IS NULL`, siteID)
	return s.OwnerHandle, s.UserID, s.Name, err
}

// GetSiteOwnerEmail is the sign-in address of the site's owner.
func GetSiteOwnerEmail(ctx context.Context, db *sql.DB, siteID string) (string, error) {
	s, err := getSite(ctx, db, true, `s.id = $1`, siteID)
	return s.OwnerUsername, err
}

// GetHandleBySiteName is the handle of the oldest live site of that name's
// owner (the legacy bare-name address).
func GetHandleBySiteName(ctx context.Context, db *sql.DB, name string) (string, error) {
	s, err := getSite(ctx, db, false, `s.name = $1 AND s.deleted_at IS NULL ORDER BY s.created_at ASC LIMIT 1`, name)
	return s.OwnerHandle, err
}

// SiteSuspension reports whether a site is taken down, by its own flag or
// through its owner's account, and the reason to show.
func SiteSuspension(ctx context.Context, q Querier, siteID string) (bool, string, error) {
	s, err := getSite(ctx, q, false, `s.id::text = $1`, siteID)
	if err != nil {
		return false, "", err
	}
	if s.SiteSuspended {
		return true, s.SiteSuspendedReason, nil
	}
	if s.OwnerSuspended {
		return true, s.OwnerSuspendedReason, nil
	}
	return false, "", nil
}

// LiveSiteNames is the account's sites that visitors can see: not deleted,
// not taken down, and the account itself not suspended. withDeleted counts
// sites in Recently deleted too.
func LiveSiteNames(ctx context.Context, q Querier, accountID string, withDeleted bool) ([]string, error) {
	return scanNames(q.QueryContext(ctx, `SELECT s.name`+ownerFrom+`
		 WHERE s.user_id = $1 AND ($2 OR s.deleted_at IS NULL) AND NOT `+sqlTakenDown+`
		 ORDER BY s.name`, accountID, withDeleted))
}

func scanNames(rows *sql.Rows, err error) ([]string, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

// AccountSiteTakenDown: some site of the account is taken down (by itself
// or through the account), so the purge keeps it.
func AccountSiteTakenDown(ctx context.Context, q Querier, accountID string) (bool, error) {
	var down bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1`+ownerFrom+` WHERE s.user_id = $1 AND `+sqlTakenDown+`)`, accountID).Scan(&down)
	return down, err
}

// ListExpiredSites returns live sites whose expires_at has passed, except
// taken-down ones (DeleteExpiredSite checks again as it deletes).
func ListExpiredSites(ctx context.Context, db *sql.DB) ([]ExpiredSite, error) {
	rows, err := db.QueryContext(ctx, `SELECT s.id, s.user_id, s.name`+ownerFrom+`
		WHERE s.expires_at IS NOT NULL AND s.expires_at < now() AND s.deleted_at IS NULL AND NOT `+sqlTakenDown+`
		ORDER BY s.expires_at LIMIT 500`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ExpiredSite
	for rows.Next() {
		var e ExpiredSite
		if err := rows.Scan(&e.ID, &e.UserID, &e.Name); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ListPurgeableSites returns deleted sites whose restore date has passed,
// except taken-down ones (LockPurgeableSite checks again as it purges).
func ListPurgeableSites(ctx context.Context, database *sql.DB, retention time.Duration) ([]DeletedSite, error) {
	rows, err := database.QueryContext(ctx, `SELECT s.id, s.user_id, s.name, s.deleted_at, s.custom_domain, s.purge_at`+ownerFrom+`
		WHERE s.deleted_at IS NOT NULL AND COALESCE(s.purge_at, s.deleted_at + ($1 * interval '1 second')) < now()
		  AND NOT `+sqlTakenDown+` ORDER BY s.deleted_at LIMIT 500`, int64(retention.Seconds()))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DeletedSite
	for rows.Next() {
		d, err := scanDeletedSite(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ListDomainsToCheck returns bound domains due for verification: every
// binding that is not "active" on every pass, plus active ones whose last
// proof is older than activeAge — an active domain that stops serving has to
// be able to fall back to error/pending, so "active" is a claim with an
// expiry, not a latch.
func ListDomainsToCheck(ctx context.Context, database *sql.DB, activeAge time.Duration) ([]model.BoundDomain, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT s.id, s.custom_domain, COALESCE(s.domain_status, ''), s.domain_verified_at IS NOT NULL, COALESCE(s.previous_domain, ''),
		       s.user_id, s.name, COALESCE(s.domain_token, ''), COALESCE(s.custom_domain = ANY(s.domain_proof_exempt), false),
		       `+sqlTakenDown+ownerFrom+`
		WHERE s.custom_domain IS NOT NULL AND s.custom_domain <> '' AND s.deleted_at IS NULL
		  AND (s.domain_status IS DISTINCT FROM 'active' OR s.domain_verified_at IS NULL
		       OR s.domain_verified_at < now() - ($1 * interval '1 second'))
		ORDER BY s.custom_domain`, int64(activeAge.Seconds()))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.BoundDomain
	for rows.Next() {
		var d model.BoundDomain
		if err := rows.Scan(&d.SiteID, &d.Domain, &d.Status, &d.Verified, &d.PreviousDomain,
			&d.UserID, &d.Name, &d.Token, &d.ProofExempt, &d.Suspended); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ServedSite is one site in the month's most-served list.
type ServedSite struct {
	SiteID     string `json:"site_id"`
	UserID     string `json:"user_id"`
	Name       string `json:"name"`
	Owner      string `json:"owner"`
	Bytes      int64  `json:"bytes"`
	PagesBytes int64  `json:"pages_bytes"`
	APIBytes   int64  `json:"api_bytes"`
	Requests   int64  `json:"requests"`
}

// TopServedSites is the limit sites that took the most bytes since day,
// with their owner (handle, else sign-in email).
func TopServedSites(ctx context.Context, q Querier, since string, limit int) ([]ServedSite, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT st.site_id, s.user_id, s.name, COALESCE(NULLIF(o.handle, ''), o.username),
		       SUM(st.bytes),
		       COALESCE(SUM(st.bytes) FILTER (WHERE st.kind = 'pages'), 0),
		       COALESCE(SUM(st.bytes) FILTER (WHERE st.kind = 'api'), 0),
		       SUM(st.requests)
		FROM site_traffic_daily st
		JOIN sites s ON s.id = st.site_id
		JOIN users o ON o.id = s.user_id
		WHERE st.day >= $1::date
		GROUP BY st.site_id, s.user_id, s.name, o.handle, o.username
		ORDER BY SUM(st.bytes) DESC, SUM(st.requests) DESC, s.name
		LIMIT $2`, since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ServedSite{}
	for rows.Next() {
		var s ServedSite
		if err := rows.Scan(&s.SiteID, &s.UserID, &s.Name, &s.Owner, &s.Bytes, &s.PagesBytes, &s.APIBytes, &s.Requests); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// SetHomeSite sets (name nil: clears) the account's home page to its live
// site of that name, in one statement that checks the site as it writes.
// sql.ErrNoRows when there is no such account or live site.
func SetHomeSite(ctx context.Context, q Querier, userID string, name *string) error {
	var res sql.Result
	var err error
	if name == nil {
		res, err = q.ExecContext(ctx, `UPDATE users SET home_site_id = NULL WHERE id = $1`, userID)
	} else {
		res, err = q.ExecContext(ctx, `UPDATE users o SET home_site_id = s.id FROM sites s
			WHERE o.id = $1 AND s.user_id = o.id AND s.name = $2 AND s.deleted_at IS NULL`, userID, *name)
	}
	var pqErr *pq.Error
	if errors.As(err, &pqErr) && pqErr.Code == "23503" {
		return sql.ErrNoRows // the site went between choosing and writing
	}
	return oneRow(res, err)
}

// ListHandlesWithSites is every handle that owns a live site, in order.
func ListHandlesWithSites(ctx context.Context, q *sql.DB) ([]string, error) {
	return scanNames(q.QueryContext(ctx, `SELECT DISTINCT o.handle`+ownerFrom+`
		WHERE s.deleted_at IS NULL AND o.handle IS NOT NULL AND o.handle <> '' ORDER BY o.handle`))
}
