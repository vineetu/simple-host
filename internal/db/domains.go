package db

import (
	"context"
	"database/sql"
	"strings"
)

// SiteDomainInfo is the domain-binding view of a site. Kept separate from the
// shared Site struct so existing SELECTs stay untouched (low blast radius).
type SiteDomainInfo struct {
	SiteID     string
	UserID     string
	Name       string
	Domain     string
	Status     string
	LastError  string
	VerifiedAt sql.NullTime
}

// SetCustomDomain binds domain to siteID with status "pending". Clears any prior
// verification error. Returns a unique-violation error when the domain is
// already taken (handler maps that to 409 via isUniqueViolation).
func SetCustomDomain(ctx context.Context, database *sql.DB, siteID, domain string) error {
	const query = `
		UPDATE sites
		SET custom_domain = $2,
		    domain_status = 'pending',
		    domain_last_error = NULL,
		    domain_verified_at = NULL
		WHERE id = $1
	`
	_, err := database.ExecContext(ctx, query, siteID, domain)
	return err
}

// ClearCustomDomain unbinds any custom domain from siteID.
func ClearCustomDomain(ctx context.Context, database *sql.DB, siteID string) error {
	const query = `
		UPDATE sites
		SET custom_domain = NULL,
		    domain_status = NULL,
		    domain_verified_at = NULL,
		    domain_last_error = NULL
		WHERE id = $1
	`
	_, err := database.ExecContext(ctx, query, siteID)
	return err
}

// GetSiteDomainInfo returns domain binding info for siteID. ok is false when
// the site has no custom_domain set (or the site row is missing).
func GetSiteDomainInfo(ctx context.Context, database *sql.DB, siteID string) (SiteDomainInfo, bool, error) {
	const query = `
		SELECT id, user_id, name, custom_domain, domain_status, domain_verified_at, domain_last_error
		FROM sites
		WHERE id = $1
	`
	var info SiteDomainInfo
	var domain, status, lastErr sql.NullString
	err := database.QueryRowContext(ctx, query, siteID).Scan(
		&info.SiteID,
		&info.UserID,
		&info.Name,
		&domain,
		&status,
		&info.VerifiedAt,
		&lastErr,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return SiteDomainInfo{}, false, nil
		}
		return SiteDomainInfo{}, false, err
	}
	if !domain.Valid || domain.String == "" {
		return SiteDomainInfo{}, false, nil
	}
	info.Domain = domain.String
	if status.Valid {
		info.Status = status.String
	}
	if lastErr.Valid {
		info.LastError = lastErr.String
	}
	return info, true, nil
}

// GetSiteByCustomDomain looks up a site by its bound custom domain (lowercased
// match). Returns sql.ErrNoRows when none.
func GetSiteByCustomDomain(ctx context.Context, database *sql.DB, domain string) (SiteDomainInfo, error) {
	const query = `
		SELECT id, user_id, name, custom_domain, domain_status
		FROM sites
		WHERE custom_domain = $1
	`
	domain = strings.ToLower(strings.TrimSpace(domain))
	var info SiteDomainInfo
	var status sql.NullString
	err := database.QueryRowContext(ctx, query, domain).Scan(
		&info.SiteID,
		&info.UserID,
		&info.Name,
		&info.Domain,
		&status,
	)
	if err != nil {
		return SiteDomainInfo{}, err
	}
	if status.Valid {
		info.Status = status.String
	}
	return info, nil
}

// SetDomainStatus updates domain_status and optional last_error. When status is
// "active", domain_verified_at is set to now(); otherwise it is left alone.
func SetDomainStatus(ctx context.Context, database *sql.DB, siteID, status, lastErr string) error {
	const query = `
		UPDATE sites
		SET domain_status = $2,
		    domain_last_error = NULLIF($3, ''),
		    domain_verified_at = CASE WHEN $2 = 'active' THEN now() ELSE domain_verified_at END
		WHERE id = $1
	`
	_, err := database.ExecContext(ctx, query, siteID, status, lastErr)
	return err
}
