package handler

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"time"

	"github.com/vsriram/simple-host/internal/config"
	"github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/storage"
)

const storageTips = "Shrink photos to about 1600 px wide; use WebP or JPEG at about 80% quality (phone photos are often 4–12 MB). Keep zip files, installers and videos elsewhere and link to them. Remove files you no longer use. Delete old sites you don't need."

func accountRetention(ctx context.Context, database *sql.DB, userID string, siteKeep int) (keep int, canSet bool, err error) {
	u, err := db.GetUserByID(ctx, database, userID)
	if err != nil {
		return 0, false, err
	}
	handles := accountHandles(ctx, database, &u)
	l := config.Active()
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

// Called before any version files or durable rows are written. Both deploy
// formats, create-or-update and unpublished deploys converge here.
func (h *SiteHandler) checkFileCaps(w http.ResponseWriter, r *http.Request, user *db.User, site db.Site, files map[string][]byte, next int, publish bool) bool {
	l := config.Active()
	if hackMode || (l.MaxSiteTotalMB == 0 && l.MaxAccountMB == 0 && l.MaxSiteTotalOverrides == "" && l.MaxAccountOverrides == "") {
		return true
	}
	handles := accountHandles(r.Context(), h.database, user)
	siteMB, accountMB := l.SiteTotalMBFor(handles...), l.AccountMBFor(handles...)
	cutoff, err := time.Parse(time.RFC3339, l.SiteTotalCapFrom)
	if err != nil || site.CreatedAt.Before(cutoff) {
		siteMB = 0
	}
	if siteMB == 0 && accountMB == 0 {
		return true
	}
	f, err := h.disk.SiteFootprint(user.ID, site.Name)
	if err != nil {
		writeJSON(w, 500, errorResponse{Error: "could not measure website files"})
		return false
	}
	var incoming int64
	for _, b := range files {
		incoming += int64(len(b))
	}
	keep, _, err := accountRetention(r.Context(), h.database, user.ID, site.KeepVersions)
	if err != nil {
		writeJSON(w, 500, errorResponse{Error: "internal server error"})
		return false
	}
	versions, err := db.ListVersionsBySite(r.Context(), h.database, site.ID)
	if err != nil {
		writeJSON(w, 500, errorResponse{Error: "internal server error"})
		return false
	}
	versions = append([]db.Version{{VersionNumber: next}}, versions...)
	floor := retainedFloor(versions, keep)
	projected := projectedFileBytes(f, versions, floor, site.ActiveVersion, next, incoming, publish)
	if siteMB > 0 && projected > int64(siteMB)<<20 {
		writeJSON(w, 413, errorResponse{Code: "site_total_too_large", Error: fmt.Sprintf("This website may use at most %d MB including its live files and kept versions; it currently uses %.1f MB. This deploy would use %.1f MB.", siteMB, float64(f.Total())/(1<<20), float64(projected)/(1<<20)), Hint: storageTips})
		return false
	}
	if accountMB > 0 {
		current, err := h.disk.AccountFileBytes(user.ID)
		if err != nil {
			writeJSON(w, 500, errorResponse{Error: "could not measure account files"})
			return false
		}
		after := current - f.Total() + projected
		if after > int64(accountMB)<<20 {
			writeJSON(w, 413, errorResponse{Code: "account_storage_full", Error: fmt.Sprintf("This account may use at most %d MB for website files and kept versions; it currently uses %.1f MB. This deploy would use %.1f MB.", accountMB, float64(current)/(1<<20), float64(after)/(1<<20)), Hint: storageTips})
			return false
		}
	}
	return true
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
	mb := config.Active().AccountMBFor(accountHandles(ctx, h.database, user)...)
	msg := fmt.Sprintf("%.1f MB used", float64(used)/(1<<20))
	if mb == 1024 {
		msg = fmt.Sprintf("%.1f MB of 1 GB used", float64(used)/(1<<20))
	} else if mb > 0 {
		msg = fmt.Sprintf("%.1f MB of %d MB used", float64(used)/(1<<20), mb)
	}
	return &accountFileUsage{used, mb, msg, storageTips}, nil
}

func fileCapsEnabled() bool {
	l := config.Active()
	return !hackMode && (l.MaxSiteTotalMB > 0 || l.MaxAccountMB > 0 || l.MaxSiteTotalOverrides != "" || l.MaxAccountOverrides != "")
}
func (h *SiteHandler) lockAccountFiles(userID string) func() {
	if !fileCapsEnabled() {
		return func() {}
	}
	return h.lockSite(userID, "")
}
