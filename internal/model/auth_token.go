package model

import (
	"database/sql"
	"time"
)

type AuthToken struct {
	Purpose   string
	SiteID    sql.NullString
	ID        string
	Email     string
	Code      string
	LinkToken string
	ExpiresAt time.Time
	UsedAt    sql.NullTime
	Attempts  int
	NonceHash sql.NullString
}
