package sites

import (
	"context"
	"log"
	"time"

	"github.com/vsriram/simple-host/host/sites/repo"

	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/model"
)

func (s *Service) GetSitePasscode(ctx context.Context, siteID string) (model.SitePasscodeRow, error) {
	return repo.GetSitePasscode(ctx, s.database, siteID)
}
func (s *Service) BumpSitePasscodeGeneration(ctx context.Context, siteID string) (int, error) {
	return repo.BumpSitePasscodeGeneration(ctx, s.database, siteID)
}
func (s *Service) GetSitePasscodeByName(ctx context.Context, userID, name string) (model.SitePasscodeRow, error) {
	return repo.GetSitePasscodeByName(ctx, s.database, userID, name)
}
func (s *Service) ListSiteViewers(ctx context.Context, siteID string) ([]model.SiteViewer, error) {
	return accountsrepo.ListSiteViewers(ctx, s.database, siteID)
}
func (s *Service) CountSiteViewers(ctx context.Context, siteID string) (int, error) {
	return accountsrepo.CountSiteViewers(ctx, s.database, siteID)
}
func (s *Service) AddSiteViewer(ctx context.Context, siteID, email string) (bool, error) {
	return accountsrepo.AddSiteViewer(ctx, s.database, siteID, email)
}

// RemoveSiteViewer removes a named viewer and, in the same call (FIX 9,
// Codex round 2: "service-layer removal bypasses cleanup entirely"),
// deletes that person's push subscriptions for this site -- every
// caller of this service method gets the cleanup, not only the HTTP
// handler that happened to remember to call it separately. Deps.
// DeleteViewerPush is nil-safe (set by the handler, which is the only
// side that knows how to resolve a push device's owner_id back to an
// email): a failure there is logged, never returned, since the viewer
// is already removed and that must not roll back over a best-effort
// cleanup step; delivery-time membership checking (ProcessDelivery)
// remains the backstop either way.
func (s *Service) RemoveSiteViewer(ctx context.Context, siteID, email string) (bool, error) {
	removed, err := accountsrepo.RemoveSiteViewer(ctx, s.database, siteID, email)
	if err != nil || !removed {
		return removed, err
	}
	if s.deps.DeleteViewerPush != nil {
		if pushErr := s.deps.DeleteViewerPush(ctx, siteID, email); pushErr != nil {
			log.Printf("remove_site_viewer: push cleanup for site %s, %s: %v", siteID, email, pushErr)
		}
	}
	return removed, nil
}
func (s *Service) IsSiteViewer(ctx context.Context, siteID, email string) (bool, error) {
	return accountsrepo.IsSiteViewer(ctx, s.database, siteID, email)
}

func (s *Service) SiteSuspension(ctx context.Context, siteID string) (bool, string, error) {
	return repo.SiteSuspension(ctx, s.database, siteID)
}

func (s *Service) SetSitePasscode(ctx context.Context, site model.Site, previous model.SitePasscodeRow, enc []byte) (time.Time, error) {
	if err := s.disk.SetPasscodeMarker(site.UserID, site.Name, true); err != nil || !s.disk.HasPasscodeMarker(site.UserID, site.Name) {
		log.Printf("passcode: marker for site %s: %v", site.ID, err)
		return time.Time{}, &Error{Code: "access_marker", Message: "the site's files could not be marked; nothing changed, try again"}
	}
	now := time.Now().UTC().Truncate(time.Second)
	if _, err := repo.SetSitePasscode(ctx, s.database, site.ID, enc, now); err != nil {
		s.repairUncertainAccess(ctx, site)
		return time.Time{}, err
	}
	return now, nil
}
func (s *Service) ClearSitePasscode(ctx context.Context, site model.Site) error {
	if err := repo.ClearSitePasscode(ctx, s.database, site.ID); err != nil {
		return err
	}
	if err := s.disk.SetPasscodeMarker(site.UserID, site.Name, site.NamedViewers); err != nil {
		log.Printf("passcode: marker for site %s: %v", site.ID, err)
		return &Error{Code: "access_marker", Message: "the passcode is removed, but the site's files could not be updated; try again"}
	}
	return nil
}
func (s *Service) SetSiteAccess(ctx context.Context, site model.Site, row model.SitePasscodeRow, access string) error {
	if access == "specific" {
		if err := s.disk.SetPasscodeMarker(site.UserID, site.Name, true); err != nil || !s.disk.HasPasscodeMarker(site.UserID, site.Name) {
			log.Printf("viewers: marker for site %s: %v", site.ID, err)
			return &Error{Code: "access_marker", Message: "the site's files could not be marked; nothing changed, try again"}
		}
		if err := repo.SetSiteAccess(ctx, s.database, site.ID, access); err != nil {
			s.repairUncertainAccess(ctx, site)
			return err
		}
	} else {
		if err := repo.SetSiteAccess(ctx, s.database, site.ID, access); err != nil {
			return err
		}
		if row.Enc == nil {
			if err := s.disk.SetPasscodeMarker(site.UserID, site.Name, false); err != nil {
				log.Printf("viewers: marker for site %s: %v", site.ID, err)
				return &Error{Code: "access_marker", Message: "the site is open again, but its files could not be updated; try again"}
			}
		}
	}
	return nil
}

// The caller holds the name lock. An autocommit error is not a rollback:
// only a successful fresh read can justify removing the marker.
func (s *Service) repairUncertainAccess(ctx context.Context, site model.Site) {
	row, err := repo.GetSitePasscodeByName(ctx, s.database, site.UserID, site.Name)
	if err == nil && row.SiteID == site.ID && row.Enc == nil && !row.NamedViewers {
		_ = s.disk.SetPasscodeMarker(site.UserID, site.Name, false)
	}
}

// Publishing an existing protected site may recreate a missing folder (for
// example an import). It must be marked before current becomes reachable.
func (s *Service) ensureAccessMarker(site model.Site) error {
	if !site.Passcode && !site.NamedViewers {
		return nil
	}
	if err := s.disk.SetPasscodeMarker(site.UserID, site.Name, true); err != nil || !s.disk.HasPasscodeMarker(site.UserID, site.Name) {
		return &Error{Code: "access_marker", Message: "the site's files could not be marked; nothing changed, try again"}
	}
	return nil
}
