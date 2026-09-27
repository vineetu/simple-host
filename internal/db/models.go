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

	// Filled only by the site-list queries (ListSitesByUser, ListAllSites),
	// so the owner's site list can show a pending domain's problem, its DNS
	// record and expiry, and when the site was last deployed.
	DomainLastError  sql.NullString
	DomainBoundAt    sql.NullTime
	DomainVerifiedAt sql.NullTime
	LastDeployedAt   sql.NullTime
	// PreviousDomain is the earlier address a site keeps serving at while a
	// new domain is pending; DomainCertStatus is pending | issuing | live |
	// failed ("" = not known yet).
	PreviousDomain   string
	DomainCertStatus string
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
