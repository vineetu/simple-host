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
	// PreviousDomain is the site's earlier proven address, still served while
	// Domain is pending (cleared once Domain is verified).
	PreviousDomain string
	// CertStatus is pending | issuing | live | failed ("" = not known yet).
	CertStatus   string
	FailingSince sql.NullTime
	// Token is the site's ownership token: the value of the DNS TXT record
	// _simple-host.<domain> that proves a custom domain is the owner's.
	Token string
	// ProofExempt: Domain was verified before the TXT proof existed and keeps
	// working without it while it stays verified.
	ProofExempt bool
}

// domainColumns is the column list scanDomainInfo reads, in order.
const domainColumns = `id, user_id, name, COALESCE(custom_domain, ''), COALESCE(domain_status, ''),
	domain_verified_at, domain_bound_at, COALESCE(domain_last_error, ''),
	COALESCE(previous_domain, ''), COALESCE(domain_cert_status, ''), domain_failing_since,
	COALESCE(domain_token, ''), COALESCE(custom_domain = ANY(domain_proof_exempt), false)`

func scanDomainInfo(row interface{ Scan(...any) error }) (SiteDomainInfo, error) {
	var info SiteDomainInfo
	err := row.Scan(&info.SiteID, &info.UserID, &info.Name, &info.Domain, &info.Status,
		&info.VerifiedAt, &info.BoundAt, &info.LastError,
		&info.PreviousDomain, &info.CertStatus, &info.FailingSince,
		&info.Token, &info.ProofExempt)
	return info, err
}

// SetCustomDomain binds domain to siteID with status "pending" and clears any
// prior verification. A proven address the site already had is kept as
// previous_domain, so the site keeps serving there until the new domain is
// verified (PromoteDomain); an earlier pending domain is simply replaced.
func SetCustomDomain(ctx context.Context, database Querier, siteID, domain string) error {
	const query = `
		UPDATE sites
		SET previous_domain = CASE
		        WHEN custom_domain IS NOT NULL AND domain_verified_at IS NOT NULL AND custom_domain <> $2 THEN custom_domain
		        WHEN previous_domain = $2 THEN NULL
		        ELSE previous_domain END,
		    custom_domain = $2,
		    domain_status = 'pending',
		    domain_bound_at = now(),
		    domain_last_error = NULL,
		    domain_verified_at = NULL,
		    domain_cert_status = NULL,
		    domain_failing_since = NULL,
		    domain_lapse_notified_at = NULL,
		    previous_domain_failing_since = NULL,
		    domain_token = COALESCE(domain_token, 'sh-' || replace(gen_random_uuid()::text, '-', ''))
		WHERE id = $1
	`
	_, err := database.ExecContext(ctx, query, siteID, domain)
	return err
}

// restorePreviousSet is the SET list that drops custom_domain and puts the
// site's previous proven address (if any) back as its domain.
const restorePreviousSet = `
	custom_domain = previous_domain,
	domain_status = CASE WHEN previous_domain IS NULL THEN NULL ELSE 'active' END,
	domain_bound_at = CASE WHEN previous_domain IS NULL THEN NULL ELSE now() END,
	domain_verified_at = CASE WHEN previous_domain IS NULL THEN NULL ELSE now() END,
	domain_cert_status = CASE WHEN previous_domain IS NULL THEN NULL ELSE 'live' END,
	domain_last_error = NULL,
	domain_failing_since = NULL,
	domain_lapse_notified_at = NULL,
	previous_domain = NULL,
	previous_domain_failing_since = NULL`

// DropCustomDomain unbinds siteID's current domain. When the site still had
// an earlier proven address (the dropped domain was pending), that address
// becomes its domain again. A dropped claimed <name>.<SITE_DOMAIN> stays with
// the site as a retired name. Returns the dropped and the restored domain.
// With expected set, only that domain is dropped: when the site's domain is
// something else by now, nothing changes and dropped is "".
func DropCustomDomain(ctx context.Context, database *sql.DB, siteID, expected string) (dropped, restored string, err error) {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return "", "", err
	}
	defer tx.Rollback()
	dropped, restored, err = dropCustomDomain(ctx, tx, siteID, expected)
	if err != nil {
		return "", "", err
	}
	return dropped, restored, tx.Commit()
}

func dropCustomDomain(ctx context.Context, tx *sql.Tx, siteID, expected string) (dropped, restored string, err error) {
	var userID string
	err = tx.QueryRowContext(ctx, `
		WITH old AS (SELECT id, custom_domain FROM sites WHERE id = $1 AND ($2 = '' OR custom_domain = $2) FOR UPDATE)
		UPDATE sites s SET `+restorePreviousSet+`
		FROM old WHERE s.id = old.id
		RETURNING COALESCE(old.custom_domain, ''), COALESCE(s.custom_domain, ''), s.user_id`, siteID, expected).Scan(&dropped, &restored, &userID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", nil
	}
	if err != nil {
		return "", "", err
	}
	if err := retirePlatformName(ctx, tx, dropped, siteID, userID); err != nil {
		return "", "", err
	}
	return dropped, restored, nil
}

// GetSiteDomainInfo returns domain binding info for siteID. ok is false when
// the site has no custom_domain set (or the site row is missing).
func GetSiteDomainInfo(ctx context.Context, database *sql.DB, siteID string) (SiteDomainInfo, bool, error) {
	info, err := scanDomainInfo(database.QueryRowContext(ctx, `SELECT `+domainColumns+` FROM sites WHERE id = $1`, siteID))
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

// GetSiteByCustomDomain looks up the site a host serves as its own domain
// (lowercased match): its bound custom domain, or the earlier proven address
// it keeps serving at while a new domain is pending. For the latter, Domain is
// that earlier address and the binding counts as verified. Returns
// sql.ErrNoRows when none.
func GetSiteByCustomDomain(ctx context.Context, database *sql.DB, domain string) (SiteDomainInfo, error) {
	domain = strings.ToLower(strings.TrimSpace(domain))
	info, err := scanDomainInfo(database.QueryRowContext(ctx, `SELECT `+domainColumns+` FROM sites WHERE custom_domain = $1 AND deleted_at IS NULL`, domain))
	if !errors.Is(err, sql.ErrNoRows) {
		return info, err
	}
	info, err = scanDomainInfo(database.QueryRowContext(ctx, `SELECT `+domainColumns+` FROM sites WHERE previous_domain = $1 AND deleted_at IS NULL`, domain))
	if err != nil {
		return SiteDomainInfo{}, err
	}
	return info.AsPrevious(), nil
}

// AsPrevious is the view of the site's earlier proven address: that address
// as its domain, active and verified.
func (info SiteDomainInfo) AsPrevious() SiteDomainInfo {
	prev := info
	prev.Domain = info.PreviousDomain
	prev.Status = "active"
	prev.LastError = ""
	prev.CertStatus = "live"
	prev.FailingSince = sql.NullTime{}
	if !prev.VerifiedAt.Valid {
		prev.VerifiedAt = sql.NullTime{Time: time.Now(), Valid: true}
	}
	prev.PreviousDomain = ""
	return prev
}

// BoundDomain is one bound custom domain and the status it currently reports.
type BoundDomain struct {
	SiteID         string
	Domain         string
	Status         string
	Verified       bool
	PreviousDomain string
	UserID         string
	Name           string
	// Token is the site's ownership token (TXT _simple-host.<domain>).
	Token string
	// ProofExempt: verified before the TXT proof existed (see SiteDomainInfo).
	ProofExempt bool
	// Suspended: the site or its owner's account is taken down; it gets no
	// checks and no certificate.
	Suspended bool
}

// BoundDomainOf is the check view of a site's binding.
func BoundDomainOf(info SiteDomainInfo) BoundDomain {
	return BoundDomain{SiteID: info.SiteID, Domain: info.Domain, Status: info.Status, Verified: info.VerifiedAt.Valid,
		PreviousDomain: info.PreviousDomain, UserID: info.UserID, Name: info.Name, Token: info.Token, ProofExempt: info.ProofExempt}
}

// ListDomainsToCheck returns bound domains due for verification: every binding
// that is not "active" on every pass, plus active ones whose last proof is older
// than activeAge — an active domain that stops serving has to be able to fall
// back to error/pending, so "active" is a claim with an expiry, not a latch.
func ListDomainsToCheck(ctx context.Context, database *sql.DB, activeAge time.Duration) ([]BoundDomain, error) {
	const query = `
		SELECT s.id, s.custom_domain, COALESCE(s.domain_status, ''), s.domain_verified_at IS NOT NULL, COALESCE(s.previous_domain, ''),
		       s.user_id, s.name, COALESCE(s.domain_token, ''), COALESCE(s.custom_domain = ANY(s.domain_proof_exempt), false),
		       s.suspended_at IS NOT NULL OR u.suspended_at IS NOT NULL
		FROM sites s JOIN users u ON u.id = s.user_id
		WHERE s.custom_domain IS NOT NULL
		  AND s.custom_domain <> ''
		  AND s.deleted_at IS NULL
		  AND (s.domain_status IS DISTINCT FROM 'active'
		       OR s.domain_verified_at IS NULL
		       OR s.domain_verified_at < now() - ($1 * interval '1 second'))
		ORDER BY s.custom_domain
	`
	rows, err := database.QueryContext(ctx, query, int64(activeAge.Seconds()))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []BoundDomain
	for rows.Next() {
		var d BoundDomain
		if err := rows.Scan(&d.SiteID, &d.Domain, &d.Status, &d.Verified, &d.PreviousDomain,
			&d.UserID, &d.Name, &d.Token, &d.ProofExempt, &d.Suspended); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// DomainCheck is what SetDomainStatus reports back about a verified domain
// that is failing: since when, and whether its owner has been told.
type DomainCheck struct {
	UserID       string
	Name         string
	FailingSince sql.NullTime
	Notified     bool
}

// SetDomainStatus records one check of siteID's bound domain: status
// (pending | active | error), the reason it is not active, and the
// certificate status. "active" (re)sets domain_verified_at and clears any
// failure; a verified domain that fails starts (or keeps) its failing_since
// clock. Nothing is written when the site's domain is no longer domain.
func SetDomainStatus(ctx context.Context, database *sql.DB, siteID, domain, status, lastErr, certStatus string) (DomainCheck, error) {
	const query = `
		UPDATE sites
		SET domain_status = $3,
		    domain_last_error = NULLIF($4, ''),
		    domain_cert_status = NULLIF($5, ''),
		    domain_verified_at = CASE WHEN $3 = 'active' THEN now() ELSE domain_verified_at END,
		    domain_failing_since = CASE
		        WHEN $3 = 'active' OR domain_verified_at IS NULL THEN NULL
		        ELSE COALESCE(domain_failing_since, now()) END,
		    domain_lapse_notified_at = CASE WHEN $3 = 'active' THEN NULL ELSE domain_lapse_notified_at END
		WHERE id = $1 AND custom_domain = $2
		RETURNING user_id, name, domain_failing_since, domain_lapse_notified_at IS NOT NULL
	`
	var c DomainCheck
	err := database.QueryRowContext(ctx, query, siteID, domain, status, lastErr, certStatus).Scan(&c.UserID, &c.Name, &c.FailingSince, &c.Notified)
	if errors.Is(err, sql.ErrNoRows) {
		return DomainCheck{}, nil
	}
	return c, err
}

// MarkDomainLapseNotified records that the owner was emailed about their
// failing domain.
func MarkDomainLapseNotified(ctx context.Context, database *sql.DB, siteID, domain string) error {
	_, err := database.ExecContext(ctx, `UPDATE sites SET domain_lapse_notified_at = now() WHERE id = $1 AND custom_domain = $2`, siteID, domain)
	return err
}

// LapseDomain lets a long-failing verified domain go completely: the site
// no longer has it (an earlier proven address, if any, comes back), its
// grandfathering ends, and the caller unbinds its link so the issuer drops
// the server and certificate. Nobody can inherit a live server; connecting it
// again (by anyone) needs the TXT ownership proof. False when domain is no
// longer the site's verified domain.
func LapseDomain(ctx context.Context, database *sql.DB, siteID, domain string) (bool, error) {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var userID string
	err = tx.QueryRowContext(ctx, `
		UPDATE sites SET `+restorePreviousSet+`,
		    domain_proof_exempt = array_remove(domain_proof_exempt, $2)
		WHERE id = $1 AND custom_domain = $2 AND domain_verified_at IS NOT NULL
		RETURNING user_id`, siteID, domain).Scan(&userID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := retirePlatformName(ctx, tx, domain, siteID, userID); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// SetPreviousDomainCheck records one check of the earlier address siteID
// still serves while its new domain is pending: ok clears its failing clock, a
// failure starts (or keeps) it. Returns since when it has been failing (not
// valid when it passed, or when prev is no longer the site's earlier address).
func SetPreviousDomainCheck(ctx context.Context, database *sql.DB, siteID, prev string, ok bool) (sql.NullTime, error) {
	var since sql.NullTime
	err := database.QueryRowContext(ctx, `
		UPDATE sites SET previous_domain_failing_since = CASE WHEN $3 THEN NULL ELSE COALESCE(previous_domain_failing_since, now()) END
		WHERE id = $1 AND previous_domain = $2
		RETURNING previous_domain_failing_since`, siteID, prev, ok).Scan(&since)
	if errors.Is(err, sql.ErrNoRows) {
		return sql.NullTime{}, nil
	}
	return since, err
}

// LapsePreviousDomain lets go of an earlier address that has stopped
// answering: the site no longer serves there, and whoever really holds the
// domain now can connect it. A claimed <name>.<SITE_DOMAIN> stays with the
// site as a retired name. False when prev is no longer the site's earlier
// address.
func LapsePreviousDomain(ctx context.Context, database *sql.DB, siteID, prev string) (bool, error) {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var userID string
	err = tx.QueryRowContext(ctx, `
		UPDATE sites SET previous_domain = NULL, previous_domain_failing_since = NULL
		WHERE id = $1 AND previous_domain = $2 RETURNING user_id`, siteID, prev).Scan(&userID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := retirePlatformName(ctx, tx, prev, siteID, userID); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// PromoteDomain finishes a switch of address once the new domain is verified:
// the site's earlier address is let go (a claimed <name>.<SITE_DOMAIN> stays
// with the site as a retired name that redirects). Returns that earlier
// address, or "".
func PromoteDomain(ctx context.Context, database *sql.DB, siteID string) (string, error) {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var prev, userID string
	err = tx.QueryRowContext(ctx, `
		WITH old AS (SELECT id, previous_domain FROM sites WHERE id = $1 AND previous_domain IS NOT NULL AND domain_verified_at IS NOT NULL FOR UPDATE)
		UPDATE sites s SET previous_domain = NULL, previous_domain_failing_since = NULL FROM old WHERE s.id = old.id
		RETURNING old.previous_domain, s.user_id`, siteID).Scan(&prev, &userID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if err := retirePlatformName(ctx, tx, prev, siteID, userID); err != nil {
		return "", err
	}
	return prev, tx.Commit()
}

// ErrDomainTaken means another site has already proved this domain.
var ErrDomainTaken = errors.New("domain is connected to another site")

// BindCustomDomain releases an unproven holder and binds the requester atomically.
// Lock the holder so verification cannot change the takeover decision mid-transaction.
// The returned holder's Domain is what it lost; its PreviousDomain is the
// address it was given back (if any). replaced is the requester's own pending
// domain this bind dropped, read under the same lock (so a concurrent change
// of the site's domain cannot make the caller unbind the wrong one).
func BindCustomDomain(ctx context.Context, database *sql.DB, siteID, domain string) (*SiteDomainInfo, string, error) {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return nil, "", err
	}
	defer tx.Rollback()
	// Serialize binds for this name even when no holder row exists yet.
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, domain); err != nil {
		return nil, "", err
	}
	var cur string
	var curVerified bool
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(custom_domain, ''), domain_verified_at IS NOT NULL FROM sites WHERE id = $1 FOR UPDATE`, siteID).Scan(&cur, &curVerified); err != nil {
		return nil, "", err
	}
	replaced := ""
	if cur != "" && cur != domain && !curVerified {
		replaced = cur
	}
	// A proven earlier address of another site, still serving it, is its own.
	var keptElsewhere bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM sites WHERE previous_domain = $1 AND id <> $2)`, domain, siteID).Scan(&keptElsewhere); err != nil {
		return nil, "", err
	}
	if keptElsewhere {
		return nil, "", ErrDomainTaken
	}
	var holder SiteDomainInfo
	err = tx.QueryRowContext(ctx, `
		SELECT s.id, s.user_id, s.name, s.custom_domain, s.domain_verified_at, COALESCE(u.handle, ''), COALESCE(s.domain_cert_status, '')
		FROM sites s JOIN users u ON u.id = s.user_id
		WHERE s.custom_domain = $1 FOR UPDATE OF s`, domain).Scan(
		&holder.SiteID, &holder.UserID, &holder.Name, &holder.Domain, &holder.VerifiedAt, &holder.Handle, &holder.CertStatus)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, "", err
	}
	var released *SiteDomainInfo
	if err == nil && holder.SiteID != siteID {
		// Proven, or past its ownership proof (a certificate is asked for
		// only once the holder's TXT record matched): not up for grabs.
		if holder.VerifiedAt.Valid || (holder.CertStatus != "" && holder.CertStatus != "pending") {
			return nil, "", ErrDomainTaken
		}
		_, restored, err := dropCustomDomain(ctx, tx, holder.SiteID, domain)
		if err != nil {
			return nil, "", err
		}
		holder.PreviousDomain = restored
		released = &holder
	}
	if err := SetCustomDomain(ctx, tx, siteID, domain); err != nil {
		return nil, "", err
	}
	if err := tx.Commit(); err != nil {
		return nil, "", err
	}
	return released, replaced, nil
}

// UnprovenDomainMaxAge is how long a binding whose DNS points here may stay
// unproven (its certificate keeps failing, or HTTPS never answers) before it
// is released like any other unproven binding.
const UnprovenDomainMaxAge = 7 * 24 * time.Hour

// ReleaseExpiredDomains clears bindings that were never proven within 24
// hours, unless DNS already points here (the certificate status is past
// "pending": it is being issued, is live, or failed and will be retried); those
// get UnprovenDomainMaxAge. A site that still had an earlier proven address
// gets it back; it is returned as the released row's PreviousDomain.
func ReleaseExpiredDomains(ctx context.Context, database *sql.DB) ([]SiteDomainInfo, error) {
	rows, err := database.QueryContext(ctx, `
		WITH expired AS (
		SELECT id, user_id, name, custom_domain FROM sites
		WHERE custom_domain IS NOT NULL AND domain_verified_at IS NULL AND deleted_at IS NULL
		AND ((domain_bound_at < now() - interval '24 hours' AND COALESCE(domain_cert_status, 'pending') = 'pending')
		     OR domain_bound_at < now() - ($1 * interval '1 second'))
		FOR UPDATE
		)
		UPDATE sites s SET `+restorePreviousSet+`
		FROM expired e WHERE s.id = e.id
		RETURNING e.id, e.user_id, e.name, e.custom_domain, COALESCE(s.custom_domain, '')`, int64(UnprovenDomainMaxAge.Seconds()))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var released []SiteDomainInfo
	for rows.Next() {
		var info SiteDomainInfo
		if err := rows.Scan(&info.SiteID, &info.UserID, &info.Name, &info.Domain, &info.PreviousDomain); err != nil {
			return nil, err
		}
		released = append(released, info)
	}
	return released, rows.Err()
}

// retirePlatformName keeps a released claimed <name>.<SITE_DOMAIN> with its
// site: it is recorded in legacy_hostnames, so the name redirects to the
// site's current address (or says the site was removed) instead of passing to
// whichever site happens to share its name, and nobody else can claim it.
// Any other host is ignored.
func retirePlatformName(ctx context.Context, q Querier, host, siteID, userID string) error {
	if !isPlatformName(host) {
		return nil
	}
	_, err := q.ExecContext(ctx, `
		INSERT INTO legacy_hostnames (hostname, site_id, user_id) VALUES (lower($1), $2, $3)
		ON CONFLICT (hostname) DO UPDATE SET site_id = EXCLUDED.site_id, user_id = EXCLUDED.user_id`,
		host, siteID, userID)
	return err
}

// RetireSiteNames records the site's claimed <name>.<SITE_DOMAIN> addresses
// (current and earlier) as retired names before the site is deleted; the
// rows outlive it (site_id becomes NULL). Returns every domain the site had,
// for the caller to unbind on disk.
func RetireSiteNames(ctx context.Context, q Querier, siteID string) ([]string, error) {
	var userID, cur, prev string
	err := q.QueryRowContext(ctx, `SELECT user_id, COALESCE(custom_domain, ''), COALESCE(previous_domain, '') FROM sites WHERE id = $1`, siteID).Scan(&userID, &cur, &prev)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, d := range []string{cur, prev} {
		if d == "" {
			continue
		}
		out = append(out, d)
		if err := retirePlatformName(ctx, q, d, siteID, userID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// RetiredName is a released <name>.<SITE_DOMAIN>: the site it belongs to, or
// SiteID "" when that site was deleted.
type RetiredName struct {
	SiteID string
}

// GetRetiredName looks host up in legacy_hostnames. sql.ErrNoRows if absent.
func GetRetiredName(ctx context.Context, database *sql.DB, host string) (RetiredName, error) {
	var r RetiredName
	var siteID sql.NullString
	err := database.QueryRowContext(ctx, `SELECT site_id::text FROM legacy_hostnames WHERE hostname = lower($1)`, host).Scan(&siteID)
	r.SiteID = siteID.String
	return r, err
}

// isPlatformName: host is exactly one label under the platform domain.
func isPlatformName(host string) bool {
	domain := currentPlatformDomain()
	host = strings.ToLower(strings.TrimSpace(host))
	if domain == "" || !strings.HasSuffix(host, "."+domain) {
		return false
	}
	label := strings.TrimSuffix(host, "."+domain)
	return label != "" && !strings.Contains(label, ".")
}

// GetSiteOwnerEmail returns the sign-in address of the site's owner.
func GetSiteOwnerEmail(ctx context.Context, database *sql.DB, siteID string) (string, error) {
	var email string
	err := database.QueryRowContext(ctx, `SELECT u.username FROM sites s JOIN users u ON u.id = s.user_id WHERE s.id = $1`, siteID).Scan(&email)
	return email, err
}

// ClaimPlatformSubdomain binds a free <name>.<SITE_DOMAIN> address to siteID,
// verified at once (the platform owns the zone, so there is nothing to prove).
// First come, first served: any other site holding the name, proven or not,
// makes this fail with ErrDomainTaken — a platform name is never taken over.
// A name retired from one of the same account's sites may be claimed again.
// Returns the addresses the site had before (current and earlier), which it
// no longer serves; a claimed name among them stays with it as retired.
func ClaimPlatformSubdomain(ctx context.Context, database *sql.DB, siteID, host string) ([]string, error) {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := lockPlatformName(ctx, tx, host); err != nil {
		return nil, err
	}
	// One namespace: an account's own address or a retired name-subdomain of
	// another account is not free to claim.
	if label, _, ok := strings.Cut(host, "."); ok {
		isHandle, err := platformNameIsHandle(ctx, tx, label)
		if err != nil {
			return nil, err
		}
		if isHandle {
			return nil, ErrNameIsAccountAddress
		}
	}
	var userID, cur, prev string
	if err := tx.QueryRowContext(ctx, `SELECT user_id, COALESCE(custom_domain, ''), COALESCE(previous_domain, '') FROM sites WHERE id = $1 FOR UPDATE`, siteID).Scan(&userID, &cur, &prev); err != nil {
		return nil, err
	}
	var legacyOther bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM legacy_hostnames WHERE lower(hostname) = lower($1)
		    AND site_id IS DISTINCT FROM $2::uuid AND user_id IS DISTINCT FROM $3::uuid)`, host, siteID, userID).Scan(&legacyOther); err != nil {
		return nil, err
	}
	if legacyOther {
		return nil, ErrDomainTaken
	}
	var holder string
	err = tx.QueryRowContext(ctx, `SELECT id FROM sites WHERE (custom_domain = $1 OR previous_domain = $1) AND id <> $2 LIMIT 1 FOR UPDATE`, host, siteID).Scan(&holder)
	switch {
	case err == nil:
		return nil, ErrDomainTaken
	case !errors.Is(err, sql.ErrNoRows):
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE sites
		SET custom_domain = $2,
		    domain_status = 'active',
		    domain_bound_at = now(),
		    domain_verified_at = now(),
		    domain_last_error = NULL,
		    domain_cert_status = NULL,
		    domain_failing_since = NULL,
		    domain_lapse_notified_at = NULL,
		    previous_domain = NULL,
		    previous_domain_failing_since = NULL
		WHERE id = $1`, siteID, host); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM legacy_hostnames WHERE lower(hostname) = lower($1)`, host); err != nil {
		return nil, err
	}
	var released []string
	for _, d := range []string{cur, prev} {
		if d == "" || strings.EqualFold(d, host) {
			continue
		}
		released = append(released, d)
		if err := retirePlatformName(ctx, tx, d, siteID, userID); err != nil {
			return nil, err
		}
	}
	return released, tx.Commit()
}

// DomainCertDailyCap is how many new custom-domain certificates one account
// may ask for in a rolling day.
const DomainCertDailyCap = 5

// AllowDomainCertRequest records that userID asks for a certificate for
// domain, unless that would be more than DomainCertDailyCap different domains
// in the last 24 hours. Asking again for a domain already asked for within
// the day is always allowed and not counted twice.
func AllowDomainCertRequest(ctx context.Context, database *sql.DB, userID, domain string) (bool, error) {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('domain-cert:' || $1, 0))`, userID); err != nil {
		return false, err
	}
	var again bool
	var n int
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(bool_or(domain = $2), false), count(DISTINCT domain)
		FROM domain_cert_requests WHERE user_id = $1 AND requested_at > now() - interval '24 hours'`, userID, domain).Scan(&again, &n); err != nil {
		return false, err
	}
	if again {
		return true, nil
	}
	if n >= DomainCertDailyCap {
		return false, nil
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO domain_cert_requests (user_id, domain) VALUES ($1, $2)`, userID, domain); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// PruneDomainCertRequests forgets requests older than two days.
func PruneDomainCertRequests(ctx context.Context, database *sql.DB) error {
	_, err := database.ExecContext(ctx, `DELETE FROM domain_cert_requests WHERE requested_at < now() - interval '2 days'`)
	return err
}

// DomainBoundToOtherSite reports whether a site other than siteID (any
// account's, Recently deleted included) has domain as its custom domain or
// the earlier one it still serves: a www / bare partner that is not this
// site's to redirect.
func DomainBoundToOtherSite(ctx context.Context, q Querier, domain, siteID string) (bool, error) {
	var bound bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM sites
		 WHERE id::text <> $2 AND (lower(custom_domain) = lower($1) OR lower(previous_domain) = lower($1)))`, domain, siteID).Scan(&bound)
	return bound, err
}
