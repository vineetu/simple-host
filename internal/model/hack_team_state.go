package model

import (
	"database/sql"
	"time"
)

// TeamWriteState is a shared Hack record.
type TeamWriteState struct {
	TeamID          string
	EventID         string
	EventSlug       string
	TeamSlug        string
	AccountID       string
	Stage           string
	EventTakenDown  bool
	SiteTakenDown   bool
	Deadline        sql.NullTime // effective: the team's own, else the event's
	Now             time.Time    // the database clock when this was read
	PinnedAt        sql.NullTime
	PinnedVersion   sql.NullInt64
	HasOverride     bool
	EventDeadline   sql.NullTime
	TeamDeadlineSet sql.NullTime
}

// MyTeam is a shared Hack record.
type MyTeam struct {
	TeamID, TeamName, TeamSlug, EventSlug, EventTitle string
}

// Frozen reports whether submissions are closed.
func (s TeamWriteState) Frozen() bool {
	return s.Stage == "judging" || s.Stage == "results" || s.Stage == "archived" ||
		(s.Deadline.Valid && !s.Now.Before(s.Deadline.Time))
}

// WriteRefusal returns the reason writes are refused, if any.
func (s TeamWriteState) WriteRefusal() string {
	switch {
	case s.EventTakenDown:
		return "event_taken_down"
	case s.Stage == "archived":
		return "event_closed"
	case s.SiteTakenDown:
		return "team_site_taken_down"
	case s.Frozen():
		return "submissions_closed"
	}
	return ""
}

// TeamKeyInfo is a shared Hack record.
type TeamKeyInfo struct {
	KeyID      string
	Last4      string
	TeamID     string
	CreatedAt  time.Time
	LastUsedAt *time.Time
}
