package sites

import (
	"context"
	"database/sql"
	"errors"

	"github.com/vsriram/simple-host/host/model"
	rules "github.com/vsriram/simple-host/host/rules/sites"
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
	unlockFirst := s.lockSite(userID, first)
	defer unlockFirst()
	unlockSecond := s.lockSite(userID, second)
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
	if msg, held := s.deletedNameConflict(ctx, userID, newName); held {
		return result, &Error{Code: "recently_deleted", Message: msg}
	}
	result.Site = site
	result.OldURL = s.deps.Hosts.Addressing().SiteURLFor(site)
	result.NewURL = s.renameURL(handle, newName)
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
		if sqlkit.IsUniqueViolation(err) { // a site took the name meanwhile
			return result, &Error{Code: "site_exists"}
		}
		return result, err
	}
	// The main address may change with the name (address families).
	s.syncDomainRedirect(ctx, site.ID)
	return result, nil
}
