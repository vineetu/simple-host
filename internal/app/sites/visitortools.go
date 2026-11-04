package sites

import (
	"context"

	accounts "github.com/vsriram/simple-host/internal/repository/accounts"
	repo "github.com/vsriram/simple-host/internal/repository/sites"
)

// RecordVisitorSiteInput identifies the grant, account and canonical address.
type RecordVisitorSiteInput struct{ Address, GrantID, UserID string }

// RecordVisitorSite records a successful visitor tool's site.
func (s *Service) RecordVisitorSite(ctx context.Context, in RecordVisitorSiteInput) error {
	return accounts.RecordVisitorSite(ctx, s.database, in.Address, in.GrantID, in.UserID)
}

// VisitorAccountExists reports whether a structural identifier names an account.
func (s *Service) VisitorAccountExists(ctx context.Context, id string) (bool, error) {
	return accounts.VisitorAccountExists(ctx, s.database, id)
}

// VisitorSharedSite identifies a site shared with a signed-in email.
type VisitorSharedSite = repo.VisitorSharedSite

// VisitorSharedSites lists shared sites in name order.
func (s *Service) VisitorSharedSites(ctx context.Context, email string) ([]VisitorSharedSite, error) {
	return repo.VisitorSharedSites(ctx, s.database, email)
}
