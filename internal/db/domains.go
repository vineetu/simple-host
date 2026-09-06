package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
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
	BoundAt    sql.NullTime
	Handle     string
}

// SetCustomDomain binds domain to siteID with status "pending". Clears any prior
// verification error. Returns a unique-violation error when the domain is
// already taken (handler maps that to 409 via isUniqueViolation).
func SetCustomDomain(ctx context.Context, database Querier, siteID, domain string) error {
	const query = `
		UPDATE sites
		SET custom_domain = $2,
		    domain_status = 'pending',
		    domain_bound_at = now(),
		    domain_last_error = NULL,
		    domain_verified_at = NULL
		WHERE id = $1
	`
	_, err := database.ExecContext(ctx, query, siteID, domain)
	return err
}

// ClearCustomDomain unbinds any custom domain from siteID.
func ClearCustomDomain(ctx context.Context, database Querier, siteID string) error {
	const query = `
		UPDATE sites
		SET custom_domain = NULL,
		    domain_status = NULL,
		    domain_bound_at = NULL,
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
		SELECT id, user_id, name, custom_domain, domain_status, domain_verified_at, domain_bound_at, domain_last_error
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
		&info.BoundAt,
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
		SELECT id, user_id, name, custom_domain, domain_status, domain_verified_at, domain_bound_at
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
		&info.VerifiedAt,
		&info.BoundAt,
	)
	if err != nil {
		return SiteDomainInfo{}, err
	}
	if status.Valid {
		info.Status = status.String
	}
	return info, nil
}

// BoundDomain is one bound custom domain and the status it currently reports.
type BoundDomain struct {
	SiteID string
	Domain string
	Status string
}

// ListDomainsToCheck returns bound domains due for verification: every binding
// that is not "active" on every pass, plus active ones whose last proof is older
// than activeAge — an active domain that stops serving has to be able to fall
// back to error/pending, so "active" is a claim with an expiry, not a latch.
func ListDomainsToCheck(ctx context.Context, database *sql.DB, activeAge time.Duration) ([]BoundDomain, error) {
	const query = `
		SELECT id, custom_domain, COALESCE(domain_status, '')
		FROM sites
		WHERE custom_domain IS NOT NULL
		  AND custom_domain <> ''
		  AND (domain_status IS DISTINCT FROM 'active'
		       OR domain_verified_at IS NULL
		       OR domain_verified_at < now() - ($1 * interval '1 second'))
		ORDER BY custom_domain
	`
	rows, err := database.QueryContext(ctx, query, int64(activeAge.Seconds()))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []BoundDomain
	for rows.Next() {
		var d BoundDomain
		if err := rows.Scan(&d.SiteID, &d.Domain, &d.Status); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
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

// ErrDomainTaken means another site has already proved this domain.
var ErrDomainTaken = errors.New("domain is connected to another site")

// BindCustomDomain releases an unproven holder and binds the requester atomically.
// Lock the holder so verification cannot change the takeover decision mid-transaction.
func BindCustomDomain(ctx context.Context, database *sql.DB, siteID, domain string) (*SiteDomainInfo, error) {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// Serialize binds for this name even when no holder row exists yet.
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, domain); err != nil {
		return nil, err
	}
	var holder SiteDomainInfo
	err = tx.QueryRowContext(ctx, `
		SELECT s.id, s.user_id, s.name, s.custom_domain, s.domain_verified_at, COALESCE(u.handle, '')
		FROM sites s JOIN users u ON u.id = s.user_id
		WHERE s.custom_domain = $1 FOR UPDATE OF s`, domain).Scan(
		&holder.SiteID, &holder.UserID, &holder.Name, &holder.Domain, &holder.VerifiedAt, &holder.Handle)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	var released *SiteDomainInfo
	if err == nil && holder.SiteID != siteID {
		if holder.VerifiedAt.Valid {
			return nil, ErrDomainTaken
		}
		if err := ClearCustomDomain(ctx, tx, holder.SiteID); err != nil {
			return nil, err
		}
		released = &holder
	}
	if err := SetCustomDomain(ctx, tx, siteID, domain); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return released, nil
}

// ReleaseExpiredDomains clears only bindings that have never been proven.
func ReleaseExpiredDomains(ctx context.Context, database *sql.DB) ([]SiteDomainInfo, error) {
	rows, err := database.QueryContext(ctx, `
		WITH expired AS (
		SELECT id, user_id, name, custom_domain FROM sites
		WHERE custom_domain IS NOT NULL AND domain_verified_at IS NULL
		AND domain_bound_at < now() - interval '24 hours'
		FOR UPDATE
		)
		UPDATE sites s SET custom_domain = NULL, domain_status = NULL,
		domain_verified_at = NULL, domain_bound_at = NULL, domain_last_error = NULL
		FROM expired e WHERE s.id = e.id
		RETURNING e.id, e.user_id, e.name, e.custom_domain`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var released []SiteDomainInfo
	for rows.Next() {
		var info SiteDomainInfo
		if err := rows.Scan(&info.SiteID, &info.UserID, &info.Name, &info.Domain); err != nil {
			return nil, err
		}
		released = append(released, info)
	}
	return released, rows.Err()
}
