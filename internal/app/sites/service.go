package sites

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lib/pq"
	accountapp "github.com/vsriram/simple-host/internal/app/accounts"
	"github.com/vsriram/simple-host/internal/eventdns"
	"github.com/vsriram/simple-host/internal/model"
	accounts "github.com/vsriram/simple-host/internal/repository/accounts"
	repo "github.com/vsriram/simple-host/internal/repository/sites"
	"github.com/vsriram/simple-host/internal/storage"
	"github.com/vsriram/simple-host/internal/tarball"
)

// Service owns site lifecycle operations.
type Service struct {
	accounts        *accountapp.Service
	database        *sql.DB
	disk            *storage.DiskStorage
	previewAccounts map[string]bool
	previewTTL      time.Duration
	deps            Deps
}

// Deps holds collaborators shared by site workflows.
type Deps struct {
	ContentHost, SiteDomain string
	PersonalSitesEnabled    bool
	CanSetRetention         func(context.Context, *model.User) bool
	DefaultRetention        int
	LockSite                func(string, string) func()
	MaxSites                func(context.Context, *model.User) int
	WantMarker              func(context.Context, model.Site) (bool, error)
	SiteURL                 func(model.Site) string
	RenameURL               func(string, string) string
	LockAccount             func(string) func()
	AccountRetention        func(context.Context, string, int) (int, error)
	RetainedFloor           func([]model.Version, int, int, int) int
	PinnedFloor             func(context.Context, string, string, int) int
	TeamKeys                bool
	TeamSitesReady          func(string) bool
	PaperSite               func(context.Context, *model.User, string)
	PaperState              func(context.Context, *model.User, string)
	Assign                  func(context.Context, *model.User)
	NotifyCreate            func(context.Context, *model.User, model.Site, map[string][]byte)
	NotifyRollback          func(string, int, int)
	NotifyUpdate            func(context.Context, *model.User, model.Site, int, int, bool, map[string][]byte)
	FileCaps                bool
	CapCutoff               string
	CapLimits               func(context.Context, *model.User) (int, int)
	Retention               func(context.Context, *model.User, model.Site) (int, error)
	Summarize               func(map[string][]byte) model.SiteSummary
	Conflict                func(context.Context, string, string) (string, bool)
	Marker                  func(context.Context, model.Site) error
	RequestCert             func(string)
	HasLiveFamilies         func() bool
	SyncRedirect            func(context.Context, string)
	DeployEnabled           bool
	NormalizePatch          func(map[string][]byte, []string) (map[string]bool, error)
	MaxFilesPerSite         int
	PhotoLimits             func(context.Context, *model.User) (int64, int64)
	ResponseState           func(model.Site, int, bool, bool) any
	WebsiteArchiveDefault   int64
	WebsiteLimit            func(context.Context, *model.User) (int64, bool)
	EventDNS                eventdns.Provider
	EventProbe              *eventdns.LiveProbe
	EventNamePeer           func(context.Context, string, string) (bool, error)
	EventDomains            []string
	EventTTL                time.Duration
	EventMaxClaims          int
	CertRequests            CertRequestDeps
	DeletedRetention        time.Duration
	ImageScanEnabled        bool
	CancelDomainCert        func(string)
	ExportResources         func(context.Context, string) ([]map[string]any, error)
	ExportSQLiteMaxMemory   int64
	DeletedTransaction      func(*sql.Tx) error
	IdleArchive             IdleArchiveDeps
	OwnerMarks              OwnerMarkDeps
	DomainCerts             DomainCertDeps
}

// CreateInput contains the values and non-HTTP dependencies for a locked create.
type CreateInput struct {
	User                     *model.User
	Name, ArchiveSHA, Author string
	Files                    map[string][]byte
	OrUpdate                 bool
}

// CreateResult describes a created site or an existing site selected for update.
type CreateResult struct {
	Site          model.Site
	Exists        bool
	FileBytes     int64
	ResponseState any
}

// Create creates and promotes a site while holding the account and site locks.
func (s *Service) Create(ctx context.Context, in CreateInput) (CreateResult, error) {
	if in.User == nil {
		return CreateResult{}, &Error{Code: "unauthorized"}
	}
	unlockAccount := s.deps.LockAccount(in.User.ID)
	defer unlockAccount()
	unlockSite := s.deps.LockSite(in.User.ID, in.Name)
	defer unlockSite()
	return s.createLocked(ctx, in)
}

// createLocked runs creation while the account and site locks are held.
func (s *Service) createLocked(ctx context.Context, in CreateInput) (CreateResult, error) {
	if err := authorizePersonalSite(in.User, s.deps.PersonalSitesEnabled); err != nil {
		return CreateResult{}, err
	}
	// A first deploy assigns a handle before the transaction builds the URL.
	if !in.User.Handle.Valid || in.User.Handle.String == "" {
		s.deps.Assign(ctx, in.User)
		if refetched, err := accounts.GetUserByUsername(ctx, s.database, in.User.Username); err == nil {
			*in.User = refetched
		}
	}
	tx, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return CreateResult{}, err
	}
	defer tx.Rollback()
	if err := s.createGate(ctx, tx, in.User, in.Name); err != nil {
		return CreateResult{}, err
	}
	if err := repo.LockUserHandle(ctx, tx, in.User.ID, &in.User.Handle); err != nil {
		return CreateResult{}, err
	}
	siteURL := fmt.Sprintf("https://%s.%s", in.Name, s.deps.SiteDomain)
	if in.User.Handle.Valid && in.User.Handle.String != "" {
		siteURL = fmt.Sprintf("https://%s/%s/%s/", s.deps.ContentHost, in.User.Handle.String, in.Name)
	}
	site, err := repo.CreateSite(ctx, tx, in.User.ID, in.Name, siteURL, s.previewExpiry(in.User.Username))
	var pqErr *pq.Error
	if errors.As(err, &pqErr) && pqErr.Code == "23505" {
		tx.Rollback()
		return s.resolveCreateConflict(ctx, in, site)
	}
	if err != nil {
		return CreateResult{Site: site}, err
	}
	if err := repo.UpdateSiteSummary(ctx, tx, site.ID, s.deps.Summarize(in.Files)); err != nil {
		return CreateResult{Site: site}, err
	}
	// A renamed site's old name is free for a new site, which wins: links to
	// the name stop following the renamed one.
	if err := repo.DropOldSiteName(ctx, tx, site.UserID, site.Name); err != nil {
		return CreateResult{Site: site}, err
	}
	if err := s.checkFileCaps(ctx, in.User, site, in.Files, 1, true); err != nil {
		return CreateResult{Site: site}, err
	}
	var handle string
	if in.User.Handle.Valid {
		handle = in.User.Handle.String
	}
	const versionNumber = 1
	diskPath := fmt.Sprintf("by-id/%s/%s/v%d/", site.UserID, site.Name, versionNumber)
	version, err := repo.CreateVersion(ctx, tx, site.ID, versionNumber, diskPath, in.ArchiveSHA, in.Author)
	if err != nil {
		return CreateResult{Site: site}, err
	}
	// Write the new version dir (not yet live) before committing.
	if err := s.disk.WriteFiles(ctx, site.UserID, site.Name, versionNumber, in.Files); err != nil {
		return CreateResult{Site: site}, err
	}
	if err := repo.ActivateVersion(ctx, tx, version.ID); err != nil {
		return CreateResult{Site: site}, err
	}
	if err := repo.UpdateSiteActiveVersion(ctx, tx, site.ID, versionNumber); err != nil {
		return CreateResult{Site: site}, err
	}
	if err := tx.Commit(); err != nil {
		return CreateResult{Site: site}, err
	}
	if err := s.deps.Marker(ctx, site); err != nil {
		log.Printf("createSite: unlisted marker %s: %v", site.Name, err)
	}
	if err := s.disk.UpdateCurrent(site.UserID, site.Name, 1); err != nil {
		log.Printf("createSite: promote %s v%d after commit: %v", site.Name, 1, err)
		return CreateResult{Site: site}, err
	}
	// Non-fatal: the legacy subdomain still works if the handle link fails.
	if handle != "" {
		if err := s.disk.EnsureHandleLink(handle, site.UserID); err != nil {
			log.Printf("createSite: ensure handle link %s: %v", handle, err)
		}
		s.deps.RequestCert(handle)
	}
	site.ActiveVersion = 1
	site.OwnerHandle = handle
	if s.deps.HasLiveFamilies() {
		s.deps.SyncRedirect(ctx, site.ID)
	}
	if s.deps.DeployEnabled {
		queueFile := s.disk.DataDir() + "/.deploy-queue"
		if err := appendToFile(queueFile, site.Name); err != nil {
			log.Printf("failed to queue deploy for %s: %v", site.Name, err)
		}
	}
	s.deps.NotifyCreate(ctx, in.User, site, in.Files)
	var fileBytes int64
	if footprint, err := s.disk.SiteFootprint(site.UserID, site.Name); err == nil {
		fileBytes = footprint.Total()
	}
	return CreateResult{Site: site, FileBytes: fileBytes, ResponseState: s.deps.ResponseState(site, 0, false, false)}, nil
}

// resolveCreateConflict checks the deleted-name hold after the transaction rolls back.
func (s *Service) resolveCreateConflict(ctx context.Context, in CreateInput, site model.Site) (CreateResult, error) {
	msg, held := s.deps.Conflict(ctx, in.User.ID, in.Name)
	return createConflict(in, site, msg, held)
}

// createConflict resolves a duplicate after the create transaction is rolled back.
func createConflict(in CreateInput, site model.Site, msg string, held bool) (CreateResult, error) {
	if held {
		return CreateResult{Site: site}, &Error{Code: "recently_deleted", Message: msg}
	}
	if in.OrUpdate {
		return CreateResult{Site: site, Exists: true}, nil
	}
	return CreateResult{Site: site}, &Error{Code: "site_exists", Message: "site already exists: use PUT to update it (PUT /v1/sites/" + in.Name + " or PUT /v1/sites/" + in.Name + "/files); add ?create=1 to a PUT to create or update in one call"}
}

// New constructs a site lifecycle service using the instance's shared locks.
func New(database *sql.DB, disk *storage.DiskStorage, previewAccounts map[string]bool, previewTTL time.Duration, deps Deps) *Service {
	return &Service{accounts: accountapp.New(database), database: database, disk: disk, previewAccounts: previewAccounts, previewTTL: previewTTL, deps: deps}
}

// previewExpiry returns the expiry for a preview account, or nil for a permanent site.
func (s *Service) previewExpiry(username string) *time.Time {
	if len(s.previewAccounts) == 0 || !s.previewAccounts[strings.ToLower(username)] {
		return nil
	}
	t := time.Now().Add(s.previewTTL)
	return &t
}

// SweepExpiredSites removes expired preview site rows and then their files.
func (s *Service) SweepExpiredSites(parents ...context.Context) {
	parent := context.Background()
	if len(parents) > 0 {
		parent = parents[0]
	}
	ctx, cancel := context.WithTimeout(parent, 60*time.Second)
	defer cancel()
	expired, err := repo.ListExpiredSites(ctx, s.database)
	if err != nil {
		ReportWorkerFailure(ctx, "list expired preview sites", err)
		log.Printf("expiry sweep: list failed: %v", err)
		return
	}
	for _, site := range expired {
		unlock := s.deps.LockSite(site.UserID, site.Name)
		if err := repo.DeleteSite(ctx, s.database, site.ID); err != nil {
			ReportWorkerFailure(ctx, "delete expired preview row", err)
			log.Printf("expiry sweep: delete row %s (%s): %v", site.Name, site.ID, err)
			unlock()
			continue
		}
		if err := s.disk.DeleteSite(site.UserID, site.Name); err != nil {
			ReportWorkerFailure(ctx, "delete expired preview files", err)
			log.Printf("expiry sweep: delete disk %s: %v", site.Name, err)
		}
		unlock()
		ReportWorkerProgress(ctx)
		log.Printf("expiry sweep: removed preview site %q", site.Name)
	}
}

// ListVersions returns a site's retained versions in repository order.
func (s *Service) ListVersions(ctx context.Context, siteID string) ([]model.Version, error) {
	return repo.ListVersionsBySite(ctx, s.database, siteID)
}

// VisibilityInput holds the values for SetVisibility.
type VisibilityInput struct{ UserID, Name, Visibility string }

// SetVisibility updates visibility and the corresponding on-disk marker under the site lock.
func (s *Service) SetVisibility(ctx context.Context, in VisibilityInput) error {
	userID, name, visibility := in.UserID, in.Name, in.Visibility
	unlock := s.deps.LockSite(userID, name)
	defer unlock()
	site, err := repo.GetSiteByUser(ctx, s.database, userID, name)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return &Error{Code: "site_not_found"}
		}
		return err
	}
	if site.Suspended() {
		return &Error{Code: "site_suspended", Message: site.SuspendedReason()}
	}
	if err := repo.SetSiteVisibility(ctx, s.database, site.ID, visibility); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return &Error{Code: "not_found", Message: "site not found"}
		}
		return err
	}
	// The marker carries the change to every address at once (unlisted.go).
	site.Visibility = visibility
	want, err := s.deps.WantMarker(ctx, site)
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

// RenameInput holds the values for Rename.
type RenameInput struct{ UserID, OldName, NewName, Handle string }

// Rename moves the files and updates the site and its old-name redirect.
func (s *Service) Rename(ctx context.Context, in RenameInput) (RenameResult, error) {
	userID, oldName, newName, handle := in.UserID, in.OldName, in.NewName, in.Handle
	var result RenameResult
	if _, err := repo.GetSiteByUser(ctx, s.database, userID, oldName); err != nil {
		return result, &Error{Code: "site_not_found"}
	}
	first, second := oldName, newName
	if second < first {
		first, second = second, first
	}
	unlockFirst := s.deps.LockSite(userID, first)
	defer unlockFirst()
	unlockSecond := s.deps.LockSite(userID, second)
	defer unlockSecond()
	site, err := repo.GetSiteByUser(ctx, s.database, userID, oldName)
	if err != nil {
		return result, &Error{Code: "site_not_found"}
	}
	if site.Suspended() {
		return result, &Error{Code: "site_suspended", Message: site.SuspendedReason()}
	}
	if _, err := repo.GetSiteByUser(ctx, s.database, userID, newName); err == nil {
		return result, &Error{Code: "site_exists"}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return result, err
	}
	if msg, held := s.deps.Conflict(ctx, userID, newName); held {
		return result, &Error{Code: "recently_deleted", Message: msg}
	}
	result.Site = site
	result.OldURL = s.deps.SiteURL(site)
	result.NewURL = s.deps.RenameURL(handle, newName)
	if site.CustomDomain.Valid {
		result.Domain = site.CustomDomain.String
	}
	domains := []string{result.Domain}
	if info, ok, err := repo.GetSiteDomainInfo(ctx, s.database, site.ID); err != nil {
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
	// The main address may change with the name (address families).
	s.deps.SyncRedirect(ctx, site.ID)
	return result, nil
}

// PruneInput holds the values for PruneVersions.
type PruneInput struct {
	SiteID, UserID, SiteName string
	ActiveVersion, SiteKeep  int
}

// PruneVersions removes database rows before their version directories.
func (s *Service) PruneVersions(ctx context.Context, in PruneInput) []int {
	siteID, userID, siteName, activeVersion, siteKeep := in.SiteID, in.UserID, in.SiteName, in.ActiveVersion, in.SiteKeep
	keep, err := s.deps.AccountRetention(ctx, userID, siteKeep)
	if err != nil {
		log.Printf("prune versions: %v", err)
		return nil
	}
	versions, err := repo.ListVersionsBySite(ctx, s.database, siteID)
	if err != nil {
		log.Printf("prune versions: %v", err)
		return nil
	}
	keepFrom := s.deps.RetainedFloor(versions, keep, activeVersion, siteKeep)
	if keepFrom < 2 {
		return nil
	}
	if keepFrom = s.deps.PinnedFloor(ctx, userID, siteName, keepFrom); keepFrom < 2 {
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

// CheckRetentionAllowed authorizes the retention setting before decoding its body.
func (s *Service) CheckRetentionAllowed(ctx context.Context, user *model.User) error {
	if !s.deps.CanSetRetention(ctx, user) {
		return &Error{Code: "keep_versions_fixed", Message: fmt.Sprintf("Simple Host keeps your %d latest versions", s.deps.DefaultRetention)}
	}
	return nil
}

// KeepVersionsInput holds the values for SetKeepVersions.
type KeepVersionsInput struct {
	UserID, Name string
	Keep         int
}

// SetKeepVersions updates a site's retention and applies it while holding its lock.
func (s *Service) SetKeepVersions(ctx context.Context, in KeepVersionsInput) (model.Site, int, []int, error) {
	userID, name, keep := in.UserID, in.Name, in.Keep
	unlock := s.deps.LockSite(userID, name)
	defer unlock()
	site, err := repo.GetSiteByUser(ctx, s.database, userID, name)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return site, 0, nil, &Error{Code: "site_not_found"}
		}
		return site, 0, nil, err
	}
	if site.Suspended() {
		return site, 0, nil, &Error{Code: "site_suspended", Message: site.SuspendedReason()}
	}
	if err := repo.SetSiteKeepVersions(ctx, s.database, site.ID, keep); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return site, 0, nil, &Error{Code: "not_found", Message: "site not found"}
		}
		return site, 0, nil, err
	}
	effective, err := s.deps.AccountRetention(ctx, userID, keep)
	if err != nil {
		return site, 0, nil, err
	}
	removed := s.PruneVersions(ctx, PruneInput{site.ID, site.UserID, site.Name, site.ActiveVersion, keep})
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

// DiscardInput holds the values for DiscardVersion.
type DiscardInput struct {
	UserID, Name, ExpectedSiteID string
	CheckSiteID                  bool
	Version                      int
}

// DiscardVersion tombstones a never-published version and deletes its files.
func (s *Service) DiscardVersion(ctx context.Context, in DiscardInput) (model.Site, error) {
	userID, name, expectedSiteID, checkSiteID, n := in.UserID, in.Name, in.ExpectedSiteID, in.CheckSiteID, in.Version
	unlockAccount := s.deps.LockAccount(userID)
	defer unlockAccount()
	unlock := s.deps.LockSite(userID, name)
	defer unlock()
	site, err := repo.GetSiteByUser(ctx, s.database, userID, name)
	if err == nil && checkSiteID && site.ID != expectedSiteID {
		err = sql.ErrNoRows
	}
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return site, &Error{Code: "site_not_found"}
		}
		return site, err
	}
	if site.Suspended() {
		return site, &Error{Code: "site_suspended", Message: site.SuspendedReason()}
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
func (s *Service) activateVersion(ctx context.Context, tx *sql.Tx, site model.Site, version int) (int, error) {
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
func (s *Service) canActivateVersion(ctx context.Context, siteID string, version int) (bool, error) {
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
func (s *Service) beginRollback(ctx context.Context, siteID string) (*sql.Tx, error) {
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
func (s *Service) prepareRollback(ctx context.Context, userID, name, expectedID string, checkID bool, version int) (model.Site, func(), bool, error) {
	unlock := s.deps.LockSite(userID, name)
	site, err := repo.GetSiteByUser(ctx, s.database, userID, name)
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
	found, err := s.canActivateVersion(ctx, site.ID, version)
	if err != nil {
		return site, unlock, false, err
	}
	if !found {
		return site, unlock, false, &Error{Code: "rollback_version_not_found"}
	}
	return site, unlock, false, nil
}

// FinishRollback activates a version and emits the existing deployment notification.
func (s *Service) finishRollback(ctx context.Context, tx *sql.Tx, site model.Site, version int) (model.Site, error) {
	prev, err := s.activateVersion(ctx, tx, site, version)
	if err != nil {
		return site, err
	}
	if prev != version {
		s.deps.NotifyRollback(site.ID, version, prev)
	}
	site.ActiveVersion = version
	return site, nil
}

// RollbackTransaction runs the transaction-time gate and activates the version.
// The gate receives the locked transaction and returns an authorization error.
func (s *Service) rollbackTransaction(ctx context.Context, site model.Site, version int, user *model.User) (model.Site, error) {
	tx, err := s.beginRollback(ctx, site.ID)
	if tx != nil {
		defer tx.Rollback()
	}
	if err != nil {
		return site, err
	}
	if err := s.createGate(ctx, tx, user, site.Name); err != nil {
		return site, err
	}
	return s.finishRollback(ctx, tx, site, version)
}

// UpdateInput contains a deploy's files and request facts.
type UpdateInput struct {
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
	Budget                   int64
	Limit                    int64
}

// Photo is an uploaded image to merge into the live site.
type Photo struct {
	Name string
	Data []byte
}

// UpdateResult describes the stored version and site.
type UpdateResult struct {
	Site          model.Site
	Version       int
	Published     bool
	Created       *CreateResult
	PhotoPaths    []string
	Reconciled    bool
	Limit         int64
	ResponseState any
}

// Update writes a new version and publishes it when requested.
func (s *Service) Update(ctx context.Context, in UpdateInput) (UpdateResult, error) {
	// Keep and photo updates must read and merge while owning both deploy locks.
	locked := in.Keep || in.Photos != nil
	if locked {
		unlockAccount := s.deps.LockAccount(in.User.ID)
		defer unlockAccount()
		unlockSite := s.deps.LockSite(in.User.ID, in.Name)
		defer unlockSite()
	}
	if in.Photos != nil {
		in.Limit, in.Budget = s.deps.PhotoLimits(ctx, in.User)
	}
	var paths []string
	if in.Keep || in.Photos != nil {
		files, digest, err := s.mergePatch(ctx, in)
		if err != nil {
			return UpdateResult{Limit: in.Limit}, err
		}
		in.Files, in.ArchiveSHA = files, digest
	}
	if in.Photos != nil {
		var err error
		paths, err = mergePhotos(ctx, in.Files, in.Photos, in.Reconcile)
		if err != nil {
			return UpdateResult{}, err
		}
		if in.Reconcile {
			site, err := repo.GetSiteByUser(ctx, s.database, in.User.ID, in.Name)
			if err != nil {
				return UpdateResult{}, &Error{Code: "photo_read_failed"}
			}
			return UpdateResult{Site: site, PhotoPaths: paths, Reconciled: true, ResponseState: s.deps.ResponseState(site, 0, false, true)}, nil
		}
		in.Files, in.ArchiveSHA, err = s.sanitizeFiles(in.Files, in.Budget)
		if err != nil {
			return UpdateResult{Limit: in.Limit}, err
		}
	}
	if in.CreateIfMissing {
		create := CreateInput{User: in.User, Name: in.Name, Files: in.Files, ArchiveSHA: in.ArchiveSHA, Author: in.Author, OrUpdate: true}
		var created CreateResult
		var err error
		if locked {
			created, err = s.createLocked(ctx, create)
		} else {
			created, err = s.Create(ctx, create)
		}
		if err != nil || !created.Exists {
			return UpdateResult{Created: &created}, err
		}
	}
	if !locked {
		unlockAccount := s.deps.LockAccount(in.User.ID)
		defer unlockAccount()
		unlockSite := s.deps.LockSite(in.User.ID, in.Name)
		defer unlockSite()
	}
	site, err := repo.GetSiteByUser(ctx, s.database, in.User.ID, in.Name)
	if errors.Is(err, sql.ErrNoRows) {
		return UpdateResult{}, &Error{Code: "deploy_site_not_found"}
	}
	if err != nil {
		return UpdateResult{}, err
	}
	if site.Suspended() {
		return UpdateResult{}, &Error{Code: "site_suspended", Message: site.SuspendedReason()}
	}
	tx, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return UpdateResult{}, err
	}
	defer tx.Rollback()
	if s.deps.FileCaps {
		if err := repo.LockUserForFileCaps(ctx, tx, in.User.ID); err != nil {
			return UpdateResult{}, err
		}
	}
	if err := repo.LockSiteForUpdate(ctx, tx, site.ID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return UpdateResult{}, &Error{Code: "rollback_site_not_found"}
		}
		return UpdateResult{}, err
	}
	liveBefore, err := repo.SiteActiveVersion(ctx, tx, site.ID)
	if err != nil {
		return UpdateResult{}, err
	}
	if err := s.createGate(ctx, tx, in.User, in.Name); err != nil {
		return UpdateResult{}, err
	}
	max, err := repo.GetMaxVersionNumber(ctx, tx, site.ID)
	if err != nil {
		return UpdateResult{}, err
	}
	n := max + 1
	if err := s.checkFileCaps(ctx, in.User, site, in.Files, n, in.Publish); err != nil {
		return UpdateResult{}, err
	}
	path := fmt.Sprintf("by-id/%s/%s/v%d/", site.UserID, in.Name, n)
	version, err := repo.CreateVersion(ctx, tx, site.ID, n, path, in.ArchiveSHA, in.Author)
	if err != nil {
		return UpdateResult{}, err
	}
	if err := repo.UpdateSiteSummary(ctx, tx, site.ID, s.deps.Summarize(in.Files)); err != nil {
		return UpdateResult{}, err
	}
	if err := s.disk.WriteFiles(ctx, site.UserID, in.Name, n, in.Files); err != nil {
		return UpdateResult{}, err
	}
	if !in.Publish {
		if err := repo.MarkVersionReady(ctx, tx, version.ID); err != nil {
			return UpdateResult{}, err
		}
		if err := tx.Commit(); err != nil {
			return UpdateResult{}, err
		}
		s.PruneVersions(ctx, PruneInput{site.ID, site.UserID, in.Name, site.ActiveVersion, site.KeepVersions})
		s.deps.NotifyUpdate(ctx, in.User, site, n, liveBefore, false, in.Files)
		return UpdateResult{Site: site, Version: n, ResponseState: s.deps.ResponseState(site, n, true, true)}, nil
	}
	if err := repo.ActivateVersion(ctx, tx, version.ID); err != nil {
		return UpdateResult{}, err
	}
	if err := repo.UpdateSiteActiveVersion(ctx, tx, site.ID, n); err != nil {
		return UpdateResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return UpdateResult{}, err
	}
	if err := s.disk.UpdateCurrent(site.UserID, in.Name, n); err != nil {
		log.Printf("updateSite: promote %s v%d after commit: %v", in.Name, n, err)
		return UpdateResult{}, err
	}
	if in.User.Handle.Valid && in.User.Handle.String != "" {
		if err := s.disk.EnsureHandleLink(in.User.Handle.String, in.User.ID); err != nil {
			log.Printf("updateSite: ensure handle link %s: %v", in.User.Handle.String, err)
		}
		s.deps.RequestCert(in.User.Handle.String)
	}
	s.PruneVersions(ctx, PruneInput{site.ID, site.UserID, in.Name, n, site.KeepVersions})
	s.deps.NotifyUpdate(ctx, in.User, site, n, liveBefore, true, in.Files)
	site.ActiveVersion = n
	return UpdateResult{Site: site, Version: n, Published: true, PhotoPaths: paths, ResponseState: s.deps.ResponseState(site, 0, false, true)}, nil
}

// mergePatch reads the live files while the update owns the deploy locks.
func (s *Service) mergePatch(ctx context.Context, in UpdateInput) (map[string][]byte, string, error) {
	files := in.Files
	totalLimit, fileLimit, entryLimit := tarball.FileLimits(in.Budget, s.deps.MaxFilesPerSite)
	removed, err := s.deps.NormalizePatch(files, in.Remove)
	if err == nil {
		var site model.Site
		site, err = repo.GetSiteByUser(ctx, s.database, in.User.ID, in.Name)
		if err == nil && in.CheckID && site.ID != in.ExpectedID {
			site, err = model.Site{}, sql.ErrNoRows
		}
		if errors.Is(err, sql.ErrNoRows) {
			err = nil
		} else if err == nil && site.ActiveVersion >= 1 {
			dir := s.disk.VersionDir(site.UserID, site.Name, site.ActiveVersion)
			var total int64
			for _, b := range files {
				total += int64(len(b))
			}
			err = filepath.WalkDir(dir, func(name string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if d.IsDir() {
					return nil
				}
				if !d.Type().IsRegular() {
					return errors.New("live version contains a non-regular file")
				}
				rel, err := filepath.Rel(dir, name)
				if err != nil {
					return err
				}
				rel = filepath.ToSlash(rel)
				if removed[rel] {
					return nil
				}
				if _, ok := files[rel]; ok {
					return nil
				}
				info, err := d.Info()
				if err != nil {
					return err
				}
				total += info.Size()
				if total > totalLimit || info.Size() > fileLimit || len(files)+1 > entryLimit {
					return fmt.Errorf("%w: merged files exceed the site limit", tarball.ErrTooLarge)
				}
				body, err := os.ReadFile(name)
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
		return nil, "", &Error{Code: code, Message: err.Error()}
	}
	return s.sanitizeFiles(files, in.Budget)
}

func (s *Service) sanitizeFiles(files map[string][]byte, budget int64) (map[string][]byte, string, error) {
	clean, err := tarball.SanitizeFilesWithLimit(files, budget, s.deps.MaxFilesPerSite)
	if err != nil {
		code := "patch_invalid"
		if errors.Is(err, tarball.ErrTooLarge) {
			code = "patch_too_large"
		}
		return nil, "", &Error{Code: code, Message: err.Error()}
	}
	if err := tarball.ValidateExtensions(clean); err != nil {
		return nil, "", &Error{Code: "patch_extension", Message: err.Error()}
	}
	names := make([]string, 0, len(clean))
	for name := range clean {
		names = append(names, name)
	}
	sort.Strings(names)
	sum := sha256.New()
	for _, name := range names {
		sum.Write([]byte(name))
		sum.Write([]byte{0})
		sum.Write(clean[name])
		sum.Write([]byte{0})
	}
	return clean, hex.EncodeToString(sum.Sum(nil)), nil
}

// RollbackInput identifies the site and version to activate.
type RollbackInput struct {
	User             *model.User
	Name, ExpectedID string
	CheckID          bool
	Version          int
}

// RollbackResult reports the site and whether it was already active.
type RollbackResult struct {
	Site          model.Site
	Unchanged     bool
	ResponseState any
}

// Rollback activates an eligible version under the site lock.
func (s *Service) Rollback(ctx context.Context, in RollbackInput) (RollbackResult, error) {
	site, unlock, unchanged, err := s.prepareRollback(ctx, in.User.ID, in.Name, in.ExpectedID, in.CheckID, in.Version)
	defer unlock()
	if err != nil {
		return RollbackResult{Site: site}, err
	}
	if !unchanged {
		site, err = s.rollbackTransaction(ctx, site, in.Version, in.User)
	}
	if err != nil {
		return RollbackResult{Site: site}, err
	}
	return RollbackResult{Site: site, Unchanged: unchanged, ResponseState: s.deps.ResponseState(site, 0, false, true)}, nil
}

func authorizePersonalSite(user *model.User, enabled bool) error {
	if !enabled && !user.IsAdmin && user.Team == nil && user.EventWebsite == nil {
		return &Error{Code: "no_personal_sites", Message: "team sites belong to an event: select your team in the Simple Hack connection before publishing"}
	}
	return nil
}

// PreflightInput contains facts needed before reading a create upload.
type PreflightInput struct {
	User          *model.User
	Name          string
	CheckExisting bool
	ReservedError string
	Publish       string
}

// CreatePreflight checks whether a new site may be uploaded.
func (s *Service) CreatePreflight(ctx context.Context, in PreflightInput) error {
	user := in.User
	if in.CheckExisting {
		_, err := repo.GetSiteByUser(ctx, s.database, user.ID, in.Name)
		if err == nil {
			return &Error{Code: "preflight_exists"}
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	if in.ReservedError != "" {
		return &Error{Code: "name_reserved", Message: in.ReservedError}
	}
	switch strings.ToLower(strings.TrimSpace(in.Publish)) {
	case "", "true", "1", "yes":
	case "false", "0", "no":
		return &Error{Code: "invalid_request", Message: "publish=false is for a new version of an existing site; a new site's first version always goes live"}
	default:
		return &Error{Code: "invalid_request", Message: "publish must be true or false"}
	}
	if err := authorizePersonalSite(user, s.deps.PersonalSitesEnabled); err != nil {
		return err
	}
	if user.IsAdmin || user.Team != nil || user.EventWebsite != nil {
		return nil
	}
	existing, err := repo.CountSitesByUser(ctx, s.database, user.ID)
	if err != nil {
		return err
	}
	max := s.deps.MaxSites(ctx, user)
	if existing >= max {
		return &Error{Code: "site_quota_reached", Message: fmt.Sprintf("site quota reached: this account holds at most %d sites (sites in Recently deleted count until they are removed)", max)}
	}
	return nil
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

func mergePhotos(ctx context.Context, files map[string][]byte, photos []Photo, reconcile bool) ([]string, error) {
	paths := make([]string, 0, len(photos))
	if reconcile {
		names := make([]string, 0, len(files))
		for name := range files {
			names = append(names, name)
		}
		sort.Strings(names)
		used := map[string]bool{}
		for _, p := range photos {
			found := "images/" + p.Name
			if used[found] || !bytes.Equal(files[found], p.Data) {
				found = ""
				for _, name := range names {
					if !used[name] && matchesPhotoName(name, p.Name) && bytes.Equal(files[name], p.Data) {
						found = name
						break
					}
				}
			}
			if found == "" {
				return nil, &Error{Code: "photo_save_uncertain", Message: "Could not confirm this batch. Check the site before adding these photos again."}
			}
			used[found] = true
			paths = append(paths, found)
		}
		return paths, nil
	}
	occupied, err := photoOccupiedPaths(ctx, files)
	if err != nil {
		return nil, &Error{Code: "photo_allocation_cancelled", Message: err.Error()}
	}
	for _, p := range photos {
		path, err := nextPhotoPath(ctx, occupied, p.Name)
		if err != nil {
			if ctx.Err() != nil {
				return nil, &Error{Code: "photo_allocation_cancelled", Message: err.Error()}
			}
			return nil, &Error{Code: "photo_conflict", Message: err.Error()}
		}
		occupied[path] = false
		files[path] = p.Data
		paths = append(paths, path)
	}
	return paths, nil
}
func matchesPhotoName(path, name string) bool {
	if path == "images/"+name {
		return true
	}
	ext := name[strings.LastIndexByte(name, '.'):]
	if !strings.HasPrefix(path, "images/") || !strings.HasSuffix(path, ext) {
		return false
	}
	stem := strings.TrimSuffix(strings.TrimPrefix(path, "images/"), ext)
	at := strings.LastIndexByte(stem, '-')
	if at < 0 {
		return false
	}
	n, err := strconv.Atoi(stem[at+1:])
	if err != nil || n < 2 || strconv.Itoa(n) != stem[at+1:] {
		return false
	}
	base := strings.TrimSuffix(name, ext)
	if max := 60 - len(stem[at:]); len(base) > max {
		base = base[:max]
	}
	return stem[:at] == base
}

// The value distinguishes directories from files. Build prefixes once, then
// each suffix probe is one map lookup, even in a large, collision-heavy tree.
func photoOccupiedPaths(ctx context.Context, files map[string][]byte) (map[string]bool, error) {
	occupied := make(map[string]bool, len(files))
	for path := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		occupied[path] = false
		for i := strings.IndexByte(path, '/'); i >= 0; {
			occupied[path[:i]] = true
			next := strings.IndexByte(path[i+1:], '/')
			if next < 0 {
				break
			}
			i += next + 1
		}
	}
	return occupied, nil
}

func nextPhotoPath(ctx context.Context, occupied map[string]bool, name string) (string, error) {
	if dir, exists := occupied["images"]; exists && !dir {
		return "", errors.New("The images path is a file. Rename it before adding photos.")
	}
	ext := name[strings.LastIndexByte(name, '.'):]
	stem := strings.TrimSuffix(name, ext)
	path := "images/" + name
	for n := 2; ; n++ {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		dir, taken := occupied[path]
		if !taken {
			return path, nil
		}
		if dir {
			return "", errors.New("A folder uses this photo name. Choose another name.")
		}
		suffix := fmt.Sprintf("-%d", n)
		base := stem
		if len(base)+len(suffix) > 60 {
			base = base[:60-len(suffix)]
		}
		path = "images/" + base + suffix + ext
	}
}
