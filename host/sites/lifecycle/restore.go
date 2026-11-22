package lifecycle

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"strings"

	accounts "github.com/vsriram/simple-host/host/access/repo"
	rules "github.com/vsriram/simple-host/host/rules/sites"

	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/sites/repo"
)

// RestoreInput identifies the owner's deleted site and current handle.
type RestoreInput struct{ UserID, Name, Handle string }

// RestoreResult keeps the site and address response snapshot captured under its lock.
type RestoreResult struct {
	Site repo.Site
}

// Restore restores a deleted site under its name lock, with its current addresses.
func (s Service) Restore(ctx context.Context, in RestoreInput) (RestoreResult, error) {
	if err := rules.ValidateShape(in.Name); err != nil {
		return RestoreResult{}, &sites.Error{Code: "deleted_not_found"}
	}
	// Lock only a name really held in Recently deleted; check again under it.
	unlock := func() {}
	if _, err := repo.GetDeletedSiteByUser(ctx, s.DB, in.UserID, in.Name); err == nil {
		unlock = s.LockSite(in.UserID, in.Name)
	}
	defer func() { unlock() }()
	d, err := repo.GetDeletedSiteByUser(ctx, s.DB, in.UserID, in.Name)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			if _, lerr := repo.GetSiteByUser(ctx, s.DB, in.UserID, in.Name); lerr == nil {
				return RestoreResult{}, &sites.Error{Code: "site_not_deleted"}
			}
			return RestoreResult{}, &sites.Error{Code: "deleted_not_found"}
		}
		return RestoreResult{}, err
	}
	// A site in Recently deleted already counts toward its quota.
	site, err := s.RestoreTrashed(ctx, sites.RestoreTrashedInput{Site: d, Handle: in.Handle})
	if err != nil {
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return RestoreResult{}, &sites.Error{Code: "deleted_not_found"}
		case errors.Is(err, sites.ErrRestoreFiles):
			return RestoreResult{}, &sites.Error{Code: "restore_files"}
		default:
			return RestoreResult{}, err
		}
	}
	s.SyncFamilyRedirect(ctx, site.ID)
	return RestoreResult{Site: site}, nil
}

// PurgeDeleted removes sites whose restore window has passed: the row
// (versions, data and claimed names cascade) first, then the files.
func (s Service) PurgeDeleted(ctx context.Context) {
	list, err := repo.ListPurgeableSites(ctx, s.DB, s.Deps.DeletedRetention)
	if err != nil {
		sites.ReportWorkerFailure(ctx, "list purgeable sites", err)
		log.Printf("deleted-site purge: list: %v", err)
		return
	}
	for _, d := range list {
		unlock := s.LockSite(d.UserID, d.Name)
		domains, err := repo.PurgeDeletedSite(ctx, s.DB, d.ID, s.Deps.DeletedRetention, accounts.PlatformDomains()...)
		unlock()
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				sites.ReportWorkerFailure(ctx, "purge site row", err)
				log.Printf("deleted-site purge: %s: %v", d.ID, err)
			}
			continue
		}
		if err := s.Disk.PurgeTrashedSite(d.UserID, d.ID); err != nil {
			sites.ReportWorkerFailure(ctx, "purge site files", err)
			log.Printf("deleted-site purge: files of %s: %v", d.ID, err)
		}
		if err := s.Disk.PurgeTrashedSiteLinks(d.UserID, d.Name, strings.ToLower(d.CustomDomain.String)); err != nil {
			sites.ReportWorkerFailure(ctx, "purge site links", err)
			log.Printf("deleted-site purge: links of %s: %v", d.ID, err)
		}
		for _, dom := range domains {
			// Only while the link is still this site's: the domain may have
			// been bound to another site since this one was deleted.
			mine, err := s.Disk.UnbindDomainOf(dom, d.UserID, d.Name)
			if err != nil {
				sites.ReportWorkerFailure(ctx, "unbind purged site domain", err)
				log.Printf("deleted-site purge: unbind %s: %v", dom, err)
				continue
			}
			if mine {
				s.Deps.Hosts.CancelDomainCert(dom)
			}
		}
		sites.ReportWorkerProgress(ctx)
		log.Printf("deleted-site purge: removed %s (%s)", d.Name, d.ID)
	}
}
