package db

import (
	"database/sql"
	"github.com/vsriram/simple-host/internal/model"
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

type EventWebsiteIdentity struct {
	EventID     string
	EventSlug   string
	OrganiserID string
}

type SiteSummary = model.SiteSummary
type Site = model.Site
type Version = model.Version
