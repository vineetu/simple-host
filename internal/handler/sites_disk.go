package handler

import (
	"context"

	"github.com/vsriram/simple-host/internal/model"
)

// SiteDisk is the filesystem capability consumed by the site transport and
// its application-service collaborators. The composition root supplies the disk.
type SiteDisk interface {
	AccountFileBytes(userID string) (int64, error)
	BindDomain(userID, siteName, domain string) error
	BindFamily(userID, suffix string) error
	Changes() int64
	ClearDomainRedirect(userID, siteName string) error
	DataDir() string
	DeleteSite(userID, siteName string) error
	DeleteUser(userID, handle string) error
	DeleteVersion(userID, siteName string, versionNum int) error
	EnsureHandleLink(handle, userID string) error
	FamilyLinkTarget(suffix string) string
	HasHandleLink(handle string) bool
	HasPasscodeMarker(userID, siteName string) bool
	IsOffline(userID, siteName string) bool
	IsSuspended(userID, siteName string) bool
	IsUnlisted(userID, siteName string) bool
	MarkChanged()
	PurgeTrashedSite(userID, siteID string) error
	PurgeTrashedSiteLinks(userID, siteName, domain string) error
	RemoveHandleLink(handle string) error
	RemoveHandleLinkOf(handle, userID string) error
	RenameSite(userID, oldName, newName string, domains ...string) error
	RestoreSite(userID, siteName, siteID string) error
	SetDomainRedirect(userID, siteName, domain string) error
	SetLivesElsewhere(userID, siteName, domain string) error
	SetOffline(userID, siteName string, on bool) error
	SetPasscodeMarker(userID, siteName string, on bool) error
	SetSuspended(userID, siteName string, on bool) error
	SetTrashedSuspended(userID, siteID string, on bool) error
	SetUnlisted(userID, siteName string, on bool) error
	SiteDir(userID, siteName string) string
	SiteFootprint(userID, name string) (model.FileFootprint, error)
	TrashDir(userID, siteID string) string
	TrashSite(userID, siteName, siteID string) error
	UnbindDomain(domain string) error
	UnbindDomainOf(domain, userID, siteName string) (bool, error)
	UnbindFamily(suffix string) error
	UpdateCurrent(userID, siteName string, versionNum int) error
	VersionDir(userID, siteName string, versionNum int) string
	WriteFiles(ctx context.Context, userID, siteName string, versionNum int, files map[string][]byte) error
}
