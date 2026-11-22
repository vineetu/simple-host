package versions

import (
	"context"
	"database/sql"
	"errors"
	"log"

	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/sites/repo"
)

// activateVersion updates the transaction and serving files, compensating if commit fails.
// The caller holds the site lock and has already authorized the transaction.
func (s Service) activateVersion(ctx context.Context, tx *sql.Tx, site model.Site, version int) (int, error) {
	prev, err := repo.SiteActiveVersion(ctx, tx, site.ID)
	if err != nil {
		return 0, err
	}
	if err := repo.UpdateSiteActiveVersion(ctx, tx, site.ID, version); err != nil {
		return 0, err
	}
	// A version stored with publish=false goes live for the first time.
	if err := repo.ActivateVersionNumber(ctx, tx, site.ID, version); err != nil {
		return 0, err
	}
	if err := s.EnsureAccessMarker(site); err != nil {
		return 0, err
	}
	// Change served files only after the database steps, and restore them if commit fails.
	if err := s.Disk.UpdateCurrent(site.UserID, site.Name, version); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		if prev != version {
			if rerr := s.Disk.UpdateCurrent(site.UserID, site.Name, prev); rerr != nil {
				log.Printf("rollback site_id=%s: commit failed (%v) and serving v%d again failed: %v", site.ID, err, prev, rerr)
			}
		}
		return 0, err
	}
	return prev, nil
}

// prepareRollback locks the site and checks the requested version before the transaction.
// The caller must release the returned lock when it is non-nil.
func (s Service) prepareRollback(ctx context.Context, userID, name, expectedID string, checkID bool, version int) (model.Site, func(), bool, error) {
	unlock := s.LockSite(userID, name)
	site, err := repo.GetSiteByUser(ctx, s.DB, userID, name)
	if err == nil && checkID && site.ID != expectedID {
		err = sql.ErrNoRows
	}
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return site, unlock, false, &sites.Error{Code: "site_not_found"}
		}
		return site, unlock, false, err
	}
	if site.Suspended() {
		return site, unlock, false, &sites.Error{Code: "site_suspended", Message: site.SuspendedReason()}
	}
	if site.ActiveVersion == version {
		return site, unlock, true, nil
	}
	versions, err := repo.ListVersionsBySite(ctx, s.DB, site.ID)
	if err != nil {
		return site, unlock, false, err
	}
	for _, v := range versions {
		// A version whose upload never finished is not one to go live.
		if v.VersionNumber == version && v.Status != "uploading" {
			return site, unlock, false, nil
		}
	}
	return site, unlock, false, &sites.Error{Code: "version_not_found", Message: "version not found"}
}

// rollbackTransaction locks the site row, runs the transaction-time gate and
// activates the version, announcing the change when it moved.
func (s Service) rollbackTransaction(ctx context.Context, site model.Site, version int, user *model.User) (model.Site, int, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return site, 0, err
	}
	defer tx.Rollback()
	if err := repo.LockSiteForUpdate(ctx, tx, site.ID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return site, 0, &sites.Error{Code: "site_not_found", Message: "site not found"}
		}
		return site, 0, err
	}
	if err := s.CreateGate(ctx, tx, user, site.Name); err != nil {
		return site, 0, err
	}
	prev, err := s.activateVersion(ctx, tx, site, version)
	if err != nil {
		return site, 0, err
	}
	site.ActiveVersion = version
	return site, prev, nil
}

// RollbackInput identifies the site and version to activate.
type RollbackInput struct {
	User             *model.User
	Name, ExpectedID string
	CheckID          bool
	Version          int
	// IfMatch is the raw If-Match header: the active version the caller last
	// read (ParseIfMatch).
	IfMatch string
}

// RollbackResult reports the site and whether it was already active.
type RollbackResult struct {
	Site      model.Site
	Unchanged bool
	// PreviousVersion is the version that was live before a rollback that
	// moved the site.
	PreviousVersion int
	// Moved: the rollback changed the live version.
	Moved bool
	// FileBytes is the site's footprint on disk, measured under the site lock.
	FileBytes int64
}

// Rollback activates an eligible version under the site lock.
func (s Service) Rollback(ctx context.Context, in RollbackInput) (RollbackResult, error) {
	ifMatch, err := sites.ParseIfMatch(in.IfMatch, s.Deps.PersonalSitesEnabled)
	if err != nil {
		return RollbackResult{}, err
	}
	site, unlock, unchanged, err := s.prepareRollback(ctx, in.User.ID, in.Name, in.ExpectedID, in.CheckID, in.Version)
	defer unlock()
	if err != nil {
		return RollbackResult{Site: site}, err
	}
	if ifMatch > 0 && site.ActiveVersion != ifMatch {
		return RollbackResult{Site: site}, sites.VersionChanged(site.ActiveVersion)
	}
	var prev int
	if !unchanged {
		site, prev, err = s.rollbackTransaction(ctx, site, in.Version, in.User)
	}
	if err != nil {
		return RollbackResult{Site: site}, err
	}
	moved := !unchanged && prev != site.ActiveVersion
	return RollbackResult{Site: site, Unchanged: unchanged, PreviousVersion: prev, Moved: moved, FileBytes: s.FileBytes(site)}, nil
}
