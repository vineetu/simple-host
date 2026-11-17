package model

import (
	"database/sql"
	"time"
)

type IdleSite struct {
	SiteID       string
	UserID       string
	Name         string
	OwnerEmail   string
	OwnerHandle  string
	LastActivity time.Time
	WarnedAt     sql.NullTime
	RemoveAt     sql.NullTime // the removal date the warning email gave
}
