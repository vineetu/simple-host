package sites

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lib/pq"
	accountapp "github.com/vsriram/simple-host/host/access"
	accounts "github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/model"
	rules "github.com/vsriram/simple-host/host/rules/sites"
	"github.com/vsriram/simple-host/host/sites/eventdns"
	"github.com/vsriram/simple-host/host/sites/pagepath"
	"github.com/vsriram/simple-host/host/sites/redirects"
	"github.com/vsriram/simple-host/host/sites/repo"
	"github.com/vsriram/simple-host/host/storage"
	"github.com/vsriram/simple-host/host/tarball"
)

// Service owns site lifecycle operations.
type Service struct {
	accounts   *accountapp.Service
	eventSweep *eventDNSState

	database        *sql.DB
	disk            Disk
	previewAccounts map[string]bool
	previewTTL      time.Duration
	deps            Deps
}

// Deps holds collaborators shared by site workflows.
type Deps struct {
	Sitemap                 SitemapFilter
	SiteViewersMax          int
	PlatformDomains         []string
	ContentHost, SiteDomain string
	PersonalSitesEnabled    bool
	CanSetRetention         func(context.Context, *model.User) bool
	DefaultRetention        int
	LockSite                func(string, string) func()
	MaxSites                func(context.Context, *model.User) int
	SiteURL                 func(model.Site) string
	RenameURL               func(string, string) string
	LockAccount             func(string) func()
	AccountRetention        func(context.Context, string, int) (int, error)
	RetainedFloor           func([]model.Version, int, int, int) int
	PinnedFloor             func(context.Context, string, string, int) int
	GroupGate               GroupGate
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
	RequestCert             func(string)
	HasLiveFamilies         func() bool
	SyncRedirect            func(context.Context, string)
	DeployEnabled           bool
	RedirectsMaxRules       int
	MaxFilesPerSite         int
	PhotoLimits             func(context.Context, *model.User) (int64, bool)
	ResponseState           func(model.Site, int, bool, bool) any
	WebsiteArchiveDefault   int64
	WebsiteLimit            func(context.Context, *model.User) (int64, bool)
	EventDNS                eventdns.Provider
	EventProbe              *eventdns.LiveProbe
	EventNamePeer           func(context.Context, string, string) (bool, error)
	EventDomains            []string
	EventTTL                time.Duration
	EventMaxClaims          int
	DeletedRetention        time.Duration
	CancelDomainCert        func(string)
	ExportResources         func(context.Context, string) ([]map[string]any, error)
	AdmitSQLite             func(context.Context, string, func(context.Context) error) error
	ExportSQLiteMaxMemory   int64
	DeletedTransaction      func(*sql.Tx) error
	IdleArchive             IdleArchiveDeps
	OwnerMarks              OwnerMarkDeps
	DomainCerts             DomainCertDeps
	// DeleteViewerPush deletes a removed named viewer's web-push
	// subscriptions for one site: set by the handler,
	// the only side that can resolve a push device's owner_id back to a
	// sign-in email the same way visitorEmail does. Optional;
	// RemoveSiteViewer is a no-op here (and logs nothing) when it is nil.
	DeleteViewerPush func(ctx context.Context, siteID, email string) error
}

// createMarkerSettle is how long a private create waits between writing its
// access marker and making its files live.
var createMarkerSettle = time.Second

// CreateInput contains the values and non-HTTP dependencies for a locked create.
type CreateInput struct {
	Access                   string
	Viewers                  []string
	NamedViewersRefusal      func(handle, name string) (code, msg string)
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
	handle, err := accounts.LockProfile(ctx, tx, in.User.ID)
	if err != nil {
		return CreateResult{}, err
	}
	in.User.Handle = handle
	if err := s.checkNamedViewers(in); err != nil {
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
		msg, held := s.deps.Conflict(ctx, in.User.ID, in.Name)
		return createConflict(in, site, msg, held)
	}
	if err != nil {
		return CreateResult{Site: site}, err
	}
	if site, err = s.commitFirstVersion(ctx, tx, in, site); err != nil {
		return CreateResult{Site: site}, err
	}
	return s.promoteCreated(ctx, in, site)
}

// checkNamedViewers refuses a specific-access create whose viewer list is not allowed.
func (s *Service) checkNamedViewers(in CreateInput) error {
	if in.Access != "specific" {
		return nil
	}
	if len(in.Viewers) > s.deps.SiteViewersMax {
		return &Error{Code: "too_many_viewers", Message: fmt.Sprintf("a site can name at most %d viewers", s.deps.SiteViewersMax)}
	}
	if in.NamedViewersRefusal != nil {
		if code, msg := in.NamedViewersRefusal(in.User.Handle.String, in.Name); code != "" {
			return &Error{Code: code, Message: msg}
		}
	}
	return nil
}

// commitFirstVersion writes version 1 of a new site row and commits tx; the
// returned site is the one to report, also on error.
func (s *Service) commitFirstVersion(ctx context.Context, tx *sql.Tx, in CreateInput, site model.Site) (model.Site, error) {
	if in.Access == "specific" {
		if err := repo.SetSiteAccess(ctx, tx, site.ID, "specific"); err != nil {
			return site, err
		}
		for _, email := range in.Viewers {
			if _, err := accounts.AddSiteViewer(ctx, tx, site.ID, email); err != nil {
				return site, err
			}
		}
		site.NamedViewers = true
	}
	// The marker comes off again only when the transaction surely rolled
	// back: once Commit has been called its outcome can be unknown (a lost
	// acknowledgement), and the Go gate reads the marker first, so a
	// committed private site must never lose it. A marker left on a site
	// whose row never committed is harmless: the gate finds no protected
	// row and serves nothing extra (fail closed, never open).
	commitTried := false
	markerAttempted := false
	defer func() {
		if markerAttempted && !commitTried {
			_ = s.disk.SetPasscodeMarker(site.UserID, site.Name, false)
		}
	}()
	if err := repo.UpdateSiteSummary(ctx, tx, site.ID, s.deps.Summarize(in.Files)); err != nil {
		return site, err
	}
	// A renamed site's old name is free for a new site, which wins: links to
	// the name stop following the renamed one.
	if err := repo.DropOldSiteName(ctx, tx, site.UserID, site.Name); err != nil {
		return site, err
	}
	if err := s.checkFileCaps(ctx, in.User, site, in.Files, 1, true); err != nil {
		return site, err
	}
	if err := s.validateRedirects(in.Files); err != nil {
		return site, err
	}
	const versionNumber = 1
	diskPath := fmt.Sprintf("by-id/%s/%s/v%d/", site.UserID, site.Name, versionNumber)
	version, err := repo.CreateVersion(ctx, tx, site.ID, versionNumber, diskPath, in.ArchiveSHA, in.Author)
	if err != nil {
		return site, err
	}
	// Write the new version dir (not yet live) before committing.
	if err := s.disk.WriteFiles(ctx, site.UserID, site.Name, versionNumber, in.Files); err != nil {
		return site, err
	}
	if in.Access == "specific" {
		markerAttempted = true
		if err := s.disk.SetPasscodeMarker(site.UserID, site.Name, true); err != nil || !s.disk.HasPasscodeMarker(site.UserID, site.Name) {
			return site, &Error{Code: "access_marker", Message: "the site's files could not be marked; nothing changed, try again"}
		}
	}
	if err := repo.ActivateVersion(ctx, tx, version.ID); err != nil {
		return site, err
	}
	if err := repo.UpdateSiteActiveVersion(ctx, tx, site.ID, versionNumber); err != nil {
		return site, err
	}
	commitTried = true
	return site, tx.Commit()
}

// promoteCreated makes a committed new site live and reports it.
func (s *Service) promoteCreated(ctx context.Context, in CreateInput, site model.Site) (CreateResult, error) {
	var handle string
	if in.User.Handle.Valid {
		handle = in.User.Handle.String
	}
	if site.NamedViewers && createMarkerSettle > 0 {
		// A request that checked for the marker just before it existed
		// (nginx, Caddy and the Go gate all check it, then open the file)
		// must have finished before `current` exists: this pause keeps that
		// gap far wider than any single request's check-then-open. The Go
		// site and person hosts need no pause (they find the row, committed
		// after the marker, before they look for the marker); this is for
		// servers that read the disk directly. The site is committed, so the
		// pause does not hold a transaction open.
		time.Sleep(createMarkerSettle)
	}
	if site.NamedViewers {
		log.Printf("site_access access=specific site_id=%s name=%s by=%s", site.ID, site.Name, in.User.ID)
		log.Printf("site_viewers added=%d site_id=%s name=%s by=%s", len(in.Viewers), site.ID, site.Name, in.User.ID)
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
func New(database *sql.DB, disk Disk, previewAccounts map[string]bool, previewTTL time.Duration, deps Deps) *Service {
	return &Service{accounts: accountapp.New(database), eventSweep: &eventDNSState{}, database: database, disk: disk, previewAccounts: previewAccounts, previewTTL: previewTTL, deps: deps}
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
		if err := repo.DeleteExpiredSite(ctx, s.database, site.ID); errors.Is(err, sql.ErrNoRows) {
			unlock()
			continue
		} else if err != nil {
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
	if err := rules.ValidateShape(newName); err != nil {
		return result, &Error{Code: "invalid_name", Message: err.Error()}
	}
	if err := rules.ValidateReserved(newName); err != nil {
		return result, &Error{Code: "name_reserved", Message: err.Error()}
	}
	if oldName == newName {
		return result, &Error{Code: "unchanged_name", Message: "new site name must be different"}
	}
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
	if site.Gated() {
		if err := s.disk.SetPasscodeMarker(userID, oldName, true); err != nil || !s.disk.HasPasscodeMarker(userID, oldName) {
			return result, &Error{Code: "access_marker", Message: "the site's files could not be marked; nothing changed, try again"}
		}
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

// activateVersion updates the transaction and serving files, compensating if commit fails.
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
	if err := s.ensureAccessMarker(site); err != nil {
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

// prepareRollback locks the site and checks the requested version before the transaction.
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
	versions, err := repo.ListVersionsBySite(ctx, s.database, site.ID)
	if err != nil {
		return site, unlock, false, err
	}
	for _, v := range versions {
		// A version whose upload never finished is not one to go live.
		if v.VersionNumber == version && v.Status != "uploading" {
			return site, unlock, false, nil
		}
	}
	return site, unlock, false, &Error{Code: "version_not_found", Message: "version not found"}
}

// rollbackTransaction locks the site row, runs the transaction-time gate and
// activates the version, announcing the change when it moved.
func (s *Service) rollbackTransaction(ctx context.Context, site model.Site, version int, user *model.User) (model.Site, error) {
	tx, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return site, err
	}
	defer tx.Rollback()
	if err := repo.LockSiteForUpdate(ctx, tx, site.ID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return site, &Error{Code: "site_not_found", Message: "site not found"}
		}
		return site, err
	}
	if err := s.createGate(ctx, tx, user, site.Name); err != nil {
		return site, err
	}
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

// UpdateInput contains a deploy's files and request facts.
type UpdateInput struct {
	Access                   string
	Viewers                  []string
	NamedViewersRefusal      func(handle, name string) (code, msg string)
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
	if in.IfMatch > 0 {
		in.CreateIfMissing = false // If-Match names an existing version
	}
	if in.LimitOverride {
		in.Budget = in.Limit
	}
	// Keep and photo updates must read and merge while owning both deploy locks.
	locked := in.Keep || in.Photos != nil
	if locked {
		unlockAccount := s.deps.LockAccount(in.User.ID)
		defer unlockAccount()
		unlockSite := s.deps.LockSite(in.User.ID, in.Name)
		defer unlockSite()
	}
	var paths []string
	if locked {
		var stop UpdateResult
		var err error
		paths, stop, err = s.mergeUpdateFiles(ctx, &in)
		if err != nil || stop.Reconciled {
			return stop, err
		}
	}
	if in.CreateIfMissing {
		create := CreateInput{Access: in.Access, Viewers: in.Viewers, NamedViewersRefusal: in.NamedViewersRefusal, User: in.User, Name: in.Name, Files: in.Files, ArchiveSHA: in.ArchiveSHA, Author: in.Author, OrUpdate: true}
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
	return s.updateExisting(ctx, in, paths)
}

// mergeUpdateFiles merges the live files and photos into in.Files under the
// deploy locks. A non-nil error or a Reconciled result is the update's answer.
func (s *Service) mergeUpdateFiles(ctx context.Context, in *UpdateInput) ([]string, UpdateResult, error) {
	if in.Photos != nil {
		var override bool
		in.Limit, override = s.deps.PhotoLimits(ctx, in.User)
		in.Budget = ExtractBudget(in.Limit, override)
	}
	files, digest, err := s.mergePatch(ctx, *in)
	if err != nil {
		return nil, UpdateResult{Limit: in.Limit}, err
	}
	in.Files, in.ArchiveSHA = files, digest
	if in.Photos == nil {
		return nil, UpdateResult{}, nil
	}
	paths, err := mergePhotos(ctx, in.Files, in.Photos, in.Reconcile)
	if err != nil {
		return nil, UpdateResult{}, err
	}
	if in.Reconcile {
		site, err := repo.GetSiteByUser(ctx, s.database, in.User.ID, in.Name)
		if err != nil {
			return nil, UpdateResult{}, &Error{Code: "photo_read_failed"}
		}
		return paths, UpdateResult{Site: site, PhotoPaths: paths, Reconciled: true, ResponseState: s.deps.ResponseState(site, 0, false, true)}, nil
	}
	in.Files, in.ArchiveSHA, err = s.PrepareFiles(FilePreparationInput{Files: in.Files, Limit: in.Budget, Override: true})
	if err != nil {
		return nil, UpdateResult{Limit: in.Limit}, err
	}
	return paths, UpdateResult{}, nil
}

// updateExisting stores the next version of an existing site while the
// deploy locks are held, then promotes it when in.Publish is set.
func (s *Service) updateExisting(ctx context.Context, in UpdateInput, paths []string) (UpdateResult, error) {
	site, err := repo.GetSiteByUser(ctx, s.database, in.User.ID, in.Name)
	if errors.Is(err, sql.ErrNoRows) {
		return UpdateResult{}, &Error{Code: "deploy_site_not_found"}
	}
	if err != nil {
		return UpdateResult{}, err
	}
	if in.Access == "specific" && !site.NamedViewers {
		return UpdateResult{}, &Error{Code: "access_differs", Message: "the existing site is not specific; use set_site_access / grant_site_viewer before updating"}
	}
	if site.Suspended() {
		return UpdateResult{}, &Error{Code: "site_suspended", Message: site.SuspendedReason()}
	}
	n, liveBefore, err := s.commitNextVersion(ctx, in, site)
	if err != nil {
		return UpdateResult{}, err
	}
	if !in.Publish {
		s.PruneVersions(ctx, PruneInput{site.ID, site.UserID, in.Name, site.ActiveVersion, site.KeepVersions})
		s.deps.NotifyUpdate(ctx, in.User, site, n, liveBefore, false, in.Files)
		return UpdateResult{Site: site, Version: n, ResponseState: s.deps.ResponseState(site, n, true, true)}, nil
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

// lockForUpdate takes the account's file-caps row lock (when caps are on)
// and the site row lock inside tx.
func (s *Service) lockForUpdate(ctx context.Context, tx *sql.Tx, userID, siteID string) error {
	if s.deps.FileCaps {
		if _, err := accounts.LockProfile(ctx, tx, userID); err != nil {
			return err
		}
	}
	if err := repo.LockSiteForUpdate(ctx, tx, siteID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return &Error{Code: "site_not_found", Message: "site not found"}
		}
		return err
	}
	return nil
}

// commitNextVersion writes and commits the site's next version, activating it
// in the database when in.Publish is set. It returns the new version number
// and the version that was live before.
func (s *Service) commitNextVersion(ctx context.Context, in UpdateInput, site model.Site) (n, liveBefore int, err error) {
	tx, err := s.database.BeginTx(ctx, nil)
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
		return 0, 0, versionChanged(liveBefore)
	}
	if err := s.createGate(ctx, tx, in.User, in.Name); err != nil {
		return 0, 0, err
	}
	max, err := repo.GetMaxVersionNumber(ctx, tx, site.ID)
	if err != nil {
		return 0, 0, err
	}
	n = max + 1
	if err := s.checkFileCaps(ctx, in.User, site, in.Files, n, in.Publish); err != nil {
		return 0, 0, err
	}
	path := fmt.Sprintf("by-id/%s/%s/v%d/", site.UserID, in.Name, n)
	if err := s.validateRedirects(in.Files); err != nil {
		return 0, 0, err
	}
	version, err := repo.CreateVersion(ctx, tx, site.ID, n, path, in.ArchiveSHA, in.Author)
	if err != nil {
		return 0, 0, err
	}
	if err := repo.UpdateSiteSummary(ctx, tx, site.ID, s.deps.Summarize(in.Files)); err != nil {
		return 0, 0, err
	}
	if err := s.disk.WriteFiles(ctx, site.UserID, in.Name, n, in.Files); err != nil {
		return 0, 0, err
	}
	if err := s.ensureAccessMarker(site); err != nil {
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
func (s *Service) mergePatch(ctx context.Context, in UpdateInput) (map[string][]byte, string, error) {
	files := in.Files
	totalLimit, fileLimit, entryLimit := tarball.FileLimits(in.Budget, s.deps.MaxFilesPerSite)
	removed, err := normalizeFilePatch(files, in.Remove)
	if err == nil {
		var site model.Site
		site, err = repo.GetSiteByUser(ctx, s.database, in.User.ID, in.Name)
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
			err = s.disk.WalkFiles(ctx, storage.FileVersion{UserID: site.UserID, SiteName: site.Name, Version: site.ActiveVersion}, func(file storage.SiteFile, err error) error {
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
		return nil, "", &Error{Code: code, Message: err.Error()}
	}
	return s.PrepareFiles(FilePreparationInput{Files: files, Limit: in.Budget, Override: true})
}

// RollbackInput identifies the site and version to activate.
type RollbackInput struct {
	User             *model.User
	Name, ExpectedID string
	CheckID          bool
	Version          int
	// IfMatch, when not 0, is the active version the caller last read.
	IfMatch int
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
	if in.IfMatch > 0 && site.ActiveVersion != in.IfMatch {
		return RollbackResult{Site: site}, versionChanged(site.ActiveVersion)
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
	if !enabled && !user.IsAdmin && user.Member == nil {
		return &Error{Code: "no_personal_sites", Message: "sites here belong to an event: name the event as owner (owner=<event>) to publish your team's site"}
	}
	return nil
}

// PreflightInput contains facts needed before reading a create upload.
type PreflightInput struct {
	User          *model.User
	Name          string
	CheckExisting bool
	CheckName     bool
	Publish       string
}

// CreatePreflight checks whether a new site may be uploaded.
func (s *Service) CreatePreflight(ctx context.Context, in PreflightInput) error {
	user := in.User
	if in.CheckName {
		if err := rules.ValidateShape(in.Name); err != nil {
			return &Error{Code: "invalid_name", Message: err.Error()}
		}
	}
	if in.CheckExisting {
		_, err := repo.GetSiteByUser(ctx, s.database, user.ID, in.Name)
		if err == nil {
			return &Error{Code: "preflight_exists"}
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	if in.CheckName || in.CheckExisting {
		if err := rules.ValidateReserved(in.Name); err != nil {
			return &Error{Code: "name_reserved", Message: err.Error()}
		}
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
	// A product's accounts (a Simple Hack event) hold as many sites as the
	// product allows, decided by its write gate.
	if user.IsAdmin || (user.Member != nil && s.deps.GroupGate != nil) {
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

func (s *Service) validateRedirects(files map[string][]byte) error {
	if data, ok := files["_redirects"]; ok {
		max := s.deps.RedirectsMaxRules
		if max == 0 {
			max = 1000
		}
		tree := newPublishedTree(files)
		exists := func(p string) bool { return pagepath.Taken(tree, p) }
		if _, err := redirects.ParseWith(data, redirects.Options{MaxRules: max, Exists: exists}); err != nil {
			return &Error{Code: "redirects_invalid", Message: "_redirects: " + err.Error()}
		}
	}
	return nil
}

// publishedTree is the files being published, as the page-address rule reads
// a version tree: every file, and every folder that holds one.
type publishedTree struct {
	files map[string][]byte
	dirs  map[string]bool
}

func newPublishedTree(files map[string][]byte) publishedTree {
	dirs := map[string]bool{".": true}
	for name := range files {
		for d := path.Dir(name); !dirs[d] && d != "/"; d = path.Dir(d) {
			dirs[d] = true
		}
	}
	return publishedTree{files, dirs}
}

func (t publishedTree) Stat(name string) (fs.FileInfo, error) {
	if _, ok := t.files[name]; ok {
		return publishedInfo(0), nil
	}
	if t.dirs[name] {
		return publishedInfo(fs.ModeDir), nil
	}
	return nil, fs.ErrNotExist
}

type publishedInfo fs.FileMode

func (i publishedInfo) Name() string       { return "" }
func (i publishedInfo) Size() int64        { return 0 }
func (i publishedInfo) Mode() fs.FileMode  { return fs.FileMode(i) }
func (i publishedInfo) ModTime() time.Time { return time.Time{} }
func (i publishedInfo) IsDir() bool        { return fs.FileMode(i).IsDir() }
func (i publishedInfo) Sys() any           { return nil }

// versionChanged refuses a change whose If-Match names a version that is no
// longer the active one: someone else published or rolled back meanwhile.
func versionChanged(active int) error {
	return &Error{Code: "version_changed", Message: fmt.Sprintf("the site changed since you read it: its active version is now %d; read it again and redo your change on that version", active)}
}
