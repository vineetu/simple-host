package model

import (
	"database/sql"
	"time"
)

type OAuthIdentity struct {
	ID             string
	UserID         string
	Provider       string
	ProviderUserID string
	Email          sql.NullString
	EmailVerified  bool
	CreatedAt      time.Time
	UpdatedAt      time.Time
}
type VisitorSession struct {
	SSOPassHash   sql.NullString
	ID            []byte
	UserID        string
	SiteID        string
	Host          string
	CreatedAt     time.Time
	LastSeenAt    time.Time
	ExpiresAt     time.Time
	IdleExpiresAt time.Time
}
