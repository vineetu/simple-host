package sites

import (
	"context"
	"database/sql"
	"errors"
	"log"

	accountapp "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sites/repo"
)

func suspendedMarker(s *Service, site model.Site) error {
	return s.disk.SetSuspended(site.UserID, site.Name, site.Suspended())
}
func (s *Service) SyncSiteSuspensionMarker(site model.Site) error {
	if site.Suspended() {
		return suspendedMarker(s, site)
	}
	return s.withFreshMarkerSite(context.Background(), site, func(fresh model.Site) error { return suspendedMarker(s, fresh) })
}

// Before removing markers, re-read under the name lock. Never apply an old
// site's verdict to a replacement at the same name. Additions need no lock.
func (s *Service) withFreshMarkerSite(ctx context.Context, site model.Site, repair func(model.Site) error) error {
	unlock := s.deps.LockSite(site.UserID, site.Name)
	defer unlock()
	fresh, err := repo.GetSiteByUser(ctx, s.database, site.UserID, site.Name)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if fresh.ID != site.ID {
		return nil
	}
	return repair(fresh)
}
func (svc *Service) SyncSuspendMarkers(ctx context.Context) {
	sites, err := repo.ListAllSites(ctx, svc.database)
	if err != nil {
		ReportWorkerFailure(ctx, "sync site markers", err)
		return
	}
	n := 0
	repair := func(s model.Site, remove bool) (needsRemoval bool) {
		for _, marker := range []struct {
			want, have bool
			set        func(bool) error
		}{
			{s.Suspended(), svc.disk.IsSuspended(s.UserID, s.Name), func(on bool) error { return svc.disk.SetSuspended(s.UserID, s.Name, on) }},
			{s.Offline, svc.disk.IsOffline(s.UserID, s.Name), func(on bool) error { return svc.disk.SetOffline(s.UserID, s.Name, on) }},
			{s.Gated(), svc.disk.HasPasscodeMarker(s.UserID, s.Name), func(on bool) error { return svc.disk.SetPasscodeMarker(s.UserID, s.Name, on) }},
		} {
			if marker.want != marker.have {
				if !marker.want && !remove {
					needsRemoval = true
					continue
				}
				if err := marker.set(marker.want); err != nil {
					ReportWorkerFailure(ctx, "sync site markers", err)
					continue
				}
				n++
			}
		}
		return needsRemoval
	}
	// Finish every addition before any removal can wait on a busy site. A
	// stalled export must not delay take-down or protection of later sites.
	var removals []model.Site
	for _, site := range sites {
		if repair(site, false) {
			removals = append(removals, site)
		}
	}
	for _, site := range removals {
		err := svc.withFreshMarkerSite(ctx, site, func(fresh model.Site) error {
			repair(fresh, true)
			return nil
		})
		if err != nil {
			ReportWorkerFailure(ctx, "sync site markers", err)
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
	if err := suspendedMarker(s, site); err != nil {
		log.Printf("suspend: marker for site %s: %v", site.ID, err)
		return model.Site{}, &accountapp.LifecycleError{Status: 500, Message: "saved, but the site's files could not be marked; retry"}
	}
	log.Printf("admin_site_suspend on=%t site_id=%s name=%s owner=%s by=%s reason=%q", on, site.ID, site.Name, site.UserID, adminID, reason)
	return site, nil
}

// MarkAccountSites re-marks every site of the account after its suspension
// changed (access.SuspendPerson) and returns how many there are.
func (s *Service) MarkAccountSites(ctx context.Context, accountID string) (int, error) {
	list, err := repo.ListSitesByUser(ctx, s.database, accountID)
	if err != nil {
		return 0, &accountapp.LifecycleError{Status: 500, Message: "saved, but the account's sites could not be listed; retry"}
	}
	failed := 0
	for _, site := range list {
		if err := s.SyncSiteSuspensionMarker(site); err != nil {
			log.Printf("suspend: marker for %s/%s: %v", site.UserID, site.Name, err)
			failed++
		}
	}
	if failed > 0 {
		return 0, &accountapp.LifecycleError{Status: 500, Message: "saved, but some of the account's sites could not be marked; retry"}
	}
	return len(list), nil
}
