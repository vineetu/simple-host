package model

import (
	"database/sql"
	"time"
)

type DeletedSite struct {
	ID           string
	UserID       string
	Name         string
	DeletedAt    time.Time
	CustomDomain sql.NullString
	// PurgeAtStored is the date stored when it was deleted (NULL for sites deleted
	// before the column existed).
	PurgeAtStored sql.NullTime
}

func (d DeletedSite) PurgeAt(retention time.Duration) time.Time {
	if d.PurgeAtStored.Valid {
		return d.PurgeAtStored.Time
	}
	return d.DeletedAt.Add(retention)
}
