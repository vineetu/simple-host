package repo

import (
	"context"
	"database/sql"

	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	sitesrepo "github.com/vsriram/simple-host/host/sites/repo"

	"github.com/vsriram/simple-host/host/model"
)

// GetAnalyticsOwner resolves the current account handle for an admin report.
func GetAnalyticsOwner(ctx context.Context, database *sql.DB, handle string) (model.User, error) {
	return accountsrepo.GetUserByHandle(ctx, database, handle)
}

// AccountCount counts the non-admin accounts holding at least one site.
func AccountCount(ctx context.Context, db *sql.DB) (int, error) {
	admins, err := accountsrepo.AdminAccountIDs(ctx, db)
	if err != nil {
		return 0, err
	}
	return sitesrepo.SiteHolderCount(ctx, db, admins)
}
