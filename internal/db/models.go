package db

import (
	"database/sql"
	"time"
)

type User struct {
	ID        string
	Username  string
	APIKey    string
	IsAdmin   bool
	CreatedAt time.Time
	Handle    sql.NullString
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

	// OwnerUsername is populated only by ListAllSites (admin view).
	OwnerUsername string
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
