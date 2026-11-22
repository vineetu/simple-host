package ops

import (
	"context"

	"github.com/vsriram/simple-host/host/access"
	accounts "github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/model"
	appsites "github.com/vsriram/simple-host/host/sites"
	sites "github.com/vsriram/simple-host/host/sites/repo"
)

// SiteListing loads the operator's complete site inventory.
func (s *Reporting) SiteListing(ctx context.Context) ([]model.Site, error) {
	return sites.ListAllSites(ctx, s.database)
}

// AdminAccount is one account on the operator's Users page with its sites.
type AdminAccount struct {
	User model.User
	// Login is the account's own login (account_passwords.login), "" with no
	// password row. A self sign-up's users.username is synthetic
	// (CreatePasswordAccount), so the page shows Login whenever present.
	Login string
	Sites []model.Site
	// MaxSites is accounts.MaxSitesOf (nil: no cap) and MaxSiteMB the
	// account's site size limit, both read with the account's earlier handles.
	MaxSites  any
	MaxSiteMB int
}

// AdminUsers is the operator's Users page: every account with its sites,
// how many sites there are and how many accounts own one.
type AdminUsers struct {
	Accounts              []AdminAccount
	SiteCount, OwnerCount int
}

// AdminAccounts is every registered account with its sites nested, for the
// operator's Users page (API keys are never read). The
// earlier handles are read only where MAX_SITES_OVERRIDES or
// MAX_ARCHIVE_MB_OVERRIDES may name one.
func (s *Reporting) AdminAccounts(ctx context.Context, limits edition.Limits) (AdminUsers, error) {
	users, err := accounts.ListAllUsers(ctx, s.database)
	if err != nil {
		return AdminUsers{}, err
	}
	listing, err := sites.ListAllSites(ctx, s.database)
	if err != nil {
		return AdminUsers{}, err
	}
	logins, err := accounts.PasswordLogins(ctx, s.database)
	if err != nil {
		return AdminUsers{}, err
	}
	var aliases map[string][]string
	if limits.MaxSitesOverrides != "" || limits.MaxArchiveOverrides != "" {
		if aliases, err = accounts.HandleAliasesByUser(ctx, s.database); err != nil {
			return AdminUsers{}, err
		}
	}
	byUser := make(map[string][]model.Site, len(users))
	for _, site := range listing {
		byUser[site.UserID] = append(byUser[site.UserID], site)
	}
	out := make([]AdminAccount, 0, len(users))
	for _, u := range users {
		out = append(out, AdminAccount{
			User: u, Login: logins[u.ID], Sites: byUser[u.ID],
			MaxSites:  access.MaxSitesOf(limits, u, aliases[u.ID]),
			MaxSiteMB: appsites.SiteLimitMB(limits, append([]string{u.Handle.String}, aliases[u.ID]...)...),
		})
	}
	return AdminUsers{Accounts: out, SiteCount: len(listing), OwnerCount: len(byUser)}, nil
}
