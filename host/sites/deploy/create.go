package deploy

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
	accountapp "github.com/vsriram/simple-host/host/access"
	accounts "github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/sites/repo"
)

// createMarkerSettle is how long a private create waits between writing its
// access marker and making its files live.
var createMarkerSettle = time.Second

// CreateInput contains the values and non-HTTP dependencies for a locked create.
type CreateInput struct {
	Access                   string
	Viewers                  []string
	User                     *model.User
	Name, ArchiveSHA, Author string
	Files                    map[string][]byte
	OrUpdate                 bool
}

// CreateResult describes a created site or an existing site selected for update.
type CreateResult struct {
	Site      model.Site
	Exists    bool
	FileBytes int64
}

// Create creates and promotes a site while holding the account and site locks.
func (s Service) Create(ctx context.Context, in CreateInput) (CreateResult, error) {
	if in.User == nil {
		return CreateResult{}, &sites.Error{Code: "unauthorized"}
	}
	unlockAccount := s.LockAccount(in.User.ID)
	defer unlockAccount()
	unlockSite := s.LockSite(in.User.ID, in.Name)
	defer unlockSite()
	return s.createLocked(ctx, in)
}

// createLocked runs creation while the account and site locks are held.
func (s Service) createLocked(ctx context.Context, in CreateInput) (CreateResult, error) {
	if err := authorizePersonalSite(in.User, s.Deps.PersonalSitesEnabled); err != nil {
		return CreateResult{}, err
	}
	// A first deploy assigns a handle before the transaction builds the URL.
	if !in.User.Handle.Valid || in.User.Handle.String == "" {
		accountapp.AssignHandle(ctx, s.DB, in.User.ID, sites.HandleSeed(in.User, s.Deps.SiteDomain), s.Deps.RandomHandles)
		if refetched, err := accounts.GetUserByUsername(ctx, s.DB, in.User.Username); err == nil {
			*in.User = refetched
		}
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return CreateResult{}, err
	}
	defer tx.Rollback()
	if err := s.CreateGate(ctx, tx, in.User, in.Name); err != nil {
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
	siteURL := fmt.Sprintf("https://%s.%s", in.Name, s.Deps.SiteDomain)
	if in.User.Handle.Valid && in.User.Handle.String != "" {
		siteURL = fmt.Sprintf("https://%s/%s/%s/", s.Deps.ContentHost, in.User.Handle.String, in.Name)
	}
	site, err := repo.CreateSite(ctx, tx, in.User.ID, in.Name, siteURL, s.previewExpiry(in.User.Username))
	var pqErr *pq.Error
	if errors.As(err, &pqErr) && pqErr.Code == "23505" {
		tx.Rollback()
		msg, held := s.DeletedNameConflict(ctx, in.User.ID, in.Name)
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
func (s Service) checkNamedViewers(in CreateInput) error {
	if in.Access != "specific" {
		return nil
	}
	if _, err := accountapp.ViewerEmails(in.Viewers, s.Deps.SiteViewersMax); err != nil {
		var refusal *accountapp.Refusal
		if errors.As(err, &refusal) {
			return &sites.Error{Code: refusal.Code, Message: refusal.Message}
		}
		return err
	}
	var refusal *accountapp.Refusal
	if errors.As(s.NamedViewersRefusal(in.User.Handle.String, in.Name), &refusal) {
		return &sites.Error{Code: refusal.Code, Message: refusal.Message}
	}
	return nil
}

// commitFirstVersion writes version 1 of a new site row and commits tx; the
// returned site is the one to report, also on error.
func (s Service) commitFirstVersion(ctx context.Context, tx *sql.Tx, in CreateInput, site model.Site) (model.Site, error) {
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
			_ = s.Disk.SetPasscodeMarker(site.UserID, site.Name, false)
		}
	}()
	if err := repo.UpdateSiteSummary(ctx, tx, site.ID, sites.ExtractSiteSummary(in.Files)); err != nil {
		return site, err
	}
	// A renamed site's old name is free for a new site, which wins: links to
	// the name stop following the renamed one.
	if err := repo.DropOldSiteName(ctx, tx, site.UserID, site.Name); err != nil {
		return site, err
	}
	if err := s.CheckFileCaps(ctx, in.User, site, in.Files, 1, true); err != nil {
		return site, err
	}
	if err := s.ValidateRedirects(in.Files); err != nil {
		return site, err
	}
	const versionNumber = 1
	diskPath := fmt.Sprintf("by-id/%s/%s/v%d/", site.UserID, site.Name, versionNumber)
	version, err := repo.CreateVersion(ctx, tx, site.ID, versionNumber, diskPath, in.ArchiveSHA, in.Author)
	if err != nil {
		return site, err
	}
	// Write the new version dir (not yet live) before committing.
	if err := s.Disk.WriteFiles(ctx, site.UserID, site.Name, versionNumber, in.Files); err != nil {
		return site, err
	}
	if in.Access == "specific" {
		markerAttempted = true
		if err := s.Disk.SetPasscodeMarker(site.UserID, site.Name, true); err != nil || !s.Disk.HasPasscodeMarker(site.UserID, site.Name) {
			return site, &sites.Error{Code: "access_marker", Message: "the site's files could not be marked; nothing changed, try again"}
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
func (s Service) promoteCreated(ctx context.Context, in CreateInput, site model.Site) (CreateResult, error) {
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
	if err := s.Disk.UpdateCurrent(site.UserID, site.Name, 1); err != nil {
		log.Printf("createSite: promote %s v%d after commit: %v", site.Name, 1, err)
		return CreateResult{Site: site}, err
	}
	// Non-fatal: the legacy subdomain still works if the handle link fails.
	if handle != "" {
		if err := s.Disk.EnsureHandleLink(handle, site.UserID); err != nil {
			log.Printf("createSite: ensure handle link %s: %v", handle, err)
		}
		s.RequestCert(handle)
	}
	site.ActiveVersion = 1
	site.OwnerHandle = handle
	s.SyncFamilyRedirect(ctx, site.ID)
	if s.Deps.DeployEnabled {
		queueFile := s.Disk.DataDir() + "/.deploy-queue"
		if err := appendToFile(queueFile, site.Name); err != nil {
			log.Printf("failed to queue deploy for %s: %v", site.Name, err)
		}
	}
	return CreateResult{Site: site, FileBytes: s.FileBytes(site)}, nil
}

// createConflict resolves a duplicate after the create transaction is rolled back.
func createConflict(in CreateInput, site model.Site, msg string, held bool) (CreateResult, error) {
	if held {
		return CreateResult{Site: site}, &sites.Error{Code: "recently_deleted", Message: msg}
	}
	if in.OrUpdate {
		return CreateResult{Site: site, Exists: true}, nil
	}
	return CreateResult{Site: site}, &sites.Error{Code: "site_exists", Message: "site already exists: use PUT to update it (PUT /v1/sites/" + in.Name + " or PUT /v1/sites/" + in.Name + "/files); add ?create=1 to a PUT to create or update in one call"}
}

func authorizePersonalSite(user *model.User, enabled bool) error {
	if !enabled && !user.IsAdmin && user.Member == nil {
		return &sites.Error{Code: "no_personal_sites", Message: "sites here belong to an event: name the event as owner (owner=<event>) to publish your team's site"}
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
