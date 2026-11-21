package repo

import (
	"context"
	"database/sql"

	"github.com/vsriram/simple-host/host/model"
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

// ScanDomainInfo preserves the shared sites lookup.
func ScanDomainInfo(row interface{ Scan(...any) error }) (SiteDomainInfo, error) {
	var info SiteDomainInfo
	err := row.Scan(&info.SiteID, &info.UserID, &info.Name, &info.Domain, &info.Status,
		&info.VerifiedAt, &info.BoundAt, &info.LastError,
		&info.PreviousDomain, &info.CertStatus, &info.FailingSince,
		&info.Token, &info.ProofExempt)
	return info, err
}
