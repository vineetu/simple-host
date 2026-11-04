package db

import (
	"context"
	"database/sql"
	"github.com/vsriram/simple-host/internal/model"
	"strings"

	storeaccounts "github.com/vsriram/simple-host/internal/repository/accounts"
	storesites "github.com/vsriram/simple-host/internal/repository/sites"
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

// ErrNameIsAccountAddress means the name is some account's own address.
var ErrNameIsAccountAddress = model.ErrNameIsAccountAddress

// HandleInUse reports, without taking the namespace lock, whether handle is
// someone else's: another account's handle or alias (a retired one too), a
// site's claimed <handle>.<SITE_DOMAIN> or a retired name-subdomain. For the
// availability check and early refusals; a claim still decides under the lock.
// userID may be empty (nobody's own handle counts as free then).
func HandleInUse(ctx context.Context, q Querier, userID, handle string) (bool, error) {
	return storeaccounts.HandleInUse(ctx, q, userID, handle)
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
	return storeaccounts.RenameHandleTx(ctx, tx, userID, newHandle)
}

// GetSiteOwner returns the owner's handle ("" if none), user id and the
// site's name for siteID. Returns sql.ErrNoRows for an unknown site.
func GetSiteOwner(ctx context.Context, q Querier, siteID string) (handle, userID, name string, err error) {
	return storesites.GetSiteOwner(ctx, q, siteID)
}

// ListHandlesWithSites returns every account handle that owns at least one
// site: the people who need a certificate for their site hosts.
func ListHandlesWithSites(ctx context.Context, q *sql.DB) ([]string, error) {
	return storesites.ListHandlesWithSites(ctx, q)
}
