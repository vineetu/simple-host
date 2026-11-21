package model

import (
	"database/sql"
	"time"
)

type EventEntry struct {
	TeamID          string
	EventID         string
	Title           string
	Tagline         string
	Description     string
	VideoURL        string
	CodeURL         string
	Problem         string
	Audience        string
	Tools           []string
	Maturity        string
	HasScreenshot   bool
	ScreenshotType  string
	UpdatedAt       time.Time
	UpdatedByName   string
	SubmittedAt     sql.NullTime
	SubmittedByName string
	SubmitAuto      bool
	SubmitSettledAt sql.NullTime
	ExcludedAt      sql.NullTime
	ExcludedByName  string
	Exists          bool
}

// Status is the participant-facing entry status: "draft" before anything is
// decided, "submitted" once complete and submitted (by the team or the deadline
// sweep), "not_submitted" once the sweep has settled it incomplete.
func (e EventEntry) Status() string {
	switch {
	case e.SubmittedAt.Valid:
		return "submitted"
	case e.SubmitSettledAt.Valid:
		return "not_submitted"
	default:
		return "draft"
	}
}

type EventTrack struct {
	ID        string `json:"id"`
	Slug      string `json:"slug"`
	Name      string `json:"name"`
	Challenge string `json:"challenge"`
	Prize     string `json:"prize"`
}
