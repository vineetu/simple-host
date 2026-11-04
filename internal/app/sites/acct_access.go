package sites

import (
	"context"
	"github.com/vsriram/simple-host/internal/model"
	repo "github.com/vsriram/simple-host/internal/repository/sites"
	"log"
	"time"
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
	return repo.ListSiteViewers(ctx, s.database, siteID)
}
func (s *Service) CountSiteViewers(ctx context.Context, siteID string) (int, error) {
	return repo.CountSiteViewers(ctx, s.database, siteID)
}
func (s *Service) AddSiteViewer(ctx context.Context, siteID, email string) (bool, error) {
	return repo.AddSiteViewer(ctx, s.database, siteID, email)
}
func (s *Service) RemoveSiteViewer(ctx context.Context, siteID, email string) (bool, error) {
	return repo.RemoveSiteViewer(ctx, s.database, siteID, email)
}
func (s *Service) IsSiteViewer(ctx context.Context, siteID, email string) (bool, error) {
	return repo.IsSiteViewer(ctx, s.database, siteID, email)
}

func (s *Service) SiteSuspension(ctx context.Context, siteID string) (bool, string, error) {
	return repo.SiteSuspension(ctx, s.database, siteID)
}

func (s *Service) SetSitePasscode(ctx context.Context, site model.Site, previous model.SitePasscodeRow, enc []byte) (time.Time, error) {
	if err := s.disk.SetPasscodeMarker(site.UserID, site.Name, true); err != nil {
		log.Printf("passcode: marker for site %s: %v", site.ID, err)
		return time.Time{}, &Error{Code: "access_marker", Message: "the site's files could not be marked; nothing changed, try again"}
	}
	now := time.Now().UTC().Truncate(time.Second)
	if _, err := repo.SetSitePasscode(ctx, s.database, site.ID, enc, now); err != nil {
		if previous.Enc == nil {
			_ = s.disk.SetPasscodeMarker(site.UserID, site.Name, false)
		}
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
		if err := s.disk.SetPasscodeMarker(site.UserID, site.Name, true); err != nil {
			log.Printf("viewers: marker for site %s: %v", site.ID, err)
			return &Error{Code: "access_marker", Message: "the site's files could not be marked; nothing changed, try again"}
		}
		if err := repo.SetSiteAccess(ctx, s.database, site.ID, access); err != nil {
			_ = s.disk.SetPasscodeMarker(site.UserID, site.Name, row.Enc != nil)
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
func (s *Service) SyncAccessMarker(site model.Site) error {
	return s.disk.SetPasscodeMarker(site.UserID, site.Name, site.Passcode || site.NamedViewers)
}
