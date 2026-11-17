package model

import (
	"database/sql"
	"errors"
	"time"
)

// OAuthClient is an app that registered itself (RFC 7591) or was inserted by
// the operator.
type OAuthClient struct {
	ClientID                string
	SecretHash              sql.NullString
	Name                    string
	RedirectURIs            []string
	TokenEndpointAuthMethod string
	PKCERequired            bool
	Dynamic                 bool
	CreatedAt               time.Time
}

// OAuthCode is one authorization code, as stored.
type OAuthCode struct {
	ClientID      string
	UserID        string
	RedirectURI   string
	CodeChallenge string
	Scope         string
	Resource      string
	ExpiresAt     time.Time
	UsedAt        sql.NullTime
	GrantID       sql.NullString
	// Device is the browser the person approved on, summarised ("Chrome on
	// macOS"), for telling connections apart; never the full user agent or
	// an address. Copied onto the grant.
	Device string
}

// OAuthGrant is the connection, independent of its short-lived access tokens.
type OAuthGrant struct {
	SelectedTeamID sql.NullString
	UserID         string
	ClientID       string
	Scope          string
	Resource       string
}

// OAuthTokenGrant is a token row joined to the grant it belongs to.
type OAuthTokenGrant struct {
	Kind           string
	ExpiresAt      time.Time
	UsedAt         sql.NullTime
	GrantID        string
	UserID         string
	ClientID       string
	Scope          string
	Resource       string
	SelectedTeamID sql.NullString
}

// OAuthConnection is one app a person has connected, for the "connected apps"
// list: every live grant for one client folded into a row.
type OAuthConnection struct {
	LastVisitorSite string
	ClientID        string
	ClientName      string
	ConnectedAt     time.Time
	LastUsedAt      time.Time
	// Device is the newest connection's summarised browser ("" when unknown).
	Device string
}

// ErrOAuthCodeUsed is returned when a code has already been redeemed. The
// caller revokes whatever the first redemption issued.
var ErrOAuthCodeUsed = errors.New("authorization code already used")

// OAuthConnectionTeam is a selected publishing destination for an app.
type OAuthConnectionTeam struct {
	Event string `json:"event"`
	Team  string `json:"team"`
}
