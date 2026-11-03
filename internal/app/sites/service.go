package sites

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/lib/pq"
	"github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/model"
	repo "github.com/vsriram/simple-host/internal/repository/sites"
	"github.com/vsriram/simple-host/internal/storage"
)

// Service owns site lifecycle operations.
type Service struct {
	database        *sql.DB
	disk            *storage.DiskStorage
	lockSite        func(string, string) func()
	previewAccounts map[string]bool
	previewTTL      time.Duration
}

// CreateInput contains the values and non-HTTP dependencies for a locked create.
type CreateInput struct {
	User                                              *db.User
	Name, ContentHost, SiteDomain, ArchiveSHA, Author string
	Files                                             map[string][]byte
	OrUpdate, FileCaps, DeployEnabled                 bool
	CapCutoff                                         string
	CapLimits                                         func() (int, int)
	Gate                                              func(*sql.Tx) error
	Assign                                            func()
	Summarize                                         func(map[string][]byte) model.SiteSummary
	Conflict                                          func(context.Context, string, string) (string, bool)
	Retention                                         func(model.Site) (int, error)
	Marker                                            func(context.Context, model.Site) error
	RequestCert                                       func(string)
	HasLiveFamilies                                   func() bool
	SyncRedirect                                      func(string)
	Notify                                            func(model.Site)
	PersonalGate                                      func() error
}

// Create acquires the deploy locks before creating a site.
func (s *Service) Create(ctx context.Context, in CreateInput, lockAccount func(string) func()) (model.Site, bool, error) {
	unlock := s.LockCreate(in.User.ID, in.Name, lockAccount)
	defer unlock()
	return s.CreateLocked(ctx, in)
}

// CreateLocked creates and promotes a site while the caller holds its locks.
func (s *Service) CreateLocked(ctx context.Context, in CreateInput) (model.Site, bool, error) {
	if in.PersonalGate != nil {
		if err := in.PersonalGate(); err != nil {
			return model.Site{}, false, err
		}
	}
	tx, site, exists, err := s.PrepareCreate(ctx, in.Gate, in.User, in.Assign, in.Name, in.ContentHost, in.SiteDomain, in.Files, in.Summarize, in.OrUpdate, in.Conflict)
	if tx != nil {
		defer tx.Rollback()
	}
	if exists || err != nil {
		return site, exists, err
	}
	if err := s.CheckFileCaps(ctx, in.FileCaps, in.CapCutoff, in.CapLimits, in.User.ID, site, in.Files, 1, true, func() (int, error) { return in.Retention(site) }); err != nil {
		return site, false, err
	}
	site, err = s.CompleteCreate(ctx, tx, site, in.Files, in.ArchiveSHA, in.Author, in.User.Handle, in.Marker, in.RequestCert, in.HasLiveFamilies, in.SyncRedirect, in.DeployEnabled)
	if err == nil && in.Notify != nil {
		in.Notify(site)
	}
	return site, false, err
}

// New constructs a site lifecycle service using the instance's shared locks.
func New(database *sql.DB, disk *storage.DiskStorage, lockSite func(string, string) func(), previewAccounts map[string]bool, previewTTL time.Duration) *Service {
	return &Service{database: database, disk: disk, lockSite: lockSite, previewAccounts: previewAccounts, previewTTL: previewTTL}
}

// PreviewExpiry returns the expiry for a preview account, or nil for a permanent site.
func (s *Service) PreviewExpiry(username string) *time.Time {
	if len(s.previewAccounts) == 0 || !s.previewAccounts[strings.ToLower(username)] {
		return nil
	}
	t := time.Now().Add(s.previewTTL)
	return &t
}

// SweepExpiredSites removes expired preview site rows and then their files.
func (s *Service) SweepExpiredSites() {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	expired, err := repo.ListExpiredSites(ctx, s.database)
	if err != nil {
		log.Printf("expiry sweep: list failed: %v", err)
		return
	}
	for _, site := range expired {
		unlock := s.lockSite(site.UserID, site.Name)
		if err := repo.DeleteSite(ctx, s.database, site.ID); err != nil {
			log.Printf("expiry sweep: delete row %s (%s): %v", site.Name, site.ID, err)
			unlock()
			continue
		}
		if err := s.disk.DeleteSite(site.UserID, site.Name); err != nil {
			log.Printf("expiry sweep: delete disk %s: %v", site.Name, err)
		}
		unlock()
		log.Printf("expiry sweep: removed preview site %q", site.Name)
	}
}

// ListVersions returns a site's retained versions in repository order.
func (s *Service) ListVersions(ctx context.Context, siteID string) ([]model.Version, error) {
	return repo.ListVersionsBySite(ctx, s.database, siteID)
}

// SetVisibility updates visibility and the corresponding on-disk marker under the site lock.
func (s *Service) SetVisibility(ctx context.Context, userID, name, visibility string, allowed func(model.Site) (bool, string), marker func(context.Context, model.Site) (bool, error)) error {
	unlock := s.lockSite(userID, name)
	defer unlock()
	site, err := db.GetSiteByUser(ctx, s.database, userID, name)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return &Error{Code: "site_not_found"}
		}
		return err
	}
	if ok, reason := allowed(site); !ok {
		return &Error{Code: "site_suspended", Message: reason}
	}
	if err := repo.SetSiteVisibility(ctx, s.database, site.ID, visibility); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return &Error{Code: "not_found", Message: "site not found"}
		}
		return err
	}
	// The marker carries the change to every address at once (unlisted.go).
	site.Visibility = visibility
	want, err := marker(ctx, site)
	if err == nil {
		err = s.disk.SetUnlisted(site.UserID, site.Name, want)
	}
	if err != nil {
		log.Printf("visibility: marker for site %s: %v", site.ID, err)
		return &Error{Code: "marker_failed", Message: "saved, but the site's files could not be marked; try again"}
	}
	return nil
}

// RenameResult contains the values needed to describe a completed rename.
type RenameResult struct {
	Site   model.Site
	OldURL string
	NewURL string
	Domain string
}

// Rename moves the files and updates the site and its old-name redirect.
func (s *Service) Rename(ctx context.Context, userID, oldName, newName string, newSiteURL func() string, allowed func(model.Site) (bool, string), conflict func(context.Context, string, string) (string, bool), siteURL func(model.Site) string) (RenameResult, error) {
	var result RenameResult
	if _, err := db.GetSiteByUser(ctx, s.database, userID, oldName); err != nil {
		return result, &Error{Code: "site_not_found"}
	}
	first, second := oldName, newName
	if second < first {
		first, second = second, first
	}
	unlockFirst := s.lockSite(userID, first)
	defer unlockFirst()
	unlockSecond := s.lockSite(userID, second)
	defer unlockSecond()
	site, err := db.GetSiteByUser(ctx, s.database, userID, oldName)
	if err != nil {
		return result, &Error{Code: "site_not_found"}
	}
	if ok, reason := allowed(site); !ok {
		return result, &Error{Code: "site_suspended", Message: reason}
	}
	if _, err := db.GetSiteByUser(ctx, s.database, userID, newName); err == nil {
		return result, &Error{Code: "site_exists"}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return result, err
	}
	if msg, held := conflict(ctx, userID, newName); held {
		return result, &Error{Code: "recently_deleted", Message: msg}
	}
	result.Site = site
	result.OldURL = siteURL(site)
	result.NewURL = newSiteURL()
	if site.CustomDomain.Valid {
		result.Domain = site.CustomDomain.String
	}
	domains := []string{result.Domain}
	if info, ok, err := db.GetSiteDomainInfo(ctx, s.database, site.ID); err != nil {
		return result, err
	} else if ok && info.PreviousDomain != "" {
		domains = append(domains, info.PreviousDomain)
	}
	if err := s.disk.RenameSite(userID, oldName, newName, domains...); err != nil {
		return result, &Error{Code: "move_failed"}
	}
	err = func() error {
		tx, err := s.database.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if err := repo.RenameSite(ctx, tx, site.ID, newName, result.NewURL); err != nil {
			return err
		}
		if err := repo.KeepOldSiteName(ctx, tx, userID, site.ID, oldName, newName); err != nil {
			return err
		}
		return tx.Commit()
	}()
	if err != nil {
		_ = s.disk.RenameSite(userID, newName, oldName, domains...)
		return result, err
	}
	return result, nil
}

// PruneVersions removes database rows before their version directories.
func (s *Service) PruneVersions(ctx context.Context, siteID, userID, siteName string, activeVersion, siteKeep int, retention func(context.Context, string, int) (int, error), floor func([]model.Version, int, int, int) int, pinned func(context.Context, string, string, int) int) []int {
	keep, err := retention(ctx, userID, siteKeep)
	if err != nil {
		log.Printf("prune versions: %v", err)
		return nil
	}
	versions, err := repo.ListVersionsBySite(ctx, s.database, siteID)
	if err != nil {
		log.Printf("prune versions: %v", err)
		return nil
	}
	keepFrom := floor(versions, keep, activeVersion, siteKeep)
	if keepFrom < 2 {
		return nil
	}
	if keepFrom = pinned(ctx, userID, siteName, keepFrom); keepFrom < 2 {
		return nil
	}
	removed, err := repo.PruneVersions(ctx, s.database, siteID, keepFrom, activeVersion)
	if err != nil {
		log.Printf("prune versions for %s: %v", siteName, err)
		return nil
	}
	for _, n := range removed {
		if err := s.disk.DeleteVersion(userID, siteName, n); err != nil {
			log.Printf("prune versions for %s: v%d row removed but files remain: %v", siteName, n, err)
		}
	}
	return removed
}

// SetKeepVersions updates a site's retention and applies it while holding its lock.
func (s *Service) SetKeepVersions(ctx context.Context, userID, name string, keep int, allowed func(model.Site) (bool, string), retention func(context.Context, string, int) (int, error), prune func(context.Context, model.Site, int) []int) (model.Site, int, []int, error) {
	unlock := s.lockSite(userID, name)
	defer unlock()
	site, err := db.GetSiteByUser(ctx, s.database, userID, name)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return site, 0, nil, &Error{Code: "site_not_found"}
		}
		return site, 0, nil, err
	}
	if ok, reason := allowed(site); !ok {
		return site, 0, nil, &Error{Code: "site_suspended", Message: reason}
	}
	if err := repo.SetSiteKeepVersions(ctx, s.database, site.ID, keep); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return site, 0, nil, &Error{Code: "not_found", Message: "site not found"}
		}
		return site, 0, nil, err
	}
	effective, err := retention(ctx, userID, keep)
	if err != nil {
		return site, 0, nil, err
	}
	removed := prune(ctx, site, keep)
	if removed == nil {
		removed = []int{}
	}
	return site, effective, removed, nil
}

// EffectiveKeepVersions selects the site retention or the instance default.
func EffectiveKeepVersions(siteKeep, instanceKeep int) int {
	if siteKeep > 0 {
		return siteKeep
	}
	return instanceKeep
}

// DiscardVersion tombstones a never-published version and deletes its files.
func (s *Service) DiscardVersion(ctx context.Context, userID, name, expectedSiteID string, checkSiteID bool, n int, lockAccount func(string) func(), allowed func(model.Site) (bool, string)) (model.Site, error) {
	unlockAccount := lockAccount(userID)
	defer unlockAccount()
	unlock := s.lockSite(userID, name)
	defer unlock()
	site, err := db.GetSiteByUser(ctx, s.database, userID, name)
	if err == nil && checkSiteID && site.ID != expectedSiteID {
		err = sql.ErrNoRows
	}
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return site, &Error{Code: "site_not_found"}
		}
		return site, err
	}
	if ok, reason := allowed(site); !ok {
		return site, &Error{Code: "site_suspended", Message: reason}
	}
	versions, err := repo.ListVersionsBySite(ctx, s.database, site.ID)
	if err != nil {
		return site, err
	}
	var found *model.Version
	for i := range versions {
		if versions[i].VersionNumber == n {
			found = &versions[i]
			break
		}
	}
	if found == nil || found.Status == "uploading" {
		return site, &Error{Code: "version_not_found"}
	}
	if n == site.ActiveVersion {
		return site, &Error{Code: "version_live", Message: fmt.Sprintf("version %d is live; only a version that was never live can be discarded", n)}
	}
	if found.Status == "active" {
		return site, &Error{Code: "version_published", Message: fmt.Sprintf("version %d has been live; kept versions are removed by retention, not discarded", n)}
	}
	gone, err := repo.DiscardReadyVersion(ctx, s.database, site.ID, n)
	if err != nil {
		return site, err
	}
	if !gone {
		return site, &Error{Code: "version_not_found"}
	}
	if err := s.disk.DeleteVersion(site.UserID, site.Name, n); err != nil {
		log.Printf("discard version for %s: v%d discarded but files remain: %v", site.Name, n, err)
	}
	return site, nil
}

// ActivateVersion updates the transaction and serving files, compensating if commit fails.
// The caller holds the site lock and has already authorized the transaction.
func (s *Service) ActivateVersion(ctx context.Context, tx *sql.Tx, site model.Site, version int) (int, error) {
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
	// Change served files only after the database steps, and restore them if commit fails.
	if err := s.disk.UpdateCurrent(site.UserID, site.Name, version); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		if prev != version {
			if rerr := s.disk.UpdateCurrent(site.UserID, site.Name, prev); rerr != nil {
				log.Printf("rollback site_id=%s: commit failed (%v) and serving v%d again failed: %v", site.ID, err, prev, rerr)
			}
		}
		return 0, err
	}
	return prev, nil
}

// CanActivateVersion reports whether a version is eligible for rollback.
func (s *Service) CanActivateVersion(ctx context.Context, siteID string, version int) (bool, error) {
	versions, err := repo.ListVersionsBySite(ctx, s.database, siteID)
	if err != nil {
		return false, err
	}
	for _, v := range versions {
		// A version whose upload never finished is not one to go live.
		if v.VersionNumber == version && v.Status != "uploading" {
			return true, nil
		}
	}
	return false, nil
}

// BeginRollback starts the rollback transaction and locks the site row.
func (s *Service) BeginRollback(ctx context.Context, siteID string) (*sql.Tx, error) {
	tx, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	if err := repo.LockSiteForUpdate(ctx, tx, siteID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return tx, &Error{Code: "rollback_site_not_found"}
		}
		return tx, err
	}
	return tx, nil
}

// PrepareRollback locks the site and checks the requested version before the transaction.
// The caller must release the returned lock when it is non-nil.
func (s *Service) PrepareRollback(ctx context.Context, userID, name, expectedID string, checkID bool, version int) (model.Site, func(), bool, error) {
	unlock := s.lockSite(userID, name)
	site, err := db.GetSiteByUser(ctx, s.database, userID, name)
	if err == nil && checkID && site.ID != expectedID {
		err = sql.ErrNoRows
	}
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return site, unlock, false, &Error{Code: "site_not_found"}
		}
		return site, unlock, false, err
	}
	if site.Suspended() {
		return site, unlock, false, &Error{Code: "site_suspended", Message: site.SuspendedReason()}
	}
	if site.ActiveVersion == version {
		return site, unlock, true, nil
	}
	found, err := s.CanActivateVersion(ctx, site.ID, version)
	if err != nil {
		return site, unlock, false, err
	}
	if !found {
		return site, unlock, false, &Error{Code: "rollback_version_not_found"}
	}
	return site, unlock, false, nil
}

// FinishRollback activates a version and emits the existing deployment notification.
func (s *Service) FinishRollback(ctx context.Context, tx *sql.Tx, site model.Site, version int, notify func(string, int)) (model.Site, error) {
	prev, err := s.ActivateVersion(ctx, tx, site, version)
	if err != nil {
		return site, err
	}
	if prev != version {
		notify(site.ID, prev)
	}
	site.ActiveVersion = version
	return site, nil
}

// RollbackTransaction runs the transaction-time gate and activates the version.
// The gate receives the locked transaction and returns an authorization error.
func (s *Service) RollbackTransaction(ctx context.Context, site model.Site, version int, gate func(*sql.Tx) error, notify func(string, int)) (model.Site, error) {
	tx, err := s.BeginRollback(ctx, site.ID)
	if tx != nil {
		defer tx.Rollback()
	}
	if err != nil {
		return site, err
	}
	if err := gate(tx); err != nil {
		return site, err
	}
	return s.FinishRollback(ctx, tx, site, version, notify)
}

// CommitFirstVersion writes and activates the first version before committing.
// The caller retains its locks and performs promotion only after this returns.
func (s *Service) CommitFirstVersion(ctx context.Context, tx *sql.Tx, site model.Site, files map[string][]byte, archiveSHA, author string) error {
	const versionNumber = 1
	diskPath := fmt.Sprintf("by-id/%s/%s/v%d/", site.UserID, site.Name, versionNumber)
	version, err := repo.CreateVersion(ctx, tx, site.ID, versionNumber, diskPath, archiveSHA, author)
	if err != nil {
		return err
	}
	// Write the new version dir (not yet live) before committing.
	if err := s.disk.WriteFiles(ctx, site.UserID, site.Name, versionNumber, files); err != nil {
		return err
	}
	if err := repo.ActivateVersion(ctx, tx, version.ID); err != nil {
		return err
	}
	if err := repo.UpdateSiteActiveVersion(ctx, tx, site.ID, versionNumber); err != nil {
		return err
	}
	return tx.Commit()
}

// PromoteFirstVersion publishes the committed files and ensures the handle link.
func (s *Service) PromoteFirstVersion(site model.Site, handle string, requestCert func(string)) error {
	if err := s.disk.UpdateCurrent(site.UserID, site.Name, 1); err != nil {
		return err
	}
	// Non-fatal: the legacy subdomain still works if the handle link fails.
	if handle != "" {
		if err := s.disk.EnsureHandleLink(handle, site.UserID); err != nil {
			log.Printf("createSite: ensure handle link %s: %v", handle, err)
		}
		requestCert(handle)
	}
	return nil
}

// PrepareNewSite records the summary and releases any old-name redirect.
func (s *Service) PrepareNewSite(ctx context.Context, tx *sql.Tx, site model.Site, summary model.SiteSummary) error {
	if err := repo.UpdateSiteSummary(ctx, tx, site.ID, summary); err != nil {
		return err
	}
	// A renamed site's old name is free for a new site, which wins: links to
	// the name stop following the renamed one.
	return repo.DropOldSiteName(ctx, tx, site.UserID, site.Name)
}

// CreateSiteRow locks the owner handle and creates the site row.
func (s *Service) CreateSiteRow(ctx context.Context, tx *sql.Tx, userID, name, contentHost, siteDomain string, handle *sql.NullString, username string) (model.Site, bool, error) {
	if err := repo.LockUserHandle(ctx, tx, userID, handle); err != nil {
		return model.Site{}, false, err
	}
	var siteURL string
	if handle.Valid && handle.String != "" {
		siteURL = fmt.Sprintf("https://%s/%s/%s/", contentHost, handle.String, name)
	} else {
		siteURL = fmt.Sprintf("https://%s.%s", name, siteDomain)
	}
	site, err := repo.CreateSite(ctx, tx, userID, name, siteURL, s.PreviewExpiry(username))
	var pqErr *pq.Error
	if errors.As(err, &pqErr) && pqErr.Code == "23505" {
		return site, true, &Error{Code: "site_exists"}
	}
	return site, true, err
}

// CreatePreparedSite creates the row and records its summary in the same transaction.
func (s *Service) CreatePreparedSite(ctx context.Context, tx *sql.Tx, userID, name, contentHost, siteDomain string, handle *sql.NullString, username string, summary model.SiteSummary) (model.Site, bool, error) {
	site, attempted, err := s.CreateSiteRow(ctx, tx, userID, name, contentHost, siteDomain, handle, username)
	if err != nil {
		return site, attempted, err
	}
	if err := s.PrepareNewSite(ctx, tx, site, summary); err != nil {
		return site, false, err
	}
	return site, true, nil
}

// BeginPreparedSite starts creation and prepares its row under the transaction.
// The caller must roll back any non-nil transaction.
func (s *Service) BeginPreparedSite(ctx context.Context, gate func(*sql.Tx) error, userID, name, contentHost, siteDomain string, handle *sql.NullString, username string, summary model.SiteSummary) (*sql.Tx, model.Site, bool, error) {
	tx, err := s.BeginCreateTransaction(ctx, gate)
	if err != nil {
		return tx, model.Site{}, false, err
	}
	site, attempted, err := s.CreatePreparedSite(ctx, tx, userID, name, contentHost, siteDomain, handle, username, summary)
	return tx, site, attempted, err
}

// PrepareCreate begins the create transaction and resolves any name conflict.
// The caller must roll back a non-nil transaction.
func (s *Service) PrepareCreate(ctx context.Context, gate func(*sql.Tx) error, user *db.User, assign func(), name, contentHost, siteDomain string, files map[string][]byte, summarize func(map[string][]byte) model.SiteSummary, orUpdate bool, conflict func(context.Context, string, string) (string, bool)) (*sql.Tx, model.Site, bool, error) {
	summary := summarize(files)
	s.EnsureOwnerHandle(ctx, user, assign)
	tx, site, attempted, err := s.BeginPreparedSite(ctx, gate, user.ID, name, contentHost, siteDomain, &user.Handle, user.Username, summary)
	var siteErr *Error
	if attempted && errors.As(err, &siteErr) && siteErr.Code == "site_exists" {
		exists, conflictErr := s.ResolveCreateConflict(ctx, tx, user.ID, name, orUpdate, conflict)
		return tx, site, exists, conflictErr
	}
	return tx, site, false, err
}

// ResolveCreateConflict rolls back before checking a recently deleted name.
func (s *Service) ResolveCreateConflict(ctx context.Context, tx *sql.Tx, userID, name string, orUpdate bool, conflict func(context.Context, string, string) (string, bool)) (bool, error) {
	tx.Rollback()
	if msg, held := conflict(ctx, userID, name); held {
		return false, &Error{Code: "recently_deleted", Message: msg}
	}
	if orUpdate {
		return true, nil
	}
	return false, &Error{Code: "site_exists", Message: "site already exists: use PUT to update it (PUT /v1/sites/" + name + " or PUT /v1/sites/" + name + "/files); add ?create=1 to a PUT to create or update in one call"}
}

// BeginCreateTransaction starts the create transaction and runs its authorization gate.
// The caller must roll back any non-nil transaction.
func (s *Service) BeginCreateTransaction(ctx context.Context, gate func(*sql.Tx) error) (*sql.Tx, error) {
	tx, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	return tx, gate(tx)
}

// CheckSiteQuota counts existing and recently deleted sites against the account limit.
func (s *Service) CheckSiteQuota(ctx context.Context, userID string, maximum func() int) (int, bool, error) {
	existing, err := db.CountSitesByUser(ctx, s.database, userID)
	if err != nil {
		return 0, false, err
	}
	max := maximum()
	return max, existing >= max, nil
}

// CheckCreateQuota applies the site limit to accounts subject to that limit.
func (s *Service) CheckCreateQuota(ctx context.Context, userID string, exempt bool, maximum func() int) (int, bool, error) {
	if exempt {
		return 0, false, nil
	}
	return s.CheckSiteQuota(ctx, userID, maximum)
}

// CheckNewSiteQuota returns the existing quota refusal for a new site.
func (s *Service) CheckNewSiteQuota(ctx context.Context, userID string, exempt bool, maximum func() int) error {
	max, reached, err := s.CheckCreateQuota(ctx, userID, exempt, maximum)
	if err != nil {
		return err
	}
	if reached {
		return &Error{Code: "site_quota_reached", Message: fmt.Sprintf("site quota reached: this account holds at most %d sites (sites in Recently deleted count until they are removed)", max)}
	}
	return nil
}

// PublishFirstVersion marks the committed site before promoting its files.
func (s *Service) PublishFirstVersion(ctx context.Context, site model.Site, handle string, marker func(context.Context, model.Site) error, requestCert func(string)) error {
	if err := marker(ctx, site); err != nil {
		log.Printf("createSite: unlisted marker %s: %v", site.Name, err)
	}
	return s.PromoteFirstVersion(site, handle, requestCert)
}

// QueueDeploy records a deployment for the deploy watcher when enabled.
func (s *Service) QueueDeploy(siteName string, enabled bool) {
	if enabled {
		queueFile := s.disk.DataDir() + "/.deploy-queue"
		if err := appendToFile(queueFile, siteName); err != nil {
			log.Printf("failed to queue deploy for %s: %v", siteName, err)
		}
	}
}

// FinalizeNewSite updates the returned site and queues its deployment.
func (s *Service) FinalizeNewSite(site model.Site, handle string, hasLiveFamilies func() bool, syncRedirect func(string), deployEnabled bool) model.Site {
	site.ActiveVersion = 1
	site.OwnerHandle = handle
	if hasLiveFamilies() {
		syncRedirect(site.ID)
	}
	s.QueueDeploy(site.Name, deployEnabled)
	return site
}

func appendToFile(path, line string) error {
	// Defense-in-depth: a newline would inject an extra line into the
	// deploy queue that the root deploy-watcher consumes. Site names are
	// already charset-validated upstream, but never rely solely on that.
	if strings.ContainsAny(line, "\n\r") {
		return errors.New("invalid queue entry")
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(line + "\n")
	return err
}

// EnsureOwnerHandle assigns and refreshes a missing owner handle before creation.
func (s *Service) EnsureOwnerHandle(ctx context.Context, user *db.User, assign func()) {
	if !user.Handle.Valid || user.Handle.String == "" {
		assign()
		if refetched, err := db.GetUserByUsername(ctx, s.database, user.Username); err == nil {
			*user = refetched
		}
	}
}

// LockCreate acquires account and site locks in deploy order.
func (s *Service) LockCreate(userID, name string, lockAccount func(string) func()) func() {
	unlockAccount := lockAccount(userID)
	unlockSite := s.lockSite(userID, name)
	return func() { unlockSite(); unlockAccount() }
}

// CommitAndPublishFirstVersion commits the first version before promoting it.
func (s *Service) CommitAndPublishFirstVersion(ctx context.Context, tx *sql.Tx, site model.Site, files map[string][]byte, archiveSHA, author, handle string, marker func(context.Context, model.Site) error, requestCert func(string)) error {
	if err := s.CommitFirstVersion(ctx, tx, site, files, archiveSHA, author); err != nil {
		return err
	}
	if err := s.PublishFirstVersion(ctx, site, handle, marker, requestCert); err != nil {
		log.Printf("createSite: promote %s v%d after commit: %v", site.Name, 1, err)
		return err
	}
	return nil
}

// CompleteCreate commits and promotes the first version before finalizing the site.
func (s *Service) CompleteCreate(ctx context.Context, tx *sql.Tx, site model.Site, files map[string][]byte, archiveSHA, author string, ownerHandle sql.NullString, marker func(context.Context, model.Site) error, requestCert func(string), hasLiveFamilies func() bool, syncRedirect func(string), deployEnabled bool) (model.Site, error) {
	var handle string
	if ownerHandle.Valid {
		handle = ownerHandle.String
	}
	if err := s.CommitAndPublishFirstVersion(ctx, tx, site, files, archiveSHA, author, handle, marker, requestCert); err != nil {
		return site, err
	}
	return s.FinalizeNewSite(site, handle, hasLiveFamilies, syncRedirect, deployEnabled), nil
}

// SiteExists reports whether the owner has a site with the given name.
func (s *Service) SiteExists(ctx context.Context, userID, name string) (bool, error) {
	_, err := db.GetSiteByUser(ctx, s.database, userID, name)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}
