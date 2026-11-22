package sites

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"

	rules "github.com/vsriram/simple-host/host/rules/sites"
	"github.com/vsriram/simple-host/host/sites/repo"
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

// Trash moves a live site to Recently deleted: marked in the database, and
// its files moved out of every served path, with the commit last so a failure
// leaves the site live and whole. The caller holds the site's lock.
// sql.ErrNoRows when the site is no longer live.
func (s *Service) Trash(ctx context.Context, in TrashInput) error {
	return s.trash(ctx, in.Site, nil)
}

// trash is Trash with also, when not nil, recording in the same transaction
// (the idle cleanup's re-check).
func (s *Service) trash(ctx context.Context, site repo.Site, also func(*sql.Tx) error) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := repo.MarkSiteDeleted(ctx, tx, site.ID, s.Deps.DeletedRetention); err != nil {
		return err
	}
	if also != nil {
		if err := also(tx); err != nil {
			return err
		}
	}
	if err := s.Disk.TrashSite(site.UserID, site.Name, site.ID); err != nil {
		log.Printf("delete site %s: %v", site.ID, err)
		return err
	}
	if err := tx.Commit(); err != nil {
		if rerr := s.Disk.RestoreSite(site.UserID, site.Name, site.ID); rerr != nil {
			log.Printf("delete site %s: commit failed and files could not be moved back: %v", site.ID, rerr)
		}
		return err
	}
	return nil
}

// RestoreTrashed brings a site in Recently deleted back exactly as it
// was. handle is the
// owner's current handle ("" if none). The caller holds the site's lock.
// sql.ErrNoRows when it is not (or no longer) in Recently deleted.
func (s *Service) RestoreTrashed(ctx context.Context, in RestoreTrashedInput) (repo.Site, error) {
	d, handle := in.Site, in.Handle
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return repo.Site{}, err
	}
	defer tx.Rollback()
	if err := repo.RestoreDeletedSite(ctx, tx, d.ID); err != nil {
		return repo.Site{}, err
	}
	full, err := repo.GetSiteByID(ctx, tx, d.ID)
	if err != nil {
		return repo.Site{}, err
	}
	if gate := s.Deps.GroupGate; gate != nil {
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
	if err := s.Disk.SetTrashedSuspended(d.UserID, d.ID, full.Suspended()); err != nil {
		log.Printf("restore site %s: suspend marker: %v", d.ID, err)
		return repo.Site{}, ErrRestoreFiles
	}
	if err := s.Disk.SetTrashedAccessMarker(d.UserID, d.ID, full.Gated()); err != nil {
		log.Printf("restore site %s: access marker: %v", d.ID, err)
		return repo.Site{}, ErrRestoreFiles
	}
	// The name is this site's again; an old name another site left there
	// stops redirecting.
	if err := repo.DropOldSiteName(ctx, tx, d.UserID, d.Name); err != nil {
		return repo.Site{}, err
	}
	if err := s.Disk.RestoreSite(d.UserID, d.Name, d.ID); err != nil {
		log.Printf("restore site %s: %v", d.ID, err)
		return repo.Site{}, ErrRestoreFiles
	}
	if err := tx.Commit(); err != nil {
		if terr := s.Disk.TrashSite(d.UserID, d.Name, d.ID); terr != nil {
			log.Printf("restore site %s: commit failed and files could not be moved back: %v", d.ID, terr)
		}
		return repo.Site{}, err
	}
	// The handle may have changed while the site was deleted.
	if handle != "" {
		if err := s.Disk.EnsureHandleLink(handle, d.UserID); err != nil {
			log.Printf("restore site: ensure handle link %s: %v", handle, err)
		}
		s.RequestCert(handle)
	}
	site, err := repo.GetSiteByUser(ctx, s.DB, d.UserID, d.Name)
	if err != nil {
		return repo.Site{}, err
	}
	// The operator may have acted since: make the marker follow the record.
	if err := s.Disk.SetSuspended(site.UserID, site.Name, site.Suspended()); err != nil {
		log.Printf("restore site %s: suspend marker: %v", site.ID, err)
	}
	return site, nil
}

// DeleteInput identifies an owner request and any site selected by an administrator.
type DeleteInput struct {
	UserID, Name, ExpectedSiteID string
	CheckSiteID                  bool
	// ConfirmName, when set, must name the site (in any case), or nothing
	// is deleted: the caller's own check that it means this site.
	ConfirmName *string
}

// Delete authorizes and moves a live site to Recently deleted under its lock.
func (s *Service) Delete(ctx context.Context, in DeleteInput) error {
	if in.ConfirmName != nil && strings.ToLower(*in.ConfirmName) != in.Name {
		return &Error{Code: "confirm_name_mismatch", Message: fmt.Sprintf("confirm_name %q does not match site %q; nothing was deleted", *in.ConfirmName, in.Name)}
	}
	if err := rules.ValidateShape(in.Name); err != nil {
		return &Error{Code: "delete_not_found"}
	}
	lookup := func() (repo.Site, error) {
		site, err := repo.GetSiteByUser(ctx, s.DB, in.UserID, in.Name)
		if err == nil && in.CheckSiteID && in.ExpectedSiteID != site.ID {
			return repo.Site{}, sql.ErrNoRows
		}
		return site, err
	}
	// Look up before taking the lock, then read again under the lock.
	if _, err := lookup(); errors.Is(err, sql.ErrNoRows) {
		return &Error{Code: "delete_not_found"}
	}
	unlock := s.LockSite(in.UserID, in.Name)
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
