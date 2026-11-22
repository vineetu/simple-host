package sites

import (
	"context"
	"database/sql"
	"errors"
	"log"

	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sites/repo"
)

// ownSite is the account's site by name for a change: Error site_not_found
// when it has none, site_suspended (Message: the reason) when the site or its
// account is taken down.
func (s *Service) ownSite(ctx context.Context, userID, name string) (model.Site, error) {
	site, err := repo.GetSiteByUser(ctx, s.database, userID, name)
	if errors.Is(err, sql.ErrNoRows) {
		return site, &Error{Code: "site_not_found", Message: "site not found"}
	}
	if err != nil {
		return site, err
	}
	if site.Suspended() {
		return site, &Error{Code: "site_suspended", Message: site.SuspendedReason()}
	}
	return site, nil
}

// SetSiteKeep marks the account's site as kept (never removed as idle) or
// not, and returns the site. Refusals as ownSite.
func (s *Service) SetSiteKeep(ctx context.Context, userID, name string, keep bool) (model.Site, error) {
	site, err := s.ownSite(ctx, userID, name)
	if err != nil {
		return site, err
	}
	return site, repo.SetSiteKeep(ctx, s.database, site.ID, keep)
}

// SetSiteOffline takes the account's site offline (on) or back online, in the
// database and on disk, under the same lock as a deploy, rename or delete so
// the marker is never written into a folder that moved meanwhile. Refusals as
// ownSite; marker_failed when the row was saved but the files could not be
// marked.
func (s *Service) SetSiteOffline(ctx context.Context, userID, name string, on bool) (model.Site, error) {
	// A name the account does not have takes no lock.
	if _, err := repo.GetSiteByUser(ctx, s.database, userID, name); errors.Is(err, sql.ErrNoRows) {
		return model.Site{}, &Error{Code: "site_not_found", Message: "site not found"}
	} else if err != nil {
		return model.Site{}, err
	}
	unlock := s.lockSite(userID, name)
	defer unlock()
	site, err := s.ownSite(ctx, userID, name)
	if err != nil {
		return site, err
	}
	if err := repo.SetSiteOffline(ctx, s.database, site.ID, on); errors.Is(err, sql.ErrNoRows) {
		return site, &Error{Code: "site_not_found", Message: "site not found"}
	} else if err != nil {
		return site, err
	}
	site.Offline = on
	if err := s.disk.SetOffline(site.UserID, site.Name, on); err != nil {
		log.Printf("offline: marker for site %s: %v", site.ID, err)
		return site, &Error{Code: "marker_failed", Message: "saved, but the site's files could not be marked; try again"}
	}
	return site, nil
}

// KeptVersion is the account's site for a preview of its version n: Error
// site_not_found (also when checkSiteID and the site found is not
// expectedSiteID, the admin's mirror), site_suspended, or version_not_found
// when n is not a version the site still keeps.
func (s *Service) KeptVersion(ctx context.Context, userID, name, expectedSiteID string, checkSiteID bool, n int) (model.Site, error) {
	site, err := repo.GetSiteByUser(ctx, s.database, userID, name)
	if err == nil && checkSiteID && site.ID != expectedSiteID {
		err = sql.ErrNoRows
	}
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return site, &Error{Code: "site_not_found", Message: "site not found"}
	case err != nil:
		return site, err
	case site.Suspended():
		return site, &Error{Code: "site_suspended", Message: site.SuspendedReason()}
	}
	kept, err := repo.VersionKept(ctx, s.database, site.ID, n)
	if err != nil {
		return site, err
	}
	if !kept {
		return site, &Error{Code: "version_not_found", Message: "version not found"}
	}
	return site, nil
}
