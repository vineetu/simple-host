package sites

import (
	"context"

	"github.com/vsriram/simple-host/internal/model"
)

// Disk is the filesystem capability consumed by this service.
type Disk interface {
	AccountFileBytes(userID string) (int64, error)
	DataDir() string
	DeleteSite(userID, siteName string) error
	DeleteVersion(userID, siteName string, versionNum int) error
	EnsureHandleLink(handle, userID string) error
	PurgeTrashedSite(userID, siteID string) error
	PurgeTrashedSiteLinks(userID, siteName, domain string) error
	RenameSite(userID, oldName, newName string, domains ...string) error
	RestoreSite(userID, siteName, siteID string) error
	SetSuspended(userID, siteName string, on bool) error
	SetTrashedSuspended(userID, siteID string, on bool) error
	SetUnlisted(userID, siteName string, on bool) error
	SiteFootprint(userID, name string) (model.FileFootprint, error)
	TrashSite(userID, siteName, siteID string) error
	UnbindDomainOf(domain, userID, siteName string) (bool, error)
	UpdateCurrent(userID, siteName string, versionNum int) error
	VersionDir(userID, siteName string, versionNum int) string
	WriteFiles(ctx context.Context, userID, siteName string, versionNum int, files map[string][]byte) error
}
