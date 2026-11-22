package sites

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"log"
	"time"

	accountapp "github.com/vsriram/simple-host/host/access"

	"github.com/vsriram/simple-host/host/sites/repo"

	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/model"
)

func (s *Service) GetSitePasscode(ctx context.Context, siteID string) (model.SitePasscodeRow, error) {
	return repo.GetSitePasscode(ctx, s.DB, siteID)
}

// BumpSitePasscodeGeneration ends every unlock of the site's passcode; a
// site without one is refused (409 no_passcode).
func (s *Service) BumpSitePasscodeGeneration(ctx context.Context, siteID string) (int, error) {
	gen, err := repo.BumpSitePasscodeGeneration(ctx, s.DB, siteID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, &accountapp.Refusal{Status: http.StatusConflict, Code: "no_passcode", Message: "this site has no passcode"}
	}
	return gen, err
}
func (s *Service) GetSitePasscodeByName(ctx context.Context, userID, name string) (model.SitePasscodeRow, error) {
	return repo.GetSitePasscodeByName(ctx, s.DB, userID, name)
}
func (s *Service) ListSiteViewers(ctx context.Context, siteID string) ([]model.SiteViewer, error) {
	return accountsrepo.ListSiteViewers(ctx, s.DB, siteID)
}
func (s *Service) CountSiteViewers(ctx context.Context, siteID string) (int, error) {
	return accountsrepo.CountSiteViewers(ctx, s.DB, siteID)
}
func (s *Service) AddSiteViewer(ctx context.Context, siteID, email string) (bool, error) {
	return accountsrepo.AddSiteViewer(ctx, s.DB, siteID, email)
}

func (s *Service) IsSiteViewer(ctx context.Context, siteID, email string) (bool, error) {
	return accountsrepo.IsSiteViewer(ctx, s.DB, siteID, email)
}

func (s *Service) SiteSuspension(ctx context.Context, siteID string) (bool, string, error) {
	return repo.SiteSuspension(ctx, s.DB, siteID)
}

func (s *Service) SetSitePasscode(ctx context.Context, site model.Site, previous model.SitePasscodeRow, enc []byte) (time.Time, error) {
	if err := s.Disk.SetPasscodeMarker(site.UserID, site.Name, true); err != nil || !s.Disk.HasPasscodeMarker(site.UserID, site.Name) {
		log.Printf("passcode: marker for site %s: %v", site.ID, err)
		return time.Time{}, &Error{Code: "access_marker", Message: "the site's files could not be marked; nothing changed, try again"}
	}
	now := time.Now().UTC().Truncate(time.Second)
	if _, err := repo.SetSitePasscode(ctx, s.DB, site.ID, enc, now); err != nil {
		s.repairUncertainAccess(ctx, site)
		return time.Time{}, err
	}
	return now, nil
}

// ClearSitePasscode opens the site to everyone again; had says whether it
// had a passcode. The caller holds the site's lock. sql.ErrNoRows for an
// unknown or deleted site.
func (s *Service) ClearSitePasscode(ctx context.Context, site model.Site) (had bool, err error) {
	row, err := repo.GetSitePasscode(ctx, s.DB, site.ID)
	if err != nil {
		return false, err
	}
	if err := repo.ClearSitePasscode(ctx, s.DB, site.ID); err != nil {
		return false, err
	}
	if err := s.Disk.SetPasscodeMarker(site.UserID, site.Name, site.NamedViewers); err != nil {
		log.Printf("passcode: marker for site %s: %v", site.ID, err)
		return false, &Error{Code: "access_marker", Message: "the passcode is removed, but the site's files could not be updated; try again"}
	}
	return row.Enc != nil, nil
}
func (s *Service) SetSiteAccess(ctx context.Context, site model.Site, row model.SitePasscodeRow, access string) error {
	if access == "specific" {
		if err := s.Disk.SetPasscodeMarker(site.UserID, site.Name, true); err != nil || !s.Disk.HasPasscodeMarker(site.UserID, site.Name) {
			log.Printf("viewers: marker for site %s: %v", site.ID, err)
			return &Error{Code: "access_marker", Message: "the site's files could not be marked; nothing changed, try again"}
		}
		if err := repo.SetSiteAccess(ctx, s.DB, site.ID, access); err != nil {
			s.repairUncertainAccess(ctx, site)
			return err
		}
	} else {
		if err := repo.SetSiteAccess(ctx, s.DB, site.ID, access); err != nil {
			return err
		}
		if row.Enc == nil {
			if err := s.Disk.SetPasscodeMarker(site.UserID, site.Name, false); err != nil {
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
	row, err := repo.GetSitePasscodeByName(ctx, s.DB, site.UserID, site.Name)
	if err == nil && row.SiteID == site.ID && row.Enc == nil && !row.NamedViewers {
		_ = s.Disk.SetPasscodeMarker(site.UserID, site.Name, false)
	}
}

// Publishing an existing protected site may recreate a missing folder (for
// example an import). It must be marked before current becomes reachable.
func (s *Service) EnsureAccessMarker(site model.Site) error {
	if !site.Passcode && !site.NamedViewers {
		return nil
	}
	if err := s.Disk.SetPasscodeMarker(site.UserID, site.Name, true); err != nil || !s.Disk.HasPasscodeMarker(site.UserID, site.Name) {
		return &Error{Code: "access_marker", Message: "the site's files could not be marked; nothing changed, try again"}
	}
	return nil
}
