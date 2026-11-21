package siteshttp

import (
	"context"
	"database/sql"

	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/web"
)

func accountRetention(ctx context.Context, database *sql.DB, userID string, siteKeep int, l edition.Limits) (keep int, canSet bool, err error) {
	return accounts.New(database).SiteRetention(ctx, userID, siteKeep, web.DefaultKeepVersions, l)
}

func (h *SiteHandler) fileCapsEnabled() bool {
	l := h.Limits
	return h.Ed.Storage().FileCaps && (l.MaxSiteTotalMB > 0 || l.MaxAccountMB > 0 || l.MaxSiteTotalOverrides != "" || l.MaxAccountOverrides != "")
}
func (h *SiteHandler) LockAccountFiles(userID string) func() {
	if !h.fileCapsEnabled() {
		return func() {}
	}
	return h.LockSite(userID, "")
}
