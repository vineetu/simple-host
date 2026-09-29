package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"time"
)

// One namespace under the platform domain (owner decision 2026-09-25).
//
// A single-label name <label>.<SITE_DOMAIN> can mean four things: an account's
// own address (its handle), a site's claimed free address, a retired
// name-subdomain that still redirects (legacy_hostnames), or a reserved
// platform name (checked in the handler package). They are first come, first
// served against each other, and both directions are checked under the same
// transaction-scoped advisory lock ClaimPlatformSubdomain takes — keyed by
// the full host — so a handle and a claimed address can never land on the
// same name concurrently.

var (
	platformDomainMu sync.RWMutex
	platformDomain   string
	// platformAlso are the other domains whose one-label names are the same
	// names (SITE_DOMAIN and SITE_BASE_DOMAIN while addresses move between
	// them, docs/designs/site-base-domain-move.md). Usually empty.
	platformAlso []string
)

// SetPlatformDomain sets SITE_DOMAIN for the namespace checks. Empty (the
// default) disables them: an instance with no domain has no names to share.
func SetPlatformDomain(domain string) {
	SetPlatformDomains(domain)
}

// SetPlatformDomains sets the domain names are handed out under (primary:
// the handle address <handle>.<primary>, the namespace lock key) and every
// other domain whose one-label names are the same names: a name taken under
// one is taken under all, and a lookup of <label>.<any> finds a row stored
// under another.
func SetPlatformDomains(primary string, also ...string) {
	p := strings.ToLower(strings.TrimSpace(primary))
	var rest []string
	for _, d := range also {
		d = strings.ToLower(strings.TrimSpace(d))
		if d != "" && d != p && !containsString(rest, d) {
			rest = append(rest, d)
		}
	}
	if p == "" {
		rest = nil
	}
	platformDomainMu.Lock()
	platformDomain, platformAlso = p, rest
	platformDomainMu.Unlock()
}

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func currentPlatformDomain() string {
	platformDomainMu.RLock()
	defer platformDomainMu.RUnlock()
	return platformDomain
}

// platformDomains is the primary domain, then the others ("" primary: none).
func platformDomains() []string {
	platformDomainMu.RLock()
	defer platformDomainMu.RUnlock()
	if platformDomain == "" {
		return nil
	}
	return append([]string{platformDomain}, platformAlso...)
}

// platformNameLabel returns the label of a one-label host under any platform
// domain.
func platformNameLabel(host string) (string, bool) {
	host = strings.ToLower(strings.TrimSpace(host))
	for _, d := range platformDomains() {
		if !strings.HasSuffix(host, "."+d) {
			continue
		}
		if label := strings.TrimSuffix(host, "."+d); label != "" && !strings.Contains(label, ".") {
			return label, true
		}
	}
	return "", false
}

// hostForms is host and, for a one-label platform name, the same label under
// every other platform domain: every way one name can be stored. Always two
// entries (the second repeats the first when there is no other form), so
// queries keep one shape.
func hostForms(host string) [2]string {
	host = strings.ToLower(strings.TrimSpace(host))
	forms := [2]string{host, host}
	label, ok := platformNameLabel(host)
	if !ok {
		return forms
	}
	for _, d := range platformDomains() {
		if f := label + "." + d; f != host {
			forms[1] = f
			break
		}
	}
	return forms
}

// lockKey is the namespace lock key of host: a platform name under its
// primary form, so both forms of one name take the same lock.
func lockKey(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	if label, ok := platformNameLabel(host); ok {
		return label + "." + currentPlatformDomain()
	}
	return host
}

// ErrNameIsAccountAddress means the name is some account's own address.
var ErrNameIsAccountAddress = errors.New("that name is an account's address")

// lockPlatformName takes the namespace lock for host inside tx.
func lockPlatformName(ctx context.Context, tx *sql.Tx, host string) error {
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, lockKey(host))
	return err
}

// handleNameTaken reports whether handle, as an address, is already used by a
// claimed site address, a legacy hostname, another account's alias (or a
// retired one: a deleted account's handles keep user_id NULL and stay taken),
// or another account's live site of that name: an unclaimed <name>.<domain>
// still redirects to the oldest such site (LegacyHostRedirect), and a handle
// there would take those old links over.
// Callers hold the namespace lock.
func handleNameTaken(ctx context.Context, tx *sql.Tx, userID, handle string) (bool, error) {
	domain := currentPlatformDomain()
	if domain == "" {
		return false, nil
	}
	f := hostForms(strings.ToLower(handle) + "." + domain)
	var taken bool
	err := tx.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM sites WHERE lower(custom_domain) IN ($1, $4) OR lower(previous_domain) IN ($1, $4))
		    OR EXISTS (SELECT 1 FROM legacy_hostnames WHERE lower(hostname) IN ($1, $4))
		    OR EXISTS (SELECT 1 FROM handle_aliases WHERE handle = $2 AND user_id::text IS DISTINCT FROM $3)
		    OR EXISTS (SELECT 1 FROM sites WHERE name = $2 AND deleted_at IS NULL AND user_id::text IS DISTINCT FROM $3)`,
		f[0], strings.ToLower(handle), userID, f[1]).Scan(&taken)
	return taken, err
}

// HandleAvailable takes the namespace lock for handle inside tx and reports
// whether userID may take it as their handle (the unique index on users.handle
// still decides between two accounts).
func HandleAvailable(ctx context.Context, tx *sql.Tx, userID, handle string) (bool, error) {
	domain := currentPlatformDomain()
	if domain == "" {
		return true, nil
	}
	if err := lockPlatformName(ctx, tx, strings.ToLower(handle)+"."+domain); err != nil {
		return false, err
	}
	taken, err := handleNameTaken(ctx, tx, userID, handle)
	return !taken, err
}

// HandleInUse reports, without taking the namespace lock, whether handle is
// someone else's: another account's handle or alias (a retired one too), a
// site's claimed <handle>.<SITE_DOMAIN> or a retired name-subdomain. For the
// availability check and early refusals; a claim still decides under the lock.
// userID may be empty (nobody's own handle counts as free then).
func HandleInUse(ctx context.Context, q Querier, userID, handle string) (bool, error) {
	h := strings.ToLower(handle)
	var taken bool
	err := q.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM users WHERE handle = $1 AND id::text IS DISTINCT FROM $2)
		    OR EXISTS (SELECT 1 FROM handle_aliases WHERE handle = $1 AND user_id::text IS DISTINCT FROM $2)`,
		h, userID).Scan(&taken)
	if err != nil || taken {
		return taken, err
	}
	domain := currentPlatformDomain()
	if domain == "" {
		return false, nil
	}
	f := hostForms(h + "." + domain)
	err = q.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM sites WHERE lower(custom_domain) IN ($1, $4) OR lower(previous_domain) IN ($1, $4))
		    OR EXISTS (SELECT 1 FROM legacy_hostnames WHERE lower(hostname) IN ($1, $4))
		    OR EXISTS (SELECT 1 FROM sites WHERE name = $2 AND deleted_at IS NULL AND user_id::text IS DISTINCT FROM $3)`, f[0], h, userID, f[1]).Scan(&taken)
	return taken, err
}

// PlatformDomain is SITE_DOMAIN as the namespace checks see it ("" when off).
func PlatformDomain() string { return currentPlatformDomain() }

// platformNameIsHandle reports whether label is an account's handle or an
// old handle kept as an alias. Callers hold the namespace lock.
func platformNameIsHandle(ctx context.Context, tx *sql.Tx, label string) (bool, error) {
	var taken bool
	err := tx.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM users WHERE handle = $1)
		    OR EXISTS (SELECT 1 FROM handle_aliases WHERE handle = $1)`, strings.ToLower(label)).Scan(&taken)
	return taken, err
}

// ResolveHandleAlias returns the current handle of the account that used to
// be old, or sql.ErrNoRows.
func ResolveHandleAlias(ctx context.Context, q Querier, old string) (string, error) {
	var handle string
	err := q.QueryRowContext(ctx, `
		SELECT u.handle FROM handle_aliases a JOIN users u ON u.id = a.user_id
		WHERE a.handle = $1 AND u.handle IS NOT NULL`, strings.ToLower(old)).Scan(&handle)
	return handle, err
}

// GetUserByHandleOrAlias looks a handle up, falling back to an old handle kept
// as an alias. Returns sql.ErrNoRows when neither exists.
func GetUserByHandleOrAlias(ctx context.Context, database *sql.DB, handle string) (User, error) {
	u, err := GetUserByHandle(ctx, database, handle)
	if !errors.Is(err, sql.ErrNoRows) {
		return u, err
	}
	current, aerr := ResolveHandleAlias(ctx, database, handle)
	if aerr != nil {
		return User{}, err
	}
	return GetUserByHandle(ctx, database, current)
}

// RenameHandle moves an account to a new handle and keeps the old one as an
// alias, so links that name the old handle keep resolving.
func RenameHandle(ctx context.Context, database *sql.DB, userID, newHandle string) (string, error) {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	old, err := RenameHandleTx(ctx, tx, userID, newHandle)
	if err != nil {
		return "", err
	}
	return old, tx.Commit()
}

// RenameHandleTx is RenameHandle inside the caller's transaction (PATCH
// /v1/me, which already holds the account row). ErrDomainTaken when the new
// handle is not free as an address.
func RenameHandleTx(ctx context.Context, tx *sql.Tx, userID, newHandle string) (string, error) {
	var old sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT handle FROM users WHERE id = $1 FOR UPDATE`, userID).Scan(&old); err != nil {
		return "", err
	}
	// The old handle becomes an alias, a name in the shared namespace: hold its
	// lock too (both in one order, so two renames never wait on each other in
	// a circle), so a claim of <old>.<SITE_DOMAIN> cannot slip in between.
	if domain := currentPlatformDomain(); domain != "" && old.Valid && old.String != "" {
		names := []string{strings.ToLower(old.String), strings.ToLower(newHandle)}
		if names[1] < names[0] {
			names[0], names[1] = names[1], names[0]
		}
		for _, n := range names {
			if err := lockPlatformName(ctx, tx, n+"."+domain); err != nil {
				return "", err
			}
		}
	}
	ok, err := HandleAvailable(ctx, tx, userID, newHandle)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", ErrDomainTaken
	}
	if _, err := tx.ExecContext(ctx, `UPDATE users SET handle = $2, handle_changed_at = now() WHERE id = $1`, userID, newHandle); err != nil {
		return "", err
	}
	if old.Valid && old.String != "" && old.String != newHandle {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO handle_aliases (handle, user_id) VALUES ($1, $2)
			ON CONFLICT (handle) DO UPDATE SET user_id = EXCLUDED.user_id, created_at = now()`, old.String, userID); err != nil {
			return "", err
		}
	}
	// The new handle stops being anyone's alias.
	// Only this account's own old alias: another account's alias is never
	// removed here (with namespace checks off, HandleAvailable does not stop it).
	if _, err := tx.ExecContext(ctx, `DELETE FROM handle_aliases WHERE handle = $1 AND user_id = $2`, newHandle, userID); err != nil {
		return "", err
	}
	return old.String, nil
}

// HandleRenamedSince reports whether the account moved to a new handle after
// publishing (a rename that left an alias behind) since t. Handle changes
// before anything is published leave no alias and do not count.
func HandleRenamedSince(ctx context.Context, q Querier, userID string, t time.Time) (bool, time.Time, error) {
	var last sql.NullTime
	err := q.QueryRowContext(ctx, `
		SELECT u.handle_changed_at FROM users u
		WHERE u.id = $1 AND u.handle_changed_at > $2
		  AND EXISTS (SELECT 1 FROM handle_aliases a WHERE a.user_id = u.id AND a.created_at > $2)`, userID, t).Scan(&last)
	if errors.Is(err, sql.ErrNoRows) {
		return false, time.Time{}, nil
	}
	if err != nil {
		return false, time.Time{}, err
	}
	return true, last.Time, nil
}

// GetSiteOwner returns the owner's handle ("" if none), user id and the
// site's name for siteID. Returns sql.ErrNoRows for an unknown site.
func GetSiteOwner(ctx context.Context, q Querier, siteID string) (handle, userID, name string, err error) {
	err = q.QueryRowContext(ctx, `
		SELECT COALESCE(u.handle, ''), u.id::text, s.name
		FROM sites s JOIN users u ON u.id = s.user_id
		WHERE s.id::text = $1 AND s.deleted_at IS NULL`, siteID).Scan(&handle, &userID, &name)
	return
}

// ListHandlesWithSites returns every account handle that owns at least one
// site: the people who need a certificate for their site hosts.
func ListHandlesWithSites(ctx context.Context, q *sql.DB) ([]string, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT DISTINCT u.handle FROM users u JOIN sites s ON s.user_id = u.id AND s.deleted_at IS NULL
		WHERE u.handle IS NOT NULL AND u.handle <> ''
		ORDER BY u.handle`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}
