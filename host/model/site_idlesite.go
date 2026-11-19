package model

import "time"

type IdleSite struct {
	SiteID       string
	UserID       string
	Name         string
	OwnerEmail   string
	OwnerHandle  string
	LastActivity time.Time
}
