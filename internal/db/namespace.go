package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
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
)

// SetPlatformDomain sets SITE_DOMAIN for the namespace checks. Empty (the
// default) disables them: an instance with no domain has no names to share.
func SetPlatformDomain(domain string) {
	platformDomainMu.Lock()
	platformDomain = strings.ToLower(strings.TrimSpace(domain))
	platformDomainMu.Unlock()
}

func currentPlatformDomain() string {
	platformDomainMu.RLock()
	defer platformDomainMu.RUnlock()
	return platformDomain
}

// ErrNameIsAccountAddress means the name is some account's own address.
var ErrNameIsAccountAddress = errors.New("that name is an account's address")

// lockPlatformName takes the namespace lock for host inside tx.
func lockPlatformName(ctx context.Context, tx *sql.Tx, host string) error {
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, host)
	return err
}

// handleNameTaken reports whether handle, as an address, is already used by a
// claimed site address, a legacy hostname, or another account's alias.
// Callers hold the namespace lock.
func handleNameTaken(ctx context.Context, tx *sql.Tx, userID, handle string) (bool, error) {
	domain := currentPlatformDomain()
	if domain == "" {
		return false, nil
	}
	host := strings.ToLower(handle) + "." + domain
	var taken bool
	err := tx.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM sites WHERE lower(custom_domain) = $1)
		    OR EXISTS (SELECT 1 FROM legacy_hostnames WHERE lower(hostname) = $1)
		    OR EXISTS (SELECT 1 FROM handle_aliases WHERE handle = $2 AND user_id::text <> $3)`,
		host, strings.ToLower(handle), userID).Scan(&taken)
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
// alias, so links that name the old handle keep resolving. Operator tool.
func RenameHandle(ctx context.Context, database *sql.DB, userID, newHandle string) (string, error) {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var old sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT handle FROM users WHERE id = $1 FOR UPDATE`, userID).Scan(&old); err != nil {
		return "", err
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
			ON CONFLICT (handle) DO UPDATE SET user_id = EXCLUDED.user_id`, old.String, userID); err != nil {
			return "", err
		}
	}
	// The new handle stops being anyone's alias.
	if _, err := tx.ExecContext(ctx, `DELETE FROM handle_aliases WHERE handle = $1`, newHandle); err != nil {
		return "", err
	}
	return old.String, tx.Commit()
}

// GetSiteOwner returns the owner's handle ("" if none), user id and the
// site's name for siteID. Returns sql.ErrNoRows for an unknown site.
func GetSiteOwner(ctx context.Context, q Querier, siteID string) (handle, userID, name string, err error) {
	err = q.QueryRowContext(ctx, `
		SELECT COALESCE(u.handle, ''), u.id::text, s.name
		FROM sites s JOIN users u ON u.id = s.user_id
		WHERE s.id::text = $1`, siteID).Scan(&handle, &userID, &name)
	return
}
