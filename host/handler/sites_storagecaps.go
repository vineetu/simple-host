package handler

import (
	"context"
	"database/sql"
	"fmt"

	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/model"
)

const storageTips = "Keep zip files, installers and videos elsewhere and link to them. Remove files you no longer use. Delete old sites you don't need."

func accountRetention(ctx context.Context, database *sql.DB, userID string, siteKeep int, l edition.Limits) (keep int, canSet bool, err error) {
	return accounts.New(database).SiteRetention(ctx, userID, siteKeep, keepVersions, l)
}

type accountFileUsage struct {
	UsedBytes int64  `json:"used_bytes"`
	LimitMB   int    `json:"limit_mb"`
	Message   string `json:"message"`
	Tips      string `json:"tips"`
}

func (h *SiteHandler) accountFileUsage(ctx context.Context, user *model.User) (*accountFileUsage, error) {
	used, err := h.disk.AccountFileBytes(user.ID)
	if err != nil {
		return nil, err
	}
	mb := h.limits.AccountMBFor(accountHandles(ctx, h.database, user)...)
	msg := fmt.Sprintf("%.1f MB used", float64(used)/(1<<20))
	if mb == 1024 {
		msg = fmt.Sprintf("%.1f MB of 1 GB used", float64(used)/(1<<20))
	} else if mb > 0 {
		msg = fmt.Sprintf("%.1f MB of %d MB used", float64(used)/(1<<20), mb)
	}
	return &accountFileUsage{used, mb, msg, storageTips}, nil
}

func (h *SiteHandler) fileCapsEnabled() bool {
	l := h.limits
	return h.ed.Storage().FileCaps && (l.MaxSiteTotalMB > 0 || l.MaxAccountMB > 0 || l.MaxSiteTotalOverrides != "" || l.MaxAccountOverrides != "")
}
func (h *SiteHandler) lockAccountFiles(userID string) func() {
	if !h.fileCapsEnabled() {
		return func() {}
	}
	return h.lockSite(userID, "")
}
