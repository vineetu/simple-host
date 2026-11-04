package sites

import (
	"context"
	"fmt"

	"github.com/vsriram/simple-host/internal/db"
	rulesstorage "github.com/vsriram/simple-host/internal/rules/storage"
	"github.com/vsriram/simple-host/internal/tarball"
)

// WebsiteLimitsInput identifies the account holding an event or team website.
type WebsiteLimitsInput struct {
	AccountID string
}

// WebsiteUploadLimit reads the account's archive limit, falling back to the instance default.
func (s *Service) WebsiteUploadLimit(ctx context.Context, in WebsiteLimitsInput) int64 {
	holder, err := db.GetUserByID(ctx, s.database, in.AccountID)
	if err != nil {
		return s.deps.WebsiteArchiveDefault
	}
	limit, _ := s.deps.WebsiteLimit(ctx, &holder)
	return limit
}

// WebsiteUploadLimits describes the account's archive and unpacked-file limits.
func (s *Service) WebsiteUploadLimits(ctx context.Context, in WebsiteLimitsInput) string {
	holder, err := db.GetUserByID(ctx, s.database, in.AccountID)
	if err != nil {
		return "Upload limits are unavailable."
	}
	limit, override := s.deps.WebsiteLimit(ctx, &holder)
	total, file, entries := tarball.FileLimits(rulesstorage.ExtractBudget(limit, override), s.deps.MaxFilesPerSite)
	return fmt.Sprintf("Zip up to %d MB. Unpacked, up to %d MB in total, %d MB per file and %d files.", limit>>20, total>>20, file>>20, entries)
}
