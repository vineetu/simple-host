package data

import (
	"path/filepath"
)

func (h *Handler) StorageRuntimeDir(c Call) string {
	return filepath.Join(h.Disk.SiteDir(c.OwnerID, c.SiteName), "runtime")
}
func (h *Handler) StorageSQLPath(c Call) string {
	return filepath.Join(h.StorageRuntimeDir(c), "sqlite", c.ResourceName+".sqlite")
}
