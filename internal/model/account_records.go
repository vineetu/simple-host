package model

import (
	"database/sql"
	"time"
)

// User records the account identity.
type User struct {
	ID       string
	Username string
	// KeyHash is the stored hash of the API key this request authenticated
	// with; empty for the env admin key and for internal credentials.
	KeyHash string
	// KeyScope is that key's scope (KeyScopeFull or KeyScopeDeploy); empty
	// when KeyHash is.
	KeyScope string
	// KeyExpiresAt is the authenticating key's fixed expiry, if any.
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
	// Team is set when the request came with a team credential (hosted
	// events, hack_sites.go): this User is then the event's holding account,
	// acting on the one site Team.TeamSlug, for the person Team.MemberID.
	Team *TeamIdentity
	// EventWebsite is set only while a trusted apex organiser route publishes
	// the event holding account's custom page.
	EventWebsite *EventWebsiteIdentity
}

// EventWebsiteIdentity records the account identity.
type EventWebsiteIdentity struct {
	EventID     string
	EventSlug   string
	OrganiserID string
}

// Signup is where an account came from, recorded once when it is created
// (v075-signup-source.sql). Source is one of the Signup* values, or
// SignupConnectorPrefix followed by the connected app's name; Agent names the
// agent for SignupAgent; Method is how the person proved who they are.
// Nothing here is ever an IP address.
type Signup struct {
	Source   string
	Agent    string
	Method   string
	Inferred bool // backfilled from other rows, not recorded at the time
	// Detail is the outside link that brought the browser in, when the
	// account is credited to one (users.signup_detail, zf-signup-detail.sql).
	Detail *SignupDetail
}

// SignupDetail contains same-visit URL/form attribution: from, utm_* and
// the linking host only, recorded at account creation. Never an IP address.
type SignupDetail struct {
	From        string    `json:"from"`
	UTMSource   string    `json:"utm_source,omitempty"`
	UTMMedium   string    `json:"utm_medium,omitempty"`
	UTMCampaign string    `json:"utm_campaign,omitempty"`
	UTMTerm     string    `json:"utm_term,omitempty"`
	UTMContent  string    `json:"utm_content,omitempty"`
	Referrer    string    `json:"referrer,omitempty"`
	LandedAt    time.Time `json:"landed_at,omitzero"`
}

// SiteDomainInfo is the domain-binding view of a site. Kept separate from the
// shared Site struct so existing SELECTs stay untouched (low blast radius).
type SiteDomainInfo struct {
	SiteID     string
	UserID     string
	Name       string
	Domain     string
	Status     string
	LastError  string
	VerifiedAt sql.NullTime
	BoundAt    sql.NullTime
	Handle     string
	// PreviousDomain is the site's earlier proven address, still served while
	// Domain is pending (cleared once Domain is verified).
	PreviousDomain string
	// CertStatus is pending | issuing | live | failed ("" = not known yet).
	CertStatus   string
	FailingSince sql.NullTime
	// Token is the site's ownership token: the value of the DNS TXT record
	// _simple-host.<domain> that proves a custom domain is the owner's.
	Token string
	// ProofExempt: Domain was verified before the TXT proof existed and keeps
	// working without it while it stays verified.
	ProofExempt bool
}

func (info SiteDomainInfo) AsPrevious(now time.Time) SiteDomainInfo {
	prev := info
	prev.Domain = info.PreviousDomain
	prev.Status = "active"
	prev.LastError = ""
	prev.CertStatus = "live"
	prev.FailingSince = sql.NullTime{}
	if !prev.VerifiedAt.Valid {
		prev.VerifiedAt = sql.NullTime{Time: now, Valid: true}
	}
	prev.PreviousDomain = ""
	return prev
}
