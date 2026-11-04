package db

import (
	"context"
	"database/sql"
	"github.com/vsriram/simple-host/internal/repository/sites"
	"time"
)

// CreateSite forwards to the sites repository.
func CreateSite(ctx context.Context, q Querier, userID, name, siteURL string, expiresAt *time.Time) (Site, error) {
	return sites.CreateSite(ctx, q, userID, name, siteURL, expiresAt)
}

// ListExpiredSites forwards to the sites repository.
func ListExpiredSites(ctx context.Context, db *sql.DB) ([]ExpiredSite, error) {
	return sites.ListExpiredSites(ctx, db)
}

// RenameSite forwards to the sites repository.
func RenameSite(ctx context.Context, q Querier, siteID, name, siteURL string) error {
	return sites.RenameSite(ctx, q, siteID, name, siteURL)
}

// DeleteSite forwards to the sites repository.
func DeleteSite(ctx context.Context, db Querier, siteID string) error {
	return sites.DeleteSite(ctx, db, siteID)
}

// SetSiteVisibility forwards to the sites repository.
func SetSiteVisibility(ctx context.Context, db *sql.DB, siteID, visibility string) error {
	return sites.SetSiteVisibility(ctx, db, siteID, visibility)
}

// SetSiteKeepVersions forwards to the sites repository.
func SetSiteKeepVersions(ctx context.Context, q Querier, siteID string, keep int) error {
	return sites.SetSiteKeepVersions(ctx, q, siteID, keep)
}

// SiteActiveVersion forwards to the sites repository.
func SiteActiveVersion(ctx context.Context, db Querier, siteID string) (int, error) {
	return sites.SiteActiveVersion(ctx, db, siteID)
}

// UpdateSiteSummary forwards to the sites repository.
func UpdateSiteSummary(ctx context.Context, q Querier, siteID string, s SiteSummary) error {
	return sites.UpdateSiteSummary(ctx, q, siteID, s)
}

// UpdateSiteSummaryIfUnset forwards to the sites repository.
func UpdateSiteSummaryIfUnset(ctx context.Context, q Querier, siteID string, s SiteSummary) error {
	return sites.UpdateSiteSummaryIfUnset(ctx, q, siteID, s)
}

// UpdateSiteActiveVersion forwards to the sites repository.
func UpdateSiteActiveVersion(ctx context.Context, db Querier, siteID string, version int) error {
	return sites.UpdateSiteActiveVersion(ctx, db, siteID, version)
}

// CreateVersion forwards to the sites repository.
func CreateVersion(ctx context.Context, db Querier, siteID string, versionNumber int, diskPath, archiveSHA256, author string) (Version, error) {
	return sites.CreateVersion(ctx, db, siteID, versionNumber, diskPath, archiveSHA256, author)
}

// ListVersionsBySite forwards to the sites repository.
func ListVersionsBySite(ctx context.Context, db *sql.DB, siteID string) ([]Version, error) {
	return sites.ListVersionsBySite(ctx, db, siteID)
}

// ActivateVersion forwards to the sites repository.
func ActivateVersion(ctx context.Context, db Querier, versionID string) error {
	return sites.ActivateVersion(ctx, db, versionID)
}

// MarkVersionReady forwards to the sites repository.
func MarkVersionReady(ctx context.Context, db Querier, versionID string) error {
	return sites.MarkVersionReady(ctx, db, versionID)
}

// ActivateVersionNumber forwards to the sites repository.
func ActivateVersionNumber(ctx context.Context, db Querier, siteID string, version int) error {
	return sites.ActivateVersionNumber(ctx, db, siteID, version)
}

// VersionKept forwards to the sites repository.
func VersionKept(ctx context.Context, db Querier, siteID string, version int) (bool, error) {
	return sites.VersionKept(ctx, db, siteID, version)
}

// LockSiteForUpdate forwards to the sites repository.
func LockSiteForUpdate(ctx context.Context, db Querier, siteID string) error {
	return sites.LockSiteForUpdate(ctx, db, siteID)
}

// GetMaxVersionNumber forwards to the sites repository.
func GetMaxVersionNumber(ctx context.Context, db Querier, siteID string) (int, error) {
	return sites.GetMaxVersionNumber(ctx, db, siteID)
}

// DiscardReadyVersion forwards to the sites repository.
func DiscardReadyVersion(ctx context.Context, q Querier, siteID string, n int) (bool, error) {
	return sites.DiscardReadyVersion(ctx, q, siteID, n)
}

// PruneVersions forwards to the sites repository.
func PruneVersions(ctx context.Context, db *sql.DB, siteID string, keepFrom, activeVersion int) ([]int, error) {
	return sites.PruneVersions(ctx, db, siteID, keepFrom, activeVersion)
}

// KeepOldSiteName forwards to the sites repository.
func KeepOldSiteName(ctx context.Context, q Querier, userID, siteID, oldName, newName string) error {
	return sites.KeepOldSiteName(ctx, q, userID, siteID, oldName, newName)
}

// DropOldSiteName forwards to the sites repository.
func DropOldSiteName(ctx context.Context, q Querier, userID, name string) error {
	return sites.DropOldSiteName(ctx, q, userID, name)
}
