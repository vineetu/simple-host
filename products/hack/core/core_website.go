package core

import (
	"context"
	"database/sql"

	appsites "github.com/vsriram/simple-host/host/sites"
	productconfig "github.com/vsriram/simple-host/products/hack/config"

	hackmodel "github.com/vsriram/simple-host/products/hack/model"
)

// websiteLimits are the instance's site size limits, as an event or team
// website's upload form states them.
type WebsiteLimits struct {
	database *sql.DB
	limits   *productconfig.Limits
	// archiveDefault is the per-site size cap for an account with no
	// MAX_ARCHIVE_MB_OVERRIDES entry (SetSiteArchiveDefault).
	ArchiveDefault int64
}

func (l *WebsiteLimits) sites() *appsites.Service {
	return appsites.New(l.database, nil, nil, 0, appsites.Deps{
		WebsiteArchiveDefault: l.ArchiveDefault,
		Limits:                l.limits.Limits,
		MaxFilesPerSite:       l.limits.MaxFilesPerSite,
	})
}

// uploadLimit is the largest zip the event's websites take, in bytes.
func (l *WebsiteLimits) UploadLimit(ctx context.Context, ev hackmodel.Event) int64 {
	return l.sites().WebsiteUploadLimit(ctx, appsites.WebsiteLimitsInput{AccountID: ev.AccountID})
}

// uploadLimits is the upload limits in words.
func (l *WebsiteLimits) UploadLimits(ctx context.Context, ev hackmodel.Event) string {
	return l.sites().WebsiteUploadLimits(ctx, appsites.WebsiteLimitsInput{AccountID: ev.AccountID})
}
