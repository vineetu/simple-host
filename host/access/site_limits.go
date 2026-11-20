package access

import (
	"context"

	"github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/model"
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

// SiteLimit is the largest site this account may deploy, in bytes: its
// MAX_ARCHIVE_MB_OVERRIDES entry (its current handle, then its earlier ones),
// else def, the instance's MAX_ARCHIVE_MB. override says an entry matched.
func (s *Service) SiteLimit(ctx context.Context, user *model.User, limits edition.Limits, def int64) (limit int64, override bool) {
	if limits.MaxArchiveOverrides == "" || user == nil {
		return def, false
	}
	if mb, ok := limits.ArchiveOverrideMB(s.SiteAccountHandles(ctx, user)...); ok {
		return int64(mb) << 20, true
	}
	return def, false
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
