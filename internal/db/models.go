package db

import (
	"database/sql"
	"time"
)

type User struct {
	ID          string
	Username    string
	APIKey      string
	IsAdmin     bool
	CreatedAt   time.Time
	Handle      sql.NullString
	DisplayName sql.NullString
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
