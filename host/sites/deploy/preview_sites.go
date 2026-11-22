package deploy

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"strings"
	"time"

	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/sites/repo"
)

// previewExpiry returns the expiry for a preview account, or nil for a permanent site.
func (s Service) previewExpiry(username string) *time.Time {
	if len(s.PreviewAccounts) == 0 || !s.PreviewAccounts[strings.ToLower(username)] {
		return nil
	}
	t := time.Now().Add(s.PreviewTTL)
	return &t
}

// SweepExpiredSites removes expired preview site rows and then their files.
func (s Service) SweepExpiredSites(parents ...context.Context) {
	parent := context.Background()
	if len(parents) > 0 {
		parent = parents[0]
	}
	ctx, cancel := context.WithTimeout(parent, 60*time.Second)
	defer cancel()
	expired, err := repo.ListExpiredSites(ctx, s.DB)
	if err != nil {
		sites.ReportWorkerFailure(ctx, "list expired preview sites", err)
		log.Printf("expiry sweep: list failed: %v", err)
		return
	}
	for _, site := range expired {
		unlock := s.LockSite(site.UserID, site.Name)
		if err := repo.DeleteExpiredSite(ctx, s.DB, site.ID); errors.Is(err, sql.ErrNoRows) {
			unlock()
			continue
		} else if err != nil {
			sites.ReportWorkerFailure(ctx, "delete expired preview row", err)
			log.Printf("expiry sweep: delete row %s (%s): %v", site.Name, site.ID, err)
			unlock()
			continue
		}
		if err := s.Disk.DeleteSite(site.UserID, site.Name); err != nil {
			sites.ReportWorkerFailure(ctx, "delete expired preview files", err)
			log.Printf("expiry sweep: delete disk %s: %v", site.Name, err)
		}
		unlock()
		sites.ReportWorkerProgress(ctx)
		log.Printf("expiry sweep: removed preview site %q", site.Name)
	}
}
