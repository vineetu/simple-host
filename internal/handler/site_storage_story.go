package handler

import (
	"os"
)

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
