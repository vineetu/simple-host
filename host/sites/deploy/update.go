package deploy

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log"

	accounts "github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/sites/repo"
	"github.com/vsriram/simple-host/host/storage"
	"github.com/vsriram/simple-host/host/tarball"
)

// UpdateInput contains a deploy's files and request facts.
type UpdateInput struct {
	Access                   string
	Viewers                  []string
	User                     *model.User
	Name, ArchiveSHA, Author string
	Files                    map[string][]byte
	Publish                  bool
	Photos                   []Photo
	Reconcile                bool
	CreateIfMissing          bool
	Keep                     bool
	Remove                   []string
	ExpectedID               string
	CheckID                  bool
	// IfMatch, when not 0, is the active version the caller last read
	// (If-Match): the update is refused with version_changed when the site
	// has moved on since, or does not exist.
	IfMatch       int
	Budget        int64
	LimitOverride bool
	Limit         int64
}

// Photo is an uploaded image to merge into the live site.
type Photo struct {
	Name string
	Data []byte
}

// UpdateResult describes the stored version and site.
type UpdateResult struct {
	Site    model.Site
	Version int
	// PreviousVersion is the version that was live before a published update.
	PreviousVersion int
	Published       bool
	Created         *CreateResult
	PhotoPaths      []string
	Reconciled      bool
	Limit           int64
	// FileBytes is the site's footprint on disk, measured under the deploy locks.
	FileBytes int64
}

// Update writes a new version and publishes it when requested. It holds the
// account and site deploy locks from start to end: a keep or photo update
// merges the live files under them, and ?create=1 creates the site under them
// when it does not exist yet (Created), so nothing is read, released and then
// acted on.
func (s Service) Update(ctx context.Context, in UpdateInput) (UpdateResult, error) {
	if in.IfMatch > 0 {
		in.CreateIfMissing = false // If-Match names an existing version
	}
	if in.LimitOverride {
		in.Budget = in.Limit
	}
	unlockAccount := s.LockAccount(in.User.ID)
	defer unlockAccount()
	unlockSite := s.LockSite(in.User.ID, in.Name)
	defer unlockSite()
	if in.Photos != nil {
		var override bool
		in.Limit, override = s.Accounts.SiteLimit(ctx, in.User, s.Deps.Limits, sites.Upload.Body)
		in.Budget = sites.ExtractBudget(in.Limit, override)
	}
	if in.Keep || in.Photos != nil {
		files, digest, err := s.mergePatch(ctx, in)
		if err != nil {
			return UpdateResult{Limit: in.Limit}, err
		}
		in.Files, in.ArchiveSHA = files, digest
	}
	var paths []string
	if in.Photos != nil {
		var err error
		if paths, err = mergePhotos(ctx, in.Files, in.Photos, in.Reconcile); err != nil {
			return UpdateResult{}, err
		}
		if in.Reconcile {
			return s.reconciledPhotos(ctx, in, paths)
		}
		if in.Files, in.ArchiveSHA, err = s.PrepareFiles(FilePreparationInput{Files: in.Files, Limit: in.Budget, Override: true}); err != nil {
			return UpdateResult{Limit: in.Limit}, err
		}
	}
	if in.CreateIfMissing {
		created, err := s.createLocked(ctx, CreateInput{Access: in.Access, Viewers: in.Viewers, User: in.User, Name: in.Name, Files: in.Files, ArchiveSHA: in.ArchiveSHA, Author: in.Author, OrUpdate: true})
		if err != nil || !created.Exists {
			return UpdateResult{Created: &created}, err
		}
	}
	return s.updateExisting(ctx, in, paths)
}

// reconciledPhotos answers a photo batch retried with X-Photo-Reconcile whose
// photos are all already in the live site: nothing new is stored.
func (s Service) reconciledPhotos(ctx context.Context, in UpdateInput, paths []string) (UpdateResult, error) {
	site, err := repo.GetSiteByUser(ctx, s.DB, in.User.ID, in.Name)
	if err != nil {
		return UpdateResult{}, &sites.Error{Code: "photo_read_failed"}
	}
	return UpdateResult{Site: site, PhotoPaths: paths, Reconciled: true, FileBytes: s.FileBytes(site)}, nil
}

// updateExisting stores the next version of an existing site while the
// deploy locks are held, then promotes it when in.Publish is set.
func (s Service) updateExisting(ctx context.Context, in UpdateInput, paths []string) (UpdateResult, error) {
	site, err := repo.GetSiteByUser(ctx, s.DB, in.User.ID, in.Name)
	if errors.Is(err, sql.ErrNoRows) {
		return UpdateResult{}, &sites.Error{Code: "deploy_site_not_found"}
	}
	if err != nil {
		return UpdateResult{}, err
	}
	if in.Access == "specific" && !site.NamedViewers {
		return UpdateResult{}, &sites.Error{Code: "access_differs", Message: "the existing site is not specific; use set_site_access / grant_site_viewer before updating"}
	}
	if site.Suspended() {
		return UpdateResult{}, &sites.Error{Code: "site_suspended", Message: site.SuspendedReason()}
	}
	n, liveBefore, err := s.commitNextVersion(ctx, in, site)
	if err != nil {
		return UpdateResult{}, err
	}
	if !in.Publish {
		s.PruneVersions(ctx, sites.PruneInput{SiteID: site.ID, UserID: site.UserID, SiteName: in.Name, ActiveVersion: site.ActiveVersion, SiteKeep: site.KeepVersions})
		return UpdateResult{Site: site, Version: n, FileBytes: s.FileBytes(site)}, nil
	}
	if err := s.Disk.UpdateCurrent(site.UserID, in.Name, n); err != nil {
		log.Printf("updateSite: promote %s v%d after commit: %v", in.Name, n, err)
		return UpdateResult{}, err
	}
	if in.User.Handle.Valid && in.User.Handle.String != "" {
		if err := s.Disk.EnsureHandleLink(in.User.Handle.String, in.User.ID); err != nil {
			log.Printf("updateSite: ensure handle link %s: %v", in.User.Handle.String, err)
		}
		s.RequestCert(in.User.Handle.String)
	}
	s.PruneVersions(ctx, sites.PruneInput{SiteID: site.ID, UserID: site.UserID, SiteName: in.Name, ActiveVersion: n, SiteKeep: site.KeepVersions})
	site.ActiveVersion = n
	return UpdateResult{Site: site, Version: n, PreviousVersion: liveBefore, Published: true, PhotoPaths: paths, FileBytes: s.FileBytes(site)}, nil
}

// lockForUpdate takes the account's file-caps row lock (when caps are on)
// and the site row lock inside tx.
func (s Service) lockForUpdate(ctx context.Context, tx *sql.Tx, userID, siteID string) error {
	if s.Deps.FileCaps {
		if _, err := accounts.LockProfile(ctx, tx, userID); err != nil {
			return err
		}
	}
	if err := repo.LockSiteForUpdate(ctx, tx, siteID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return &sites.Error{Code: "site_not_found", Message: "site not found"}
		}
		return err
	}
	return nil
}

// commitNextVersion writes and commits the site's next version, activating it
// in the database when in.Publish is set. It returns the new version number
// and the version that was live before.
func (s Service) commitNextVersion(ctx context.Context, in UpdateInput, site model.Site) (n, liveBefore int, err error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()
	if err := s.lockForUpdate(ctx, tx, in.User.ID, site.ID); err != nil {
		return 0, 0, err
	}
	liveBefore, err = repo.SiteActiveVersion(ctx, tx, site.ID)
	if err != nil {
		return 0, 0, err
	}
	if in.IfMatch > 0 && liveBefore != in.IfMatch {
		return 0, 0, sites.VersionChanged(liveBefore)
	}
	if err := s.CreateGate(ctx, tx, in.User, in.Name); err != nil {
		return 0, 0, err
	}
	max, err := repo.GetMaxVersionNumber(ctx, tx, site.ID)
	if err != nil {
		return 0, 0, err
	}
	n = max + 1
	if err := s.CheckFileCaps(ctx, in.User, site, in.Files, n, in.Publish); err != nil {
		return 0, 0, err
	}
	path := fmt.Sprintf("by-id/%s/%s/v%d/", site.UserID, in.Name, n)
	if err := s.ValidateRedirects(in.Files); err != nil {
		return 0, 0, err
	}
	version, err := repo.CreateVersion(ctx, tx, site.ID, n, path, in.ArchiveSHA, in.Author)
	if err != nil {
		return 0, 0, err
	}
	if err := repo.UpdateSiteSummary(ctx, tx, site.ID, sites.ExtractSiteSummary(in.Files)); err != nil {
		return 0, 0, err
	}
	if err := s.Disk.WriteFiles(ctx, site.UserID, in.Name, n, in.Files); err != nil {
		return 0, 0, err
	}
	if err := s.EnsureAccessMarker(site); err != nil {
		return 0, 0, err
	}
	if in.Publish {
		if err := repo.ActivateVersion(ctx, tx, version.ID); err != nil {
			return 0, 0, err
		}
		if err := repo.UpdateSiteActiveVersion(ctx, tx, site.ID, n); err != nil {
			return 0, 0, err
		}
	} else if err := repo.MarkVersionReady(ctx, tx, version.ID); err != nil {
		return 0, 0, err
	}
	return n, liveBefore, tx.Commit()
}

// mergePatch reads the live files while the update owns the deploy locks.
func (s Service) mergePatch(ctx context.Context, in UpdateInput) (map[string][]byte, string, error) {
	files := in.Files
	totalLimit, fileLimit, entryLimit := tarball.FileLimits(in.Budget, s.Deps.MaxFilesPerSite)
	removed, err := normalizeFilePatch(files, in.Remove)
	if err == nil {
		var site model.Site
		site, err = repo.GetSiteByUser(ctx, s.DB, in.User.ID, in.Name)
		if err == nil && in.CheckID && site.ID != in.ExpectedID {
			site, err = model.Site{}, sql.ErrNoRows
		}
		if errors.Is(err, sql.ErrNoRows) {
			err = nil
		} else if err == nil && site.ActiveVersion >= 1 {
			var total int64
			for _, b := range files {
				total += int64(len(b))
			}
			err = s.Disk.WalkFiles(ctx, storage.FileVersion{UserID: site.UserID, SiteName: site.Name, Version: site.ActiveVersion}, func(file storage.SiteFile, err error) error {
				rel := file.Name
				if file.Mode.IsRegular() {
					// Replaced and removed files need neither metadata nor bytes.
					if removed[rel] {
						return nil
					}
					if _, ok := files[rel]; ok {
						return nil
					}
				}
				if err != nil {
					return err
				}
				if !file.Mode.IsRegular() {
					return errors.New("live version contains a non-regular file")
				}
				total += file.Size
				if total > totalLimit || file.Size > fileLimit || len(files)+1 > entryLimit {
					return fmt.Errorf("%w: merged files exceed the site limit", tarball.ErrTooLarge)
				}
				body, err := io.ReadAll(file.Reader)
				if err != nil {
					return err
				}
				files[rel] = body
				return nil
			})
			if err == nil {
				for p := range removed {
					delete(files, p)
				}
			}
		}
	}
	if err != nil {
		code := "patch_invalid"
		if errors.Is(err, tarball.ErrTooLarge) {
			code = "patch_too_large"
		}
		return nil, "", &sites.Error{Code: code, Message: err.Error()}
	}
	return s.PrepareFiles(FilePreparationInput{Files: files, Limit: in.Budget, Override: true})
}
