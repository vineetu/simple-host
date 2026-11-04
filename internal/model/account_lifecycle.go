package model

import "time"

// AccountForDelete is the account row, locked, with what the delete policy
// needs to decide.
type AccountForDelete struct {
	ID          string
	Username    string
	Handle      string
	IsAdmin     bool
	Suspended   bool
	EventClaims int
	// TakenDown counts the account's sites the operator took down (live or
	// Recently deleted): the person cannot delete those, so neither the
	// account that holds them; the operator can.
	TakenDown int
}

// ErasedSite is one site removed with its account, for the disk clean-up.
type ErasedSite struct {
	ID   string
	Name string
	// Domains is every own address the site had: its custom domain or
	// claimed name and the earlier one it still served at (previous_domain),
	// to unbind and withdraw while they still point at this site.
	Domains []string
}

// ErasedAccount is what EraseAccount removed from the database and leaves
// the caller to remove on disk after the commit.
type ErasedAccount struct {
	UserID  string
	Handle  string
	Aliases []string     // earlier handles, whose disk links go too
	Sites   []ErasedSite // live and Recently deleted
	// Families are the account's address-family suffixes, whose links and
	// issuer requests go too (the rows go with the account).
	Families []string
	// AffectedEventAccounts are the holding accounts of hosted events whose
	// team this person admined and whose team row was deleted outright
	// below (not kept): the caller sweeps orphan team sites for each, the
	// same way leaveTeam does (Grok security review, 2026-11-01).
	AffectedEventAccounts []string
}

// AccountAddress is one own address of one of the account's live sites.
type AccountAddress struct {
	Site     string
	Domain   string
	Status   string
	Previous string
}

// RetiredAccountName is a name the account's sites used to have.
type RetiredAccountName struct {
	Name string
	Site string // "" when that site is gone
}

// VisitorSignIn is a site this person is signed in to as a visitor.
type VisitorSignIn struct {
	SiteID    string
	Host      string
	FirstSeen time.Time
	LastSeen  time.Time
}

// Submission is one list item this person submitted to someone else's site.
type Submission struct {
	ID         int64
	SiteID     string
	Collection string
	Data       []byte
	CreatedAt  time.Time
}
