package handler

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/vsriram/simple-host/internal/config"
	"github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/storage"
)

const storageTips = "Keep zip files, installers and videos elsewhere and link to them. Remove files you no longer use. Delete old sites you don't need."

func accountRetention(ctx context.Context, database *sql.DB, userID string, siteKeep int, l config.Limits) (keep int, canSet bool, err error) {
	u, err := db.GetUserByID(ctx, database, userID)
	if err != nil {
		return 0, false, err
	}
	handles := accountHandles(ctx, database, &u)
	canSet = l.CanSetKeepVersions(handles...)
	keep = l.KeepVersionsFor(keepVersions, handles...)
	if canSet && siteKeep > 0 {
		keep = siteKeep
	}
	return
}

// retainedFloor uses actual rows, so gaps left by previous pruning do not
// reduce the count. The live version is kept in addition when it is older.
func retainedFloor(versions []db.Version, keep int) int {
	if keep <= 0 || len(versions) <= keep {
		return 0
	}
	return versions[keep-1].VersionNumber
}

func projectedFileBytes(f storage.FileFootprint, versions []db.Version, floor, active, next int, incoming int64, publish bool) int64 {
	total := f.Total() - f.Versions[next] + incoming
	if publish {
		total += incoming - f.Live
		active = next
	}
	for _, v := range versions {
		if floor > 0 && v.VersionNumber < floor && v.VersionNumber != active && v.VersionNumber != next {
			total -= f.Versions[v.VersionNumber]
		}
	}
	return total
}

type accountFileUsage struct {
	UsedBytes int64  `json:"used_bytes"`
	LimitMB   int    `json:"limit_mb"`
	Message   string `json:"message"`
	Tips      string `json:"tips"`
}

func (h *SiteHandler) accountFileUsage(ctx context.Context, user *db.User) (*accountFileUsage, error) {
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
