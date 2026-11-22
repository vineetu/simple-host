package sites

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"time"

	"github.com/vsriram/simple-host/host/sites/repo"
)

// IdleSite is a candidate for idle cleanup.
type IdleSite = repo.IdleSite

// IdleExempt describes accounts and sites excluded from cleanup.
type IdleExempt = repo.IdleExempt

// IdleArchiveDeps supplies instance policy.
type IdleArchiveDeps struct {
	Exempt repo.IdleExempt
	// After is how long a site goes without a visit, a new version or saved
	// data before it moves to Recently deleted.
	After time.Duration
}

// IdleArchiveInput identifies a listed candidate and the cleanup clock.
type IdleArchiveInput struct {
	Site IdleSite
	Now  time.Time
}

// RemoveIdleSite moves a site to Recently deleted, in the transaction that
// moves it: the removal re-checks that the site still qualifies (a Keep,
// deploy, save or new domain since the list was read rolls it back).
func (svc *Service) RemoveIdleSite(ctx context.Context, in IdleArchiveInput) {
	s, now := in.Site, in.Now
	d := svc.deps.IdleArchive
	unlock := svc.lockSite(s.UserID, s.Name)
	defer unlock()
	site, err := repo.GetSiteByUser(ctx, svc.database, s.UserID, s.Name)
	if err != nil || site.ID != s.SiteID || !site.Live() {
		return
	}
	markIdle := func(tx *sql.Tx) error {
		return repo.MarkIdleRemoved(ctx, tx, d.Exempt, site.ID, now.Add(-d.After))
	}
	if err := svc.trash(ctx, site, markIdle); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			ReportWorkerFailure(ctx, "remove idle site", err)
			log.Printf("idle-site cleanup: remove %s: %v", s.SiteID, err)
		}
		return
	}
	ReportWorkerProgress(ctx)
	log.Printf("idle-site cleanup: moved %s (%s) to Recently deleted", s.Name, s.SiteID)
}
