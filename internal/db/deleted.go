package db

import (
	"context"
	"database/sql"
	"time"

	"github.com/vsriram/simple-host/internal/repository/sites"
)

// Recently deleted (completeness plan, 2026-09-27).
//
// Deleting a site sets sites.deleted_at instead of removing the row, so its
// versions, saved state, collections, private-list settings and claimed names
// stay attached and its name stays held. Every lookup that serves, lists or
// resolves a site filters on deleted_at IS NULL; the functions here are the
// only ones that see deleted rows. PurgeDeletedSite removes one for good once
// the caller's retention limit (DELETED_RETENTION_DAYS) has passed.

// DeletedSite is one site in an account's Recently deleted list.
type DeletedSite = sites.DeletedSite

// MarkSiteDeleted forwards to the sites repository.
func MarkSiteDeleted(ctx context.Context, q Querier, siteID string, retention time.Duration) error {
	return sites.MarkSiteDeleted(ctx, q, siteID, retention)
}

// RestoreDeletedSite forwards to the sites repository.
func RestoreDeletedSite(ctx context.Context, q Querier, siteID string) error {
	return sites.RestoreDeletedSite(ctx, q, siteID)
}

// GetDeletedSiteByUser forwards to the sites repository.
func GetDeletedSiteByUser(ctx context.Context, q Querier, userID, name string) (DeletedSite, error) {
	return sites.GetDeletedSiteByUser(ctx, q, userID, name)
}

// ListDeletedSitesByUser forwards to the sites repository.
func ListDeletedSitesByUser(ctx context.Context, database *sql.DB, userID string) ([]DeletedSite, error) {
	return sites.ListDeletedSitesByUser(ctx, database, userID)
}

// ListPurgeableSites forwards to the sites repository.
func ListPurgeableSites(ctx context.Context, database *sql.DB, retention time.Duration) ([]DeletedSite, error) {
	return sites.ListPurgeableSites(ctx, database, retention)
}

// PurgeDeletedSite forwards to the sites repository.
func PurgeDeletedSite(ctx context.Context, database *sql.DB, siteID string, retention time.Duration) ([]string, error) {
	return sites.PurgeDeletedSite(ctx, database, siteID, retention)
}

func oneRow(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// DomainHeldByDeletedSite reports whether host is the own address (current or
// earlier) of a site in Recently deleted, which keeps it held until purge.
func DomainHeldByDeletedSite(ctx context.Context, q Querier, host string) (bool, error) {
	var held bool
	f := hostForms(host)
	err := q.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM sites WHERE deleted_at IS NOT NULL
		AND (custom_domain IN (lower($1), lower($2)) OR previous_domain IN (lower($1), lower($2))))`, f[0], f[1]).Scan(&held)
	return held, err
}
