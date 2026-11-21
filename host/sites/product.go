package sites

import (
	"context"
	"database/sql"

	accountsrepo "github.com/vsriram/simple-host/host/access/repo"

	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sites/repo"
	"github.com/vsriram/simple-host/host/sqlkit"
)

// What a product (Simple Hack) reads of an account's sites. A product never
// queries the platform's site tables itself: it calls these.

// SiteByName loads the account's live (not deleted) site name.
func SiteByName(ctx context.Context, db *sql.DB, accountID, name string) (model.Site, error) {
	return repo.GetSiteByUser(ctx, db, accountID, name)
}

// SiteVersions lists a site's versions.
func SiteVersions(ctx context.Context, db *sql.DB, siteID string) ([]model.Version, error) {
	return repo.ListVersionsBySite(ctx, db, siteID)
}

// ActiveSiteVersion is the version of a site that is live.
func ActiveSiteVersion(ctx context.Context, db *sql.DB, siteID string) (model.Version, error) {
	return repo.GetActiveSiteVersion(ctx, db, siteID)
}

// SitePasscode reads a site's passcode settings.
func SitePasscode(ctx context.Context, q sqlkit.Querier, siteID string) (model.SitePasscodeRow, error) {
	return accountsrepo.GetSitePasscode(ctx, q, siteID)
}

// AccountSites lists the account's live sites.
func AccountSites(ctx context.Context, db *sql.DB, accountID string) ([]model.Site, error) {
	return repo.ListSitesByUser(ctx, db, accountID)
}

// SiteOwner is the account that holds a live site, and the site's name.
func SiteOwner(ctx context.Context, q sqlkit.Querier, siteID string) (accountID, name string, err error) {
	_, accountID, name, err = repo.GetSiteOwner(ctx, q, siteID)
	return accountID, name, err
}

// LiveSiteNames is the account's sites visitors can see: not deleted, not
// taken down, the account not suspended. The platform alone decides it.
func LiveSiteNames(ctx context.Context, q sqlkit.Querier, accountID string) ([]string, error) {
	return repo.LiveSiteNames(ctx, q, accountID, false)
}

// ShownSiteNames is LiveSiteNames with the sites in Recently deleted too: a
// site a product still shows after it left the live gallery (final results).
func ShownSiteNames(ctx context.Context, q sqlkit.Querier, accountID string) ([]string, error) {
	return repo.LiveSiteNames(ctx, q, accountID, true)
}

// SiteEverNamed reports whether the account has or had a site of that name.
func SiteEverNamed(ctx context.Context, q sqlkit.Querier, accountID, name string) (bool, error) {
	return repo.SiteEverNamed(ctx, q, accountID, name)
}

// LiveActiveVersion is the live version of the account's site of that name.
func LiveActiveVersion(ctx context.Context, q sqlkit.Querier, accountID, name string) (sql.NullInt64, error) {
	return repo.LiveActiveVersion(ctx, q, accountID, name)
}

// StoredVersions is every stored version of the account's sites, as "<site id>/<n>".
func StoredVersions(ctx context.Context, q sqlkit.Querier, accountID string) (map[string]bool, error) {
	return repo.StoredVersions(ctx, q, accountID)
}

// AccountSiteCounts counts the account's site records and their kept versions.
func AccountSiteCounts(ctx context.Context, q sqlkit.Querier, accountID string) (int64, int64, error) {
	return repo.AccountSiteCounts(ctx, q, accountID)
}

// LockAccountSites locks every site row of the account, as a deploy takes
// its own: an account the product is about to delete.
func LockAccountSites(ctx context.Context, tx *sql.Tx, accountID string) error {
	return repo.LockAccountSites(ctx, tx, accountID)
}

// DeleteProductAccount deletes, inside the product's transaction, an account
// the product holds; its sites and memberships go with it.
func DeleteProductAccount(ctx context.Context, tx *sql.Tx, accountID string) error {
	return repo.DeleteAccountRow(ctx, tx, accountID)
}

// AccountSiteTakenDown reports whether any site of the account is taken
// down: a product must not erase it (an operator hold always wins).
func AccountSiteTakenDown(ctx context.Context, q sqlkit.Querier, accountID string) (bool, error) {
	return repo.AccountSiteTakenDown(ctx, q, accountID)
}

// SiteTakenDown reports whether a site is taken down, by its own flag or
// through its account's.
func SiteTakenDown(ctx context.Context, q sqlkit.Querier, siteID string) (bool, error) {
	down, _, err := accountsrepo.SiteSuspension(ctx, q, siteID)
	return down, err
}
