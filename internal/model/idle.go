package model

import (
	"database/sql"
	"time"
)

// IdleLink is the site a cleanup link's token belongs to.
type IdleLink struct {
	SiteID  string
	UserID  string
	Name    string
	Deleted bool
	Removed bool // removed by the cleanup (idle_removed_at set)
	// TakenDown: the operator took the site down or suspended its owner;
	// the links do nothing then.
	TakenDown bool
}

// SiteIdleFlags is a live site's Keep flag and pending warning.
type SiteIdleFlags struct {
	Keep     bool
	WarnedAt sql.NullTime
	RemoveAt sql.NullTime // the removal date the warning gave
}

// RemovalAt is when a warned site moves to Recently deleted: the date the
// warning gave, or the warning plus grace for warnings from before it was
// stored. Zero when not warned.
func (f SiteIdleFlags) RemovalAt(grace time.Duration) time.Time {
	switch {
	case !f.WarnedAt.Valid:
		return time.Time{}
	case f.RemoveAt.Valid:
		return f.RemoveAt.Time
	}
	return f.WarnedAt.Time.Add(grace)
}
