package model

import (
	"database/sql"
	"time"
)

// EventEntry is one team's entry (event_entries). Exists is false when the
// team has not written one yet: the text is empty and there is no screenshot.
// UpdatedByName and SubmittedByName are the writer's display name in this
// event, or "" when they are no longer a member — never an email or an
// account id.
//
// zv-hack-entry-summary.sql: the summary card (Problem, Audience, Tools,
// Maturity) and the submit state. SubmittedAt/SubmitAuto/SubmitSettledAt are
// the owner's deadline decision (coordinator decision 2026-11-02, INTENT.md):
// a complete draft auto-submits at the deadline; an incomplete one settles
// as "not submitted" but stays judged unless an organiser excludes it by
// hand (ExcludedAt/ExcludedBy). SubmitSettledAt is non-nil exactly when the
// deadline sweep has decided this entry one way or the other; it is cleared
// by an extension, a stage move back to building, or the event deadline
// moving later, so the sweep reconsiders it.
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

// Status is the participant-facing entry status (section 4.1): "draft"
// before anything is decided, "submitted" once complete and submitted
// (by the team or the deadline sweep), "not_submitted" once the sweep has
// settled it incomplete. Excluded is orthogonal (an organiser's hand
// decision on top of either status).
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

// EventTrack describes a track in an event.
type EventTrack struct {
	ID        string `json:"id"`
	Slug      string `json:"slug"`
	Name      string `json:"name"`
	Challenge string `json:"challenge"`
	Prize     string `json:"prize"`
}
