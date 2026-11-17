package handler

func (h *SiteHandler) storageBudgetUsed(u siteStorageUsage) int64 {
	return h.storageSvc().BudgetUsed(u)
}
func (h *SiteHandler) storageFilesLimitBytes() int64 { return h.storageSvc().FilesLimitBytes() }
func (h *SiteHandler) storageFilesRemaining(u siteStorageUsage) int64 {
	return h.storageSvc().FilesRemaining(u)
}
