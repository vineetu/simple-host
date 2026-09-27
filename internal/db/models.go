package db

import (
	"database/sql"
	"time"
)

type User struct {
	ID       string
	Username string
	// KeyHash is the stored hash of the API key this request authenticated
	// with; empty for the env admin key and for internal credentials.
	KeyHash     string
	IsAdmin     bool
	CreatedAt   time.Time
	Handle      sql.NullString
	DisplayName sql.NullString
	// Suspended is set when the operator has suspended the account: its keys
	// and connected apps stop working and its sites are taken down, without
	// deleting anything. Loaded by GetUserByAPIKey, GetUserByID and ListAllUsers.
	Suspended       bool
	SuspendedReason string
}

type Site struct {
	ID            string
	UserID        string
	Name          string
	ActiveVersion int
	SiteURL       string
	CreatedAt     time.Time
	UpdatedAt     time.Time
	CustomDomain  sql.NullString
	DomainStatus  sql.NullString
	Visibility    string // showcase visibility: 'public' | 'unlisted' (default 'public')

	// OwnerUsername is populated only by ListAllSites (admin view).
	OwnerUsername string
	// OwnerHandle is the owner's handle ("" if none), populated by the
	// queries that answer the API so a site's address is computed on read.
	OwnerHandle string

	// Operator take-down. SiteSuspended is this site's own flag; OwnerSuspended
	// is its owner's account being suspended, which takes every site of theirs
	// down too. Populated by GetSiteByUser, ListSitesByUser and ListAllSites.
	SiteSuspended        bool
	SiteSuspendedReason  string
	OwnerSuspended       bool
	OwnerSuspendedReason string
}

// Suspended reports whether the site is taken down, by itself or through its
// owner's account.
func (s Site) Suspended() bool { return s.SiteSuspended || s.OwnerSuspended }

// SuspendedReason is the reason shown to the owner: the site's own, else the
// account's.
func (s Site) SuspendedReason() string {
	if s.SiteSuspended {
		return s.SiteSuspendedReason
	}
	if s.OwnerSuspended {
		return s.OwnerSuspendedReason
	}
	return ""
}

type Version struct {
	ID            string
	SiteID        string
	VersionNumber int
	DiskPath      string
	Status        string
	ArchiveSHA256 string
	CreatedAt     time.Time
}
