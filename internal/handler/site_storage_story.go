package handler

import (
	"net/http"
	"os"
)

func (c storageCall) addOnly() bool { return !c.owner && c.resource.WriteMode == "add" }
func (c storageCall) ownReader() string {
	if !c.owner && c.resource.Read == "own" {
		if c.visitorID == "" {
			return "!missing-visitor!"
		} // impossible stored identity; never an unfiltered read
		return c.visitorID
	}
	return ""
}
func storageAddDeleteOK(w http.ResponseWriter, r *http.Request, c storageCall) bool {
	if c.addOnly() && r.Method == http.MethodDelete {
		storageError(w, 403, "add_only", "visitors can add only")
		return false
	}
	return true
}
func (h *SiteHandler) storageBudgetUsed(u siteStorageUsage) int64 {
	if hackMode {
		return u.total()
	}
	return u.data()
}
func (h *SiteHandler) storageFilesLimitBytes() int64 {
	if hackMode {
		return storageSiteLimitBytes()
	}
	return storageLimitBytes(os.Getenv("SITE_STORAGE_FILES_MAX_BYTES"), 10000000, 10<<30)
}

func (h *SiteHandler) storageFilesRemaining(u siteStorageUsage) int64 {
	used := u.Files
	if hackMode {
		used = u.total()
	}
	return max(int64(0), h.storageFilesLimitBytes()-used)
}
