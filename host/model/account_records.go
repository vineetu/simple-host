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
	// EventAccount is a Simple Hack event's account (users.event_account).
	// Loaded by GetUserByID and UsersByIDs.
	EventAccount bool
	// Signup is where the account came from (see SetSignup). Loaded only by
	// ListAllUsers, for the admin page.
	Signup Signup
	// Member is set when a person named an account they may act for as the
	// owner of the sites a request acts on (owner=<handle>, auth.ScopeGate):
	// a team they belong to, or an account with a site they edit. This User
	// is then that account, acting for Member.PersonID. It is checked again
	// on every request.
	Member *MemberIdentity
}

// DeployOnly reports the scope of a stored deployment credential.
func (u User) DeployOnly() bool { return u.KeyScope == KeyScopeDeploy }

// MemberIdentity is the person behind a request that acts for a team.
type MemberIdentity struct {
	PersonID     string
	PersonHandle string
	// Site is set when the person is an editor of this one site of the
	// account rather than a member of it.
	Site string
	// Edits lists the sites the person edits, when they list the account's
	// sites as an editor (GET /v1/sites): they see only those.
	Edits []string
}

// Team is a team as its members see it (GET /v1/teams).
type Team struct {
	ID        string    `json:"id"`
	Handle    string    `json:"handle"`
	Members   int       `json:"members"`
	Sites     int       `json:"sites"`
	CreatedAt time.Time `json:"created_at"`
}

// TeamMember is one member of a team (GET /v1/teams/{team}/members). A
// person's sign-in email is never shown to the other members.
type TeamMember struct {
	ID          string     `json:"id"`
	Handle      string     `json:"handle,omitempty"`
	DisplayName string     `json:"display_name,omitempty"`
	AddedBy     string     `json:"added_by,omitempty"`
	AddedAt     *time.Time `json:"added_at,omitempty"`
}

// TeamInvitation is a live invitation to join a team: shown to the team's
// members (with AddressedTo, what the inviter typed) and to the person it is
// addressed to (without it).
type TeamInvitation struct {
	ID          string    `json:"id"`
	Team        string    `json:"team"`
	AddressedTo string    `json:"addressed_to,omitempty"`
	InvitedBy   string    `json:"invited_by"`
	CreatedAt   time.Time `json:"created_at"`
	ExpiresAt   time.Time `json:"expires_at"`
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

type HandleVerdict struct {
	Status  int
	Code    string
	Error   string
	Address string
}
