package versions

import (
	"context"
	"database/sql"
	"errors"

	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/sites/repo"
)

// KeptVersion is the account's site for a preview of its version n: Error
// site_not_found (also when checkSiteID and the site found is not
// expectedSiteID, the admin's mirror), site_suspended, or version_not_found
// when n is not a version the site still keeps.
func (s Service) KeptVersion(ctx context.Context, userID, name, expectedSiteID string, checkSiteID bool, n int) (model.Site, error) {
	site, err := repo.GetSiteByUser(ctx, s.DB, userID, name)
	if err == nil && checkSiteID && site.ID != expectedSiteID {
		err = sql.ErrNoRows
	}
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return site, &sites.Error{Code: "site_not_found", Message: "site not found"}
	case err != nil:
		return site, err
	case site.Suspended():
		return site, &sites.Error{Code: "site_suspended", Message: site.SuspendedReason()}
	}
	kept, err := repo.VersionKept(ctx, s.DB, site.ID, n)
	if err != nil {
		return site, err
	}
	if !kept {
		return site, &sites.Error{Code: "version_not_found", Message: "version not found"}
	}
	return site, nil
}
