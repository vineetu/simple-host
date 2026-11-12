package sites

import (
	"context"
	"database/sql"
	"errors"
	accountapp "github.com/vsriram/simple-host/internal/app/accounts"
	"github.com/vsriram/simple-host/internal/model"
	repo "github.com/vsriram/simple-host/internal/repository/sites"
	"log"
)

func suspendedMarker(s *Service, site model.Site) error {
	return s.disk.SetSuspended(site.UserID, site.Name, site.Suspended())
}
func (s *Service) SyncSiteSuspensionMarker(site model.Site) error { return suspendedMarker(s, site) }
func (svc *Service) SyncSuspendMarkers(ctx context.Context) {
	sites, err := repo.ListAllSites(ctx, svc.database)
	if err != nil {
		ReportWorkerFailure(ctx, "sync site markers", err)
		return
	}
	n := 0
	for _, s := range sites {
		if s.Suspended() != svc.disk.IsSuspended(s.UserID, s.Name) {
			if err := suspendedMarker(svc, s); err != nil {
				ReportWorkerFailure(ctx, "sync site markers", err)
				continue
			}
			n++
		}
		// The owner's offline switch (offline.go) has a marker too.
		if s.Offline != svc.disk.IsOffline(s.UserID, s.Name) {
			if err := svc.disk.SetOffline(s.UserID, s.Name, s.Offline); err != nil {
				ReportWorkerFailure(ctx, "sync site markers", err)
				continue
			}
			n++
		}
		// And a passcode (passcode.go).
		if (s.Passcode || s.NamedViewers) != svc.disk.HasPasscodeMarker(s.UserID, s.Name) {
			if err := svc.disk.SetPasscodeMarker(s.UserID, s.Name, s.Passcode || s.NamedViewers); err != nil {
				ReportWorkerFailure(ctx, "sync site markers", err)
				continue
			}
			n++
		}
	}
	if n > 0 {
		ReportWorkerProgress(ctx)
		log.Printf("suspend: corrected %d site marker(s)", n)
	}
}

func (s *Service) SuspendSite(ctx context.Context, id, reason, adminID string, on bool) (model.Site, error) {
	missing := func(err error) error {
		if errors.Is(err, sql.ErrNoRows) {
			return &accountapp.LifecycleError{Status: 404, Message: "site not found"}
		}
		return err
	}
	site, err := repo.GetSiteByID(ctx, s.database, id)
	if err != nil {
		return model.Site{}, missing(err)
	}
	// Lock, then re-read so a rename or delete cannot be undone by stale markers.
	for {
		unlock := s.deps.LockSite(site.UserID, site.Name)
		fresh, err := repo.GetSiteByID(ctx, s.database, id)
		if err != nil {
			unlock()
			return model.Site{}, missing(err)
		}
		if fresh.UserID == site.UserID && fresh.Name == site.Name {
			site = fresh
			defer unlock()
			break
		}
		unlock()
		site = fresh
	}
	if err := repo.SetSiteSuspended(ctx, s.database, site.ID, reason); err != nil {
		return model.Site{}, err
	}
	site.SiteSuspended, site.SiteSuspendedReason = on, reason
	if site.Deleted {
		log.Printf("admin_site_suspend on=%t site_id=%s name=%s owner=%s by=%s reason=%q deleted=true", on, site.ID, site.Name, site.UserID, adminID, reason)
		return site, nil
	}
	if err := s.SyncSiteSuspensionMarker(site); err != nil {
		log.Printf("suspend: marker for site %s: %v", site.ID, err)
		return model.Site{}, &accountapp.LifecycleError{Status: 500, Message: "saved, but the site's files could not be marked; retry"}
	}
	log.Printf("admin_site_suspend on=%t site_id=%s name=%s owner=%s by=%s reason=%q", on, site.ID, site.Name, site.UserID, adminID, reason)
	return site, nil
}
