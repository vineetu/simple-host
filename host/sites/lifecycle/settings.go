package lifecycle

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/url"
	"strings"

	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/sites/repo"
)

// VisibilityInput holds the values for SetVisibility.
type VisibilityInput struct{ UserID, Name, Visibility string }

// SetVisibility updates visibility and the corresponding on-disk marker under the site lock.
func (s Service) SetVisibility(ctx context.Context, in VisibilityInput) error {
	userID, name, visibility := in.UserID, in.Name, in.Visibility
	unlock := s.LockSite(userID, name)
	defer unlock()
	site, err := repo.GetSiteByUser(ctx, s.DB, userID, name)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return &sites.Error{Code: "site_not_found"}
		}
		return err
	}
	if site.Suspended() {
		return &sites.Error{Code: "site_suspended", Message: site.SuspendedReason()}
	}
	if err := repo.SetSiteVisibility(ctx, s.DB, site.ID, visibility); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return &sites.Error{Code: "not_found", Message: "site not found"}
		}
		return err
	}
	return nil
}

// SetAllowedOrigins sets the extra origins that may call a site's API
// cross-origin, so a page hosted anywhere can use the site as its backend.
// Each origin is scheme://host with no path, at most 20; the cleaned list is
// returned. Refusals: site_not_found, site_suspended, invalid_origin and
// too_many_origins.
func (s Service) SetAllowedOrigins(ctx context.Context, userID, name string, origins []string) ([]string, error) {
	site, err := repo.GetSiteByUser(ctx, s.DB, userID, name)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, &sites.Error{Code: "site_not_found", Message: "site not found"}
	}
	if err != nil {
		return nil, err
	}
	if site.Suspended() {
		return nil, &sites.Error{Code: "site_suspended", Message: site.SuspendedReason()}
	}
	clean := make([]string, 0, len(origins))
	for _, o := range origins {
		o = strings.TrimSpace(o)
		if o == "" {
			continue
		}
		u, err := url.Parse(o)
		if err != nil || u.Scheme == "" || u.Host == "" || (u.Path != "" && u.Path != "/") {
			return nil, &sites.Error{Code: "invalid_origin", Message: "each origin must be scheme://host with no path, e.g. https://you.github.io"}
		}
		clean = append(clean, u.Scheme+"://"+u.Host)
	}
	if len(clean) > 20 {
		return nil, &sites.Error{Code: "too_many_origins", Message: "too many origins (max 20)"}
	}
	if err := repo.SetAllowedOrigins(ctx, s.DB, site.ID, strings.Join(clean, ",")); err != nil {
		return nil, err
	}
	return clean, nil
}

// ownSite is the account's site by name for a change: Error site_not_found
// when it has none, site_suspended (Message: the reason) when the site or its
// account is taken down.
func (s Service) ownSite(ctx context.Context, userID, name string) (model.Site, error) {
	site, err := repo.GetSiteByUser(ctx, s.DB, userID, name)
	if errors.Is(err, sql.ErrNoRows) {
		return site, &sites.Error{Code: "site_not_found", Message: "site not found"}
	}
	if err != nil {
		return site, err
	}
	if site.Suspended() {
		return site, &sites.Error{Code: "site_suspended", Message: site.SuspendedReason()}
	}
	return site, nil
}

// SetSiteKeep marks the account's site as kept (never removed as idle) or
// not, and returns the site. Refusals as ownSite.
func (s Service) SetSiteKeep(ctx context.Context, userID, name string, keep bool) (model.Site, error) {
	site, err := s.ownSite(ctx, userID, name)
	if err != nil {
		return site, err
	}
	return site, repo.SetSiteKeep(ctx, s.DB, site.ID, keep)
}

// SetSiteOffline takes the account's site offline (on) or back online, in the
// database and on disk, under the same lock as a deploy, rename or delete so
// the marker is never written into a folder that moved meanwhile. Refusals as
// ownSite; marker_failed when the row was saved but the files could not be
// marked.
func (s Service) SetSiteOffline(ctx context.Context, userID, name string, on bool) (model.Site, error) {
	// A name the account does not have takes no lock.
	if _, err := repo.GetSiteByUser(ctx, s.DB, userID, name); errors.Is(err, sql.ErrNoRows) {
		return model.Site{}, &sites.Error{Code: "site_not_found", Message: "site not found"}
	} else if err != nil {
		return model.Site{}, err
	}
	unlock := s.LockSite(userID, name)
	defer unlock()
	site, err := s.ownSite(ctx, userID, name)
	if err != nil {
		return site, err
	}
	if err := repo.SetSiteOffline(ctx, s.DB, site.ID, on); errors.Is(err, sql.ErrNoRows) {
		return site, &sites.Error{Code: "site_not_found", Message: "site not found"}
	} else if err != nil {
		return site, err
	}
	site.Offline = on
	if err := s.Disk.SetOffline(site.UserID, site.Name, on); err != nil {
		log.Printf("offline: marker for site %s: %v", site.ID, err)
		return site, &sites.Error{Code: "marker_failed", Message: "saved, but the site's files could not be marked; try again"}
	}
	return site, nil
}
