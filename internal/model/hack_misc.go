package model

import (
	"database/sql"
	"time"
)

// EventContent is a shared Hack record.
type EventContent struct {
	Sponsors []byte
	FAQ      []byte
	Schedule []byte
}

// EventAnnouncement is a shared Hack record.
type EventAnnouncement struct {
	ID         string    `json:"id"`
	Title      string    `json:"title"`
	Body       string    `json:"body"`
	CreatedAt  time.Time `json:"created_at"`
	EmailsSent int64     `json:"emails_sent"`
}

// EventMail is a shared Hack record.
type EventMail struct {
	Kind, EventID, TeamID, AnnouncementID, Recipient, EventTitle, EventSlug, TeamName, Title, Body string
}

// EntryReminder is a shared Hack record.
type EntryReminder struct {
	EventID, TeamID string
	Kind            string // an offset label, e.g. "24h", "90m"
	Deadline        time.Time
}

// JudgeReminderDue is a shared Hack record: one judge of one event due a
// judging-close reminder now, at this offset, not yet claimed for this
// deadline (0003-hack-notices-judges-messages.sql).
type JudgeReminderDue struct {
	EventID  string
	UserID   string
	Kind     string // an offset label, e.g. "24h", "90m"
	Deadline time.Time
}

// TeamMessage is a shared Hack record: one event_team_messages row
// (0003-hack-notices-judges-messages.sql).
type TeamMessage struct {
	ID, EventID, TeamID string
	SenderID            sql.NullString
	SenderName          string
	Body                string
	CreatedAt           time.Time
	FromTeam            bool // written by a member of the team to the organisers
}

// GalleryCard is a shared Hack record.
type GalleryCard struct {
	ID, Slug, Name, Title, Tagline string
	Tools                          []string
	HasScreenshot                  bool
}

// Notice is a shared Hack record.
type Notice struct {
	ID, EventID, UserID, Kind, TeamID, RefID, Detail string
	CreatedAt                                        time.Time
}

// PendingJoinRequest is a shared Hack record.
type PendingJoinRequest struct{ ID, DisplayName string }

// PendingInvite is a shared Hack record.
type PendingInvite struct{ ID, TeamName string }

// MailBatch is a shared Hack record.
type MailBatch struct {
	UserID, Kind string
	Recipient    string // the current sign-in address; "" when they may not be emailed now
	Notices      []MailNotice
}

// MailNotice is a shared Hack record.
type MailNotice struct {
	Notice
	EventSlug, EventTitle, EventTimeZone string
}

// HackPreferences is a shared Hack record.
type HackPreferences struct {
	Theme                    string `json:"theme"`
	OrganiserWalkthroughDone bool   `json:"organiser_walkthrough_done"`
	JudgeWalkthroughDone     bool   `json:"judge_walkthrough_done"`
	// EmailUpdates: "Email me when something waits on me" (2026-11-02). On
	// by default; nothing is ever emailed to an account without an address.
	EmailUpdates bool `json:"email_updates"`
}

// DirectoryEvent is a shared Hack record.
type DirectoryEvent struct {
	Slug, Title, Tagline, Stage, TimeZone string
	AccentColor                           string
	StartsAt, EndsAt                      sql.NullTime
	// zp-hack-applications.sql: the organiser's switch and whether the
	// platform admin has taken the event down, for the applications_open
	// rule (always false here: the WHERE clause already excludes taken-down
	// events, but the row carries it so the rule is computed the one shared
	// way everywhere it is used).
	ApplicationsOpen bool
	TakenDownAt      sql.NullTime
}

// VotingSettings is a shared Hack record.
type VotingSettings struct {
	Enabled           bool
	OpensAt, ClosesAt sql.NullTime
	Eligibility       string
	DirectoryListed   bool
}

// VoteOption is a shared Hack record.
type VoteOption struct {
	TeamID, Slug, Name, Title, Tagline string
	Count                              int
}
