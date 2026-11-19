package storage

import (
	"context"
	"io"
	"io/fs"
	"time"
)

// FileVersion identifies deploy contents without exposing their storage path.
// Version zero selects the live copy. TrashID selects a recently deleted site.
type FileVersion struct {
	UserID, SiteName, TrashID string
	Version                   int
}

// SiteFile is one entry in a deploy tree. Reader is valid only during the walk
// callback, and is nil for non-regular entries. Names use slash separators.
// Bytes are opened only when Reader is read; read errors come from Reader.
// WalkFiles delivers per-entry metadata errors to the callback, like fs.WalkDir.
// On metadata errors, Name and Mode still carry the entry's name and type.
type SiteFile struct {
	Name    string
	Size    int64
	ModTime time.Time
	Mode    fs.FileMode
	Reader  io.Reader
}

// SiteFiles is the deploy-content backend. It has no local-path capability.
// Lifecycle calls retain their existing coupled disk/link semantics; a later
// backend must coordinate those with LocalServing, as DiskStorage does today.
// Runtime SQLite databases and uploaded file buckets are outside this contract.
type SiteFiles interface {
	WriteFiles(context.Context, string, string, int, map[string][]byte) error
	ReadFile(context.Context, FileVersion, string) ([]byte, error)
	// Live opens preserve component-by-component page path resolution within
	// the tree; retained opens also accept absolute symlinks contained in it.
	OpenFile(context.Context, FileVersion, string) (io.ReadCloser, fs.FileInfo, error)
	// StatVersion checks one deploy tree without listing its parent or reading bytes.
	StatVersion(context.Context, FileVersion) (fs.FileInfo, error)
	// StatFile resolves contained symlinks and returns metadata without reading bytes.
	StatFile(context.Context, FileVersion, string) (fs.FileInfo, error)
	WalkFiles(context.Context, FileVersion, func(SiteFile, error) error) error
	ListVersions(string, string) ([]int, error)
	DeleteVersion(string, string, int) error
	SiteFootprint(string, string) (FileFootprint, error)
	AccountFileBytes(string) (int64, error)
	DeleteSite(string, string) error
	DeleteUser(string, string) error
	RenameSite(string, string, string, ...string) error
	TrashSite(string, string, string) error
	RestoreSite(string, string, string) error
	PurgeTrashedSite(string, string) error
}

// LocalServing is the local tree consumed by Go serving, nginx, Caddy and Static,
// plus local operational files. Runtime resources remain local and use SiteDir
// and TrashDir; neither their bytes nor SQLite mechanics belong to SiteFiles.
type LocalServing interface {
	DataDir() string
	SiteDir(string, string) string
	TrashDir(string, string) string
	VersionDir(string, string, int) string
	UpdateCurrent(string, string, int) error
	BindDomain(string, string, string) error
	UnbindDomain(string) error
	UnbindDomainOf(string, string, string) (bool, error)
	BindFamily(string, string) error
	UnbindFamily(string) error
	FamilyDir() string
	FamilyLinkTarget(string) string
	EnsureHandleLink(string, string) error
	HasHandleLink(string) bool
	RemoveHandleLink(string) error
	RemoveHandleLinkOf(string, string) error
	PurgeTrashedSiteLinks(string, string, string) error
	SetDomainRedirect(string, string, string) error
	ClearDomainRedirect(string, string) error
	SetLivesElsewhere(string, string, string) error
	SetSuspended(string, string, bool) error
	IsSuspended(string, string) bool
	SetTrashedSuspended(string, string, bool) error
	SetTrashedAccessMarker(string, string, bool) error
	SetOffline(string, string, bool) error
	IsOffline(string, string) bool
	SetPasscodeMarker(string, string, bool) error
	HasPasscodeMarker(string, string) bool
	RedirectsFile(string, string) string
	ReadRedirects(string, string) ([]byte, error)
	ReadTrashedRedirects(string, string) ([]byte, error)
	AccountAuxiliaryBytes(context.Context, string) (int64, error)
	Changes() int64
	MarkChanged()
}

var _ SiteFiles = (*DiskStorage)(nil)
var _ LocalServing = (*DiskStorage)(nil)
