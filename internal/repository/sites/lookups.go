package sites

import (
	"context"
	"database/sql"

	"github.com/vsriram/simple-host/internal/model"
)

// SiteDomainInfo is the domain-binding view of a site.
type SiteDomainInfo = model.SiteDomainInfo

// DomainColumns lists the columns consumed by ScanDomainInfo, in order.
const DomainColumns = `id, user_id, name, COALESCE(custom_domain, ''), COALESCE(domain_status, ''),
	domain_verified_at, domain_bound_at, COALESCE(domain_last_error, ''),
	COALESCE(previous_domain, ''), COALESCE(domain_cert_status, ''), domain_failing_since,
	COALESCE(domain_token, ''), COALESCE(custom_domain = ANY(domain_proof_exempt), false)`

// CountSitesByUser counts the account's sites including those in Recently
// deleted, which still hold their name and files: the per-account cap.
func CountSitesByUser(ctx context.Context, q Querier, userID string) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, `SELECT count(*) FROM sites WHERE user_id = $1`, userID).Scan(&n)
	return n, err
}

// GetSiteByID loads one site by id with its owner's handle and suspension
// state (admin actions address sites by id). It also finds a site in Recently
// deleted, with Deleted set.
func GetSiteByID(ctx context.Context, q Querier, siteID string) (Site, error) {
	var site Site
	err := q.QueryRowContext(ctx, `
		SELECT s.id, s.user_id, s.name, s.active_version, COALESCE(s.site_url, ''), s.created_at, s.updated_at,
		       s.custom_domain, s.domain_status, s.visibility, u.username, COALESCE(u.handle, ''),
		       s.suspended_at IS NOT NULL, COALESCE(s.suspended_reason, ''),
		       u.suspended_at IS NOT NULL, COALESCE(u.suspended_reason, ''),
		       s.deleted_at IS NOT NULL, s.offline_at IS NOT NULL, s.keep_versions, s.passcode_enc IS NOT NULL, s.access = 'specific'
		  FROM sites s JOIN users u ON u.id = s.user_id
		 WHERE s.id::text = $1`, siteID).Scan(
		&site.ID, &site.UserID, &site.Name, &site.ActiveVersion, &site.SiteURL, &site.CreatedAt, &site.UpdatedAt,
		&site.CustomDomain, &site.DomainStatus, &site.Visibility, &site.OwnerUsername, &site.OwnerHandle,
		&site.SiteSuspended, &site.SiteSuspendedReason, &site.OwnerSuspended, &site.OwnerSuspendedReason,
		&site.Deleted, &site.Offline, &site.KeepVersions, &site.Passcode, &site.NamedViewers,
	)
	return site, err
}

// GetSiteByUser preserves the shared sites lookup.
func GetSiteByUser(ctx context.Context, db *sql.DB, userID, name string) (Site, error) {
	const query = `
		SELECT id, user_id, name, active_version, COALESCE(site_url, ''), created_at, updated_at, custom_domain, domain_status, visibility,
		       (SELECT COALESCE(u.handle, '') FROM users u WHERE u.id = sites.user_id),
		       sites.suspended_at IS NOT NULL, COALESCE(sites.suspended_reason, ''),
		       COALESCE((SELECT su.suspended_at IS NOT NULL FROM users su WHERE su.id = sites.user_id), false),
		       COALESCE((SELECT su.suspended_reason FROM users su WHERE su.id = sites.user_id AND su.suspended_at IS NOT NULL), ''),
		       sites.offline_at IS NOT NULL, sites.keep_versions, sites.passcode_enc IS NOT NULL, sites.access = 'specific'
		FROM sites
		WHERE user_id = $1 AND name = $2 AND deleted_at IS NULL
	`

	var site Site
	err := db.QueryRowContext(ctx, query, userID, name).Scan(
		&site.ID,
		&site.UserID,
		&site.Name,
		&site.ActiveVersion,
		&site.SiteURL,
		&site.CreatedAt,
		&site.UpdatedAt,
		&site.CustomDomain,
		&site.DomainStatus,
		&site.Visibility,
		&site.OwnerHandle,
		&site.SiteSuspended,
		&site.SiteSuspendedReason,
		&site.OwnerSuspended,
		&site.OwnerSuspendedReason,
		&site.Offline,
		&site.KeepVersions,
		&site.Passcode,
		&site.NamedViewers,
	)
	return site, err
}

// GetSiteDomainInfo returns domain binding info for siteID. ok is false when
// the site has no custom_domain set (or the site row is missing).
func GetSiteDomainInfo(ctx context.Context, database *sql.DB, siteID string) (SiteDomainInfo, bool, error) {
	info, err := ScanDomainInfo(database.QueryRowContext(ctx, `SELECT `+DomainColumns+` FROM sites WHERE id = $1`, siteID))
	if err != nil {
		if err == sql.ErrNoRows {
			return SiteDomainInfo{}, false, nil
		}
		return SiteDomainInfo{}, false, err
	}
	if info.Domain == "" {
		return SiteDomainInfo{}, false, nil
	}
	return info, true, nil
}

// SetSiteSuspended takes a site down (reason non-empty) or restores it
// (reason empty). Nothing else about the site changes, so restoring puts it
// back exactly as it was. Returns sql.ErrNoRows for an unknown site.
func SetSiteSuspended(ctx context.Context, q Querier, siteID, reason string) error {
	var res sql.Result
	var err error
	if reason != "" {
		res, err = q.ExecContext(ctx,
			`UPDATE sites SET suspended_at = COALESCE(suspended_at, now()), suspended_reason = $2 WHERE id::text = $1`,
			siteID, reason)
	} else {
		res, err = q.ExecContext(ctx,
			`UPDATE sites SET suspended_at = NULL, suspended_reason = NULL WHERE id::text = $1`, siteID)
	}
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// GetActiveSiteVersion preserves the shared sites lookup.
func GetActiveSiteVersion(ctx context.Context, db *sql.DB, siteID string) (Version, error) {
	const query = `
		SELECT v.id, v.site_id, v.version_number, v.disk_path, v.status, v.created_at
		FROM versions v
		INNER JOIN sites s ON s.id = v.site_id
		WHERE v.site_id = $1
		  AND v.version_number = s.active_version
		  AND v.status = 'active'
	`

	var version Version
	err := db.QueryRowContext(ctx, query, siteID).Scan(
		&version.ID,
		&version.SiteID,
		&version.VersionNumber,
		&version.DiskPath,
		&version.Status,
		&version.CreatedAt,
	)
	return version, err
}

// ListSitesByUser preserves the shared sites lookup.
func ListSitesByUser(ctx context.Context, db *sql.DB, userID string) ([]Site, error) {
	const query = `
		SELECT id, user_id, name, active_version, COALESCE(site_url, ''), created_at, updated_at, custom_domain, domain_status, visibility,
		       (SELECT COALESCE(u.handle, '') FROM users u WHERE u.id = sites.user_id),
		       domain_last_error, domain_bound_at, domain_verified_at, COALESCE(previous_domain, ''), COALESCE(domain_cert_status, ''), COALESCE(domain_token, ''),
		       (SELECT max(v.created_at) FROM versions v WHERE v.site_id = sites.id AND v.status = 'active'),
		       sites.suspended_at IS NOT NULL, COALESCE(sites.suspended_reason, ''),
		       COALESCE((SELECT su.suspended_at IS NOT NULL FROM users su WHERE su.id = sites.user_id), false),
		       COALESCE((SELECT su.suspended_reason FROM users su WHERE su.id = sites.user_id AND su.suspended_at IS NOT NULL), ''),
		       sites.offline_at IS NOT NULL, sites.keep_versions, sites.passcode_enc IS NOT NULL, sites.access = 'specific',
		       COALESCE(summary_title, ''), COALESCE(summary_description, ''), COALESCE(summary_headings, ''), COALESCE(summary_text, '')
		FROM sites
		WHERE user_id = $1 AND deleted_at IS NULL
		ORDER BY updated_at DESC, created_at DESC, name ASC
	`

	rows, err := db.QueryContext(ctx, query, userID)
	if err != nil {
		return nil, err
	}
	return ScanSiteRows(rows)
}

// ScanSiteRows preserves the shared sites lookup.
func ScanSiteRows(rows *sql.Rows) ([]Site, error) {
	defer rows.Close()

	var sites []Site
	for rows.Next() {
		var site Site

		if err := rows.Scan(
			&site.ID,
			&site.UserID,
			&site.Name,
			&site.ActiveVersion,
			&site.SiteURL,
			&site.CreatedAt,
			&site.UpdatedAt,
			&site.CustomDomain,
			&site.DomainStatus,
			&site.Visibility,
			&site.OwnerHandle,
			&site.DomainLastError,
			&site.DomainBoundAt,
			&site.DomainVerifiedAt,
			&site.PreviousDomain,
			&site.DomainCertStatus,
			&site.DomainToken,
			&site.LastDeployedAt,
			&site.SiteSuspended,
			&site.SiteSuspendedReason,
			&site.OwnerSuspended,
			&site.OwnerSuspendedReason,
			&site.Offline,
			&site.KeepVersions,
			&site.Passcode,
			&site.NamedViewers,
			&site.SummaryTitle, &site.SummaryDescription, &site.SummaryHeadings, &site.SummaryText,
		); err != nil {
			return nil, err
		}

		sites = append(sites, site)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return sites, nil
}

// ScanDomainInfo preserves the shared sites lookup.
func ScanDomainInfo(row interface{ Scan(...any) error }) (SiteDomainInfo, error) {
	var info SiteDomainInfo
	err := row.Scan(&info.SiteID, &info.UserID, &info.Name, &info.Domain, &info.Status,
		&info.VerifiedAt, &info.BoundAt, &info.LastError,
		&info.PreviousDomain, &info.CertStatus, &info.FailingSince,
		&info.Token, &info.ProofExempt)
	return info, err
}
