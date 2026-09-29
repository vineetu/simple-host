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
	KeyHash string
	// KeyScope is that key's scope (KeyScopeFull or KeyScopeDeploy); empty
	// when KeyHash is.
	KeyScope string
	// KeyExpiresAt is set alongside ErrKeyExpired: when the key expired.
	KeyExpiresAt *time.Time
	IsAdmin      bool
	CreatedAt    time.Time
	Handle       sql.NullString
	DisplayName  sql.NullString
	// Suspended is set when the operator has suspended the account: its keys
	// and connected apps stop working and its sites are taken down, without
	// deleting anything. Loaded by GetUserByAPIKey, GetUserByID and ListAllUsers.
	Suspended       bool
	SuspendedReason string
	// Signup is where the account came from (see SetSignup). Loaded only by
	// ListAllUsers, for the admin page.
	Signup Signup
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
	// DomainToken is the site's custom-domain ownership token (the value of
	// the TXT record _simple-host.<domain>).
	DomainToken string

	// Operator take-down. SiteSuspended is this site's own flag; OwnerSuspended
	// is its owner's account being suspended, which takes every site of theirs
	// down too. Populated by GetSiteByUser, ListSitesByUser and ListAllSites.
	SiteSuspended        bool
	SiteSuspendedReason  string
	OwnerSuspended       bool
	OwnerSuspendedReason string

	// Offline: its owner has taken it offline (every address shows "This
	// site is offline", visitor saves are refused, nothing is deleted).
	// Populated by GetSiteByUser, ListSitesByUser, ListAllSites and GetSiteByID.
	Offline bool

	// Deleted: the site is in Recently deleted. Only GetSiteByID sees such
	// rows; every other lookup skips them.
	Deleted bool

	// KeepVersions is how many deploys of this site are kept: 0 = the
	// instance setting (KEEP_VERSIONS), N >= 1 = the newest N plus always the
	// live one (sites.keep_versions). Populated by GetSiteByUser,
	// ListSitesByUser, ListAllSites and GetSiteByID.
	KeepVersions int
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
