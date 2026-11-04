package accounts

import (
	"context"

	"github.com/vsriram/simple-host/internal/edition"
	"github.com/vsriram/simple-host/internal/model"
	repo "github.com/vsriram/simple-host/internal/repository/accounts"
)

// SiteAccountHandles resolves the current and earlier handles in override priority order.
// A failed alias read preserves the current-handle fallback.
func (s *Service) SiteAccountHandles(ctx context.Context, user *model.User) []string {
	handles := []string{user.Handle.String}
	if aliases, err := repo.ListHandleAliases(ctx, s.database, user.ID); err == nil {
		handles = append(handles, aliases...)
	}
	return handles
}

// SiteRetention resolves the account override before applying an allowed per-site setting.
func (s *Service) SiteRetention(ctx context.Context, userID string, siteKeep, fallback int, limits edition.Limits) (keep int, canSet bool, err error) {
	user, err := repo.GetUserByID(ctx, s.database, userID)
	if err != nil {
		return 0, false, err
	}
	handles := s.SiteAccountHandles(ctx, &user)
	canSet = limits.CanSetKeepVersions(handles...)
	keep = limits.KeepVersionsFor(fallback, handles...)
	if canSet && siteKeep > 0 {
		keep = siteKeep
	}
	return
}
