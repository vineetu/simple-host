package db

import (
	"context"

	"github.com/vsriram/simple-host/internal/model"
	"github.com/vsriram/simple-host/internal/repository/sites"
)

// SitePasscodeRow is the shared passcode state.
type SitePasscodeRow = model.SitePasscodeRow

// GetSitePasscode loads a live site's passcode state.
func GetSitePasscode(ctx context.Context, q Querier, siteID string) (SitePasscodeRow, error) {
	return sites.GetSitePasscode(ctx, q, siteID)
}
