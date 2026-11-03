package handler

import "os"
import appstorage "github.com/vsriram/simple-host/internal/app/storage"

func (h *SiteHandler) storageSvc() *appstorage.Service {
	return appstorage.New(h.database, appstorage.Deps{Locks: &h.uploadLocks, CombinedPool: h.ed.Storage().CombinedPool, SiteLimit: os.Getenv("SITE_STORAGE_MAX_BYTES"), FilesLimit: os.Getenv("SITE_STORAGE_FILES_MAX_BYTES")})
}
