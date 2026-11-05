package operations

import (
	"context"

	"github.com/vsriram/simple-host/internal/model"
	accounts "github.com/vsriram/simple-host/internal/repository/accounts"
	sites "github.com/vsriram/simple-host/internal/repository/sites"
)

// SiteListing loads the operator's complete site inventory.
func (s *Reporting) SiteListing(ctx context.Context) ([]model.Site, error) {
	return sites.ListAllSites(ctx, s.database)
}

// SiteAccounts loads the account rows used by the operator's site inventory.
func (s *Reporting) SiteAccounts(ctx context.Context) ([]model.User, error) {
	return accounts.ListAllUsers(ctx, s.database)
}

// SiteAccountAliases loads the aliases used for account-specific limits in the inventory.
func (s *Reporting) SiteAccountAliases(ctx context.Context) (map[string][]string, error) {
	return accounts.HandleAliasesByUser(ctx, s.database)
}

// SiteAccountLogins loads every password account's own login, keyed by user
// id, so the admin Users page can show it instead of a self sign-up's
// possibly-synthetic users.username (CreatePasswordAccount).
func (s *Reporting) SiteAccountLogins(ctx context.Context) (map[string]string, error) {
	return accounts.PasswordLogins(ctx, s.database)
}
