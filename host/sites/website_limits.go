package sites

import (
	"context"
	"fmt"

	accounts "github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/tarball"
)

// WebsiteLimitsInput identifies the account holding an event or team website.
type WebsiteLimitsInput struct {
	AccountID string
}

// WebsiteUploadLimit reads the account's archive limit, falling back to the instance default.
func (s *Service) WebsiteUploadLimit(ctx context.Context, in WebsiteLimitsInput) int64 {
	holder, err := accounts.GetUserByID(ctx, s.DB, in.AccountID)
	if err != nil {
		return s.Deps.WebsiteArchiveDefault
	}
	limit, _ := s.Accounts.SiteLimit(ctx, &holder, s.Deps.Limits, s.Deps.WebsiteArchiveDefault)
	return limit
}

// WebsiteUploadLimits describes the account's archive and unpacked-file limits.
func (s *Service) WebsiteUploadLimits(ctx context.Context, in WebsiteLimitsInput) string {
	holder, err := accounts.GetUserByID(ctx, s.DB, in.AccountID)
	if err != nil {
		return "Upload limits are unavailable."
	}
	limit, override := s.Accounts.SiteLimit(ctx, &holder, s.Deps.Limits, s.Deps.WebsiteArchiveDefault)
	total, file, entries := tarball.FileLimits(ExtractBudget(limit, override), s.Deps.MaxFilesPerSite)
	return fmt.Sprintf("Zip up to %d MB. Unpacked, up to %d MB in total, %d MB per file and %d files.", limit>>20, total>>20, file>>20, entries)
}
