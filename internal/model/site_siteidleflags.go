package model

import (
	"database/sql"
	"time"
)

type SiteIdleFlags struct {
	Keep     bool
	WarnedAt sql.NullTime
	RemoveAt sql.NullTime // the removal date the warning gave
}

func (f SiteIdleFlags) RemovalAt(grace time.Duration) time.Time {
	switch {
	case !f.WarnedAt.Valid:
		return time.Time{}
	case f.RemoveAt.Valid:
		return f.RemoveAt.Time
	}
	return f.WarnedAt.Time.Add(grace)
}
