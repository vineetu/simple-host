package sites

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	accountapp "github.com/vsriram/simple-host/internal/app/accounts"
	"github.com/vsriram/simple-host/internal/model"
	accounts "github.com/vsriram/simple-host/internal/repository/accounts"
	repo "github.com/vsriram/simple-host/internal/repository/sites"
	rules "github.com/vsriram/simple-host/internal/rules/sites"
)

var ErrInvalidHost = errors.New("invalid site host")

// HostOwner resolves only addressable account/site label pairs, before alias fallback.
func (s *Service) HostOwner(ctx context.Context, handle, name string) (model.User, error) {
	if !HandleAddressable(handle) || !rules.ValidSiteName.MatchString(name) {
		return model.User{}, ErrInvalidHost
	}
	return accounts.GetUserByHandle(ctx, s.database, handle)
}

// SiteHostAddress decides whether the site's host may be served and builds its hostname.
func SiteHostAddress(handle, name, base string, enabled, personAddress, ready bool) string {
	if !enabled || !personAddress || !rules.ValidSiteName.MatchString(name) || strings.HasPrefix(name, "xn--") || !ready {
		return ""
	}
	return strings.ToLower(name) + "." + strings.ToLower(handle) + "." + strings.ToLower(base)
}

// PhotoTarget checks naming before looking up the destination for a photo upload.
func (s *Service) PhotoTarget(ctx context.Context, userID, name string) (model.Site, error) {
	if err := rules.ValidateShape(name); err != nil {
		return model.Site{}, &Error{Code: "invalid_name", Message: "Invalid site name."}
	}
	return repo.GetSiteByUser(ctx, s.database, userID, name)
}

// SuspensionGate excludes malformed identities and event holding accounts before mutation.
func (s *Service) SuspensionGate(ctx context.Context, id string, holdingAccounts bool) error {
	if !rules.UUIDShape.MatchString(id) {
		return sql.ErrNoRows
	}
	held, err := accountapp.HoldingAccount(ctx, s.database, id, holdingAccounts)
	if err != nil {
		return err
	}
	if held {
		return &Error{Code: "event_account", Message: "this account holds an event; take the event down or restore it from the admin Events tab"}
	}
	return nil
}

// SelectConnectionTeam checks the connector context and identifier before changing its grant.
func (s *Service) SelectConnectionTeam(ctx context.Context, enabled bool, grantID, userID, teamID string) error {
	if !enabled || grantID == "" || userID == "" {
		return errors.New("connect your personal account to select a team")
	}
	if !rules.UUIDShape.MatchString(teamID) {
		return errors.New("team_id must be a team UUID")
	}
	ok, err := s.accounts.SelectOAuthTeam(ctx, grantID, userID, strings.ToLower(teamID))
	if err != nil {
		return errors.New("could not select the team")
	}
	if !ok {
		return errors.New("select a team you currently belong to")
	}
	return nil
}

// AddressedSite resolves a DNS-label site name without querying malformed names.
func (s *Service) AddressedSite(ctx context.Context, userID, name string) (model.Site, error) {
	if !rules.ValidSiteName.MatchString(name) {
		return model.Site{}, sql.ErrNoRows
	}
	return repo.GetSiteByUser(ctx, s.database, userID, name)
}

// AddressedOldSite resolves a retired DNS-label name at the address boundary.
func (s *Service) AddressedOldSite(ctx context.Context, userID, name string) (string, string, error) {
	if !rules.ValidSiteName.MatchString(name) {
		return "", "", sql.ErrNoRows
	}
	return repo.ResolveOldSiteName(ctx, s.database, userID, name)
}

// AdminSiteTarget resolves the current site and owner before adapting an admin action.
func (s *Service) AdminSiteTarget(ctx context.Context, id string, deleting bool) (model.Site, model.User, error) {
	site, err := repo.GetSiteByID(ctx, s.database, id)
	if err == nil && site.Deleted {
		err = sql.ErrNoRows
	}
	var owner model.User
	if err == nil {
		owner, err = accounts.GetUserByID(ctx, s.database, site.UserID)
	}
	if err != nil {
		return site, owner, err
	}
	if err := rules.ValidateShape(site.Name); err != nil && deleting {
		return site, owner, &Error{Code: "legacy_site_name", Message: "this site's name (" + site.Name + ") is from before the current naming rules, so it cannot be deleted from here; its owner can rename it first, or take it down instead"}
	}
	return site, owner, nil
}

// ContentPathOwner resolves a path-model owner only after both path labels pass
// their existing boundary rules, including aliases of current handles.
func (s *Service) ContentPathOwner(ctx context.Context, handle, name string) (model.User, error) {
	if !rules.VisitorHandle.MatchString(handle) || !rules.ValidSiteName.MatchString(name) {
		return model.User{}, sql.ErrNoRows
	}
	return accounts.GetUserByHandleOrAlias(ctx, s.database, handle)
}
