package model

import (
	"database/sql"
	"time"
)

// OAuthState is one in-flight authorization-code flow.
type OAuthState struct {
	State        string
	Provider     string
	CodeVerifier string
	ReturnTo     string
	Host         string
	SiteID       sql.NullString
	Purpose      string
	NonceHash    sql.NullString
	CreatedAt    time.Time
	ExpiresAt    time.Time
	UsedAt       sql.NullTime
}
