package lifecycle

import (
	"context"
	"database/sql"
	"errors"

	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	rules "github.com/vsriram/simple-host/host/rules/sites"

	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/sites/repo"
	"github.com/vsriram/simple-host/host/sqlkit"
)

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
func (s Service) Rename(ctx context.Context, in RenameInput) (RenameResult, error) {
	userID, oldName, newName, handle := in.UserID, in.OldName, in.NewName, in.Handle
	var result RenameResult
	if err := rules.ValidateShape(newName); err != nil {
		return result, &sites.Error{Code: "invalid_name", Message: err.Error()}
	}
	if err := rules.ValidateReserved(newName); err != nil {
		return result, &sites.Error{Code: "name_reserved", Message: err.Error()}
	}
	if err := s.Deps.Hosts.NameFits(newName, handle); err != nil {
		return result, err
	}
	if oldName == newName {
		return result, &sites.Error{Code: "unchanged_name", Message: "new site name must be different"}
	}
	if _, err := repo.GetSiteByUser(ctx, s.DB, userID, oldName); err != nil {
		return result, &sites.Error{Code: "site_not_found"}
	}
	first, second := oldName, newName
	if second < first {
		first, second = second, first
	}
	unlockFirst := s.LockSite(userID, first)
	defer unlockFirst()
	unlockSecond := s.LockSite(userID, second)
	defer unlockSecond()
	site, err := repo.GetSiteByUser(ctx, s.DB, userID, oldName)
	if err != nil {
		return result, &sites.Error{Code: "site_not_found"}
	}
	if site.Suspended() {
		return result, &sites.Error{Code: "site_suspended", Message: site.SuspendedReason()}
	}
	if _, err := repo.GetSiteByUser(ctx, s.DB, userID, newName); err == nil {
		return result, &sites.Error{Code: "site_exists"}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return result, err
	}
	if msg, held := s.DeletedNameConflict(ctx, userID, newName); held {
		return result, &sites.Error{Code: "recently_deleted", Message: msg}
	}
	result.Site = site
	result.OldURL = s.Deps.Hosts.Addressing().SiteURLFor(site)
	result.NewURL = s.RenameURL(handle, newName)
	if site.CustomDomain.Valid {
		result.Domain = site.CustomDomain.String
	}
	domains := []string{result.Domain}
	if info, ok, err := repo.GetSiteDomainInfo(ctx, s.DB, site.ID); err != nil {
		return result, err
	} else if ok && info.PreviousDomain != "" {
		domains = append(domains, info.PreviousDomain)
	}
	if site.Gated() {
		if err := s.Disk.SetPasscodeMarker(userID, oldName, true); err != nil || !s.Disk.HasPasscodeMarker(userID, oldName) {
			return result, &sites.Error{Code: "access_marker", Message: "the site's files could not be marked; nothing changed, try again"}
		}
	}
	if err := s.Disk.RenameSite(userID, oldName, newName, domains...); err != nil {
		return result, &sites.Error{Code: "move_failed"}
	}
	err = func() error {
		tx, err := s.DB.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		// Again under the account lock: the handle may have changed since.
		current, err := accountsrepo.LockProfile(ctx, tx, userID)
		if err != nil {
			return err
		}
		if err := s.Deps.Hosts.NameFits(newName, current.String); err != nil {
			return err
		}
		if err := repo.RenameSite(ctx, tx, site.ID, newName, result.NewURL); err != nil {
			return err
		}
		if err := repo.KeepOldSiteName(ctx, tx, userID, site.ID, oldName, newName); err != nil {
			return err
		}
		return tx.Commit()
	}()
	if err != nil {
		_ = s.Disk.RenameSite(userID, newName, oldName, domains...)
		if sqlkit.IsUniqueViolation(err) { // a site took the name meanwhile
			return result, &sites.Error{Code: "site_exists"}
		}
		return result, err
	}
	// The main address may change with the name (address families).
	s.SyncDomainRedirect(ctx, site.ID)
	return result, nil
}
