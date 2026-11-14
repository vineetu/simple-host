package sites

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"strings"
	"time"

	"github.com/vsriram/simple-host/internal/repository/accounts"
	repo "github.com/vsriram/simple-host/internal/repository/sites"
	rules "github.com/vsriram/simple-host/internal/rules/sites"
)

// ErrRestoreFiles means the deleted site's files could not be restored.
var ErrRestoreFiles = errors.New("could not restore the site's files")

// TrashInput describes a locked deletion, for an already locked site.
type TrashInput struct {
	Site repo.Site
}

// RestoreTrashedInput describes a locked restore and current handle.
type RestoreTrashedInput struct {
	Site   repo.DeletedSite
	Handle string
}

// Trash moves a live site to Recently deleted: marked in the database
// (plus whatever also records in the same transaction), and its files moved
// out of every served path, with the commit last so a failure leaves the site
// live and whole. The caller holds the site's lock. sql.ErrNoRows when the
// site is no longer live.
func (s *Service) Trash(ctx context.Context, in TrashInput) error {
	site, also := in.Site, s.deps.DeletedTransaction
	tx, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := repo.MarkSiteDeleted(ctx, tx, site.ID, s.deps.DeletedRetention); err != nil {
		return err
	}
	if also != nil {
		if err := also(tx); err != nil {
			return err
		}
	}
	if err := s.disk.TrashSite(site.UserID, site.Name, site.ID); err != nil {
		log.Printf("delete site %s: %v", site.ID, err)
		return err
	}
	if err := tx.Commit(); err != nil {
		if rerr := s.disk.RestoreSite(site.UserID, site.Name, site.ID); rerr != nil {
			log.Printf("delete site %s: commit failed and files could not be moved back: %v", site.ID, rerr)
		}
		return err
	}
	return nil
}

// WakeImageScans makes image checks waiting on a restored site's delete
// run now, not at their next hourly look. A failure only delays them. The
// restore has committed by then, so a client that hangs up does not stop it.
func (s *Service) WakeImageScans(ctx context.Context, siteID string) {
	if !s.deps.ImageScanEnabled {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := repo.WakeImageScans(ctx, s.database, siteID); err != nil {
		log.Printf("restore site %s: image checks: %v", siteID, err)
	}
}

// RestoreTrashed brings a site in Recently deleted back exactly as it
// was (plus whatever also records in the same transaction). handle is the
// owner's current handle ("" if none). The caller holds the site's lock.
// sql.ErrNoRows when it is not (or no longer) in Recently deleted.
func (s *Service) RestoreTrashed(ctx context.Context, in RestoreTrashedInput) (repo.Site, error) {
	d, handle, also := in.Site, in.Handle, s.deps.DeletedTransaction
	tx, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return repo.Site{}, err
	}
	defer tx.Rollback()
	if err := repo.RestoreDeletedSite(ctx, tx, d.ID); err != nil {
		return repo.Site{}, err
	}
	if also != nil {
		if err := also(tx); err != nil {
			return repo.Site{}, err
		}
	}
	full, err := repo.GetSiteByID(ctx, tx, d.ID)
	if err != nil {
		return repo.Site{}, err
	}
	if gate := s.deps.GroupGate; gate != nil {
		prefix, reason, err := gate.Suspension(ctx, tx, full)
		if err != nil {
			return repo.Site{}, err
		}
		current := full.SiteSuspendedReason
		if prefix != "" && (current == "" || strings.HasPrefix(current, prefix)) && current != reason {
			if err := repo.SetSiteSuspended(ctx, tx, d.ID, reason); err != nil {
				return repo.Site{}, err
			}
		}
	}
	full, err = repo.GetSiteByID(ctx, tx, d.ID)
	if err != nil {
		return repo.Site{}, err
	}
	// Rewrite the deleted folder's marker before it can be served, including
	// any owner suspension even when the product just lifted its own reason.
	if err := s.disk.SetTrashedSuspended(d.UserID, d.ID, full.Suspended()); err != nil {
		log.Printf("restore site %s: suspend marker: %v", d.ID, err)
		return repo.Site{}, ErrRestoreFiles
	}
	// The name is this site's again; an old name another site left there
	// stops redirecting.
	if err := repo.DropOldSiteName(ctx, tx, d.UserID, d.Name); err != nil {
		return repo.Site{}, err
	}
	if err := s.disk.RestoreSite(d.UserID, d.Name, d.ID); err != nil {
		log.Printf("restore site %s: %v", d.ID, err)
		return repo.Site{}, ErrRestoreFiles
	}
	if err := tx.Commit(); err != nil {
		if terr := s.disk.TrashSite(d.UserID, d.Name, d.ID); terr != nil {
			log.Printf("restore site %s: commit failed and files could not be moved back: %v", d.ID, terr)
		}
		return repo.Site{}, err
	}
	s.WakeImageScans(ctx, d.ID)
	// The handle may have changed while the site was deleted.
	if handle != "" {
		if err := s.disk.EnsureHandleLink(handle, d.UserID); err != nil {
			log.Printf("restore site: ensure handle link %s: %v", handle, err)
		}
		s.deps.RequestCert(handle)
	}
	site, err := repo.GetSiteByUser(ctx, s.database, d.UserID, d.Name)
	if err != nil {
		return repo.Site{}, err
	}
	// The operator may have acted since: make the marker follow the record.
	if err := s.disk.SetSuspended(site.UserID, site.Name, site.Suspended()); err != nil {
		log.Printf("restore site %s: suspend marker: %v", site.ID, err)
	}
	return site, nil
}

// PurgeDeleted removes sites whose restore window has passed: the row
// (versions, data and claimed names cascade) first, then the files.
func (s *Service) PurgeDeleted(ctx context.Context) {
	list, err := repo.ListPurgeableSites(ctx, s.database, s.deps.DeletedRetention)
	if err != nil {
		ReportWorkerFailure(ctx, "list purgeable sites", err)
		log.Printf("deleted-site purge: list: %v", err)
		return
	}
	for _, d := range list {
		unlock := s.deps.LockSite(d.UserID, d.Name)
		domains, err := s.purgeDeletedSite(ctx, d.ID)
		unlock()
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				ReportWorkerFailure(ctx, "purge site row", err)
				log.Printf("deleted-site purge: %s: %v", d.ID, err)
			}
			continue
		}
		if err := s.disk.PurgeTrashedSite(d.UserID, d.ID); err != nil {
			ReportWorkerFailure(ctx, "purge site files", err)
			log.Printf("deleted-site purge: files of %s: %v", d.ID, err)
		}
		if err := s.disk.PurgeTrashedSiteLinks(d.UserID, d.Name, strings.ToLower(d.CustomDomain.String)); err != nil {
			ReportWorkerFailure(ctx, "purge site links", err)
			log.Printf("deleted-site purge: links of %s: %v", d.ID, err)
		}
		for _, dom := range domains {
			// Only while the link is still this site's: the domain may have
			// been bound to another site since this one was deleted.
			mine, err := s.disk.UnbindDomainOf(dom, d.UserID, d.Name)
			if err != nil {
				ReportWorkerFailure(ctx, "unbind purged site domain", err)
				log.Printf("deleted-site purge: unbind %s: %v", dom, err)
				continue
			}
			if mine {
				s.deps.CancelDomainCert(dom)
			}
		}
		ReportWorkerProgress(ctx)
		log.Printf("deleted-site purge: removed %s (%s)", d.Name, d.ID)
	}
}

func (s *Service) purgeDeletedSite(ctx context.Context, siteID string) ([]string, error) {
	return repo.PurgeDeletedSite(ctx, s.database, siteID, s.deps.DeletedRetention, accounts.PlatformDomains()...)
}

// DeleteInput identifies an owner request and any site selected by an administrator.
type DeleteInput struct {
	UserID, Name, ExpectedSiteID string
	CheckSiteID                  bool
}

// Delete authorizes and moves a live site to Recently deleted under its lock.
func (s *Service) Delete(ctx context.Context, in DeleteInput) error {
	if err := rules.ValidateShape(in.Name); err != nil {
		return &Error{Code: "delete_not_found"}
	}
	lookup := func() (repo.Site, error) {
		site, err := repo.GetSiteByUser(ctx, s.database, in.UserID, in.Name)
		if err == nil && in.CheckSiteID && in.ExpectedSiteID != site.ID {
			return repo.Site{}, sql.ErrNoRows
		}
		return site, err
	}
	// Look up before taking the lock, then read again under the lock.
	if _, err := lookup(); errors.Is(err, sql.ErrNoRows) {
		return &Error{Code: "delete_not_found"}
	}
	unlock := s.deps.LockSite(in.UserID, in.Name)
	defer unlock()
	site, err := lookup()
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return &Error{Code: "delete_not_found"}
		}
		return err
	}
	if site.Suspended() {
		return &Error{Code: "site_suspended", Message: site.SuspendedReason()}
	}
	if err := s.Trash(ctx, TrashInput{Site: site}); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return &Error{Code: "delete_not_found"}
		}
		return err
	}
	return nil
}

// RestoreInput identifies the owner's deleted site and current handle.
type RestoreInput struct{ UserID, Name, Handle string }

// RestoreResult keeps the site and address response snapshot captured under its lock.
type RestoreResult struct {
	Site          repo.Site
	ResponseState any
}

// Restore restores a deleted site under its name lock, with its current addresses.
func (s *Service) Restore(ctx context.Context, in RestoreInput) (RestoreResult, error) {
	if err := rules.ValidateShape(in.Name); err != nil {
		return RestoreResult{}, &Error{Code: "deleted_not_found"}
	}
	// Lock only a name really held in Recently deleted; check again under it.
	unlock := func() {}
	if _, err := repo.GetDeletedSiteByUser(ctx, s.database, in.UserID, in.Name); err == nil {
		unlock = s.deps.LockSite(in.UserID, in.Name)
	}
	defer func() { unlock() }()
	d, err := repo.GetDeletedSiteByUser(ctx, s.database, in.UserID, in.Name)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			if _, lerr := repo.GetSiteByUser(ctx, s.database, in.UserID, in.Name); lerr == nil {
				return RestoreResult{}, &Error{Code: "site_not_deleted"}
			}
			return RestoreResult{}, &Error{Code: "deleted_not_found"}
		}
		return RestoreResult{}, err
	}
	// A site in Recently deleted already counts toward its quota.
	site, err := s.RestoreTrashed(ctx, RestoreTrashedInput{Site: d, Handle: in.Handle})
	if err != nil {
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return RestoreResult{}, &Error{Code: "deleted_not_found"}
		case errors.Is(err, ErrRestoreFiles):
			return RestoreResult{}, &Error{Code: "restore_files"}
		default:
			return RestoreResult{}, err
		}
	}
	if s.deps.HasLiveFamilies() {
		s.deps.SyncRedirect(ctx, site.ID)
	}
	return RestoreResult{Site: site, ResponseState: s.deps.ResponseState(site, 0, false, false)}, nil
}
