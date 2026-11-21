package model

import (
	"database/sql"
	"time"
)

type EventContent struct {
	Sponsors []byte
	FAQ      []byte
	Schedule []byte
}

type EventAnnouncement struct {
	ID         string    `json:"id"`
	Title      string    `json:"title"`
	Body       string    `json:"body"`
	CreatedAt  time.Time `json:"created_at"`
	EmailsSent int64     `json:"emails_sent"`
	// Pinned is true for the one announcement an organiser keeps at the top.
	Pinned bool `json:"pinned"`
	// BodyHTML is Body rendered for display (links, cards, bold, bullets),
	// filled by the service on reads; it is never stored.
	BodyHTML string `json:"body_html"`
}

type EventMail struct {
	Kind, EventID, TeamID, AnnouncementID, Recipient, EventTitle, EventSlug, TeamName, Title, Body string
}

type EntryReminder struct {
	EventID, TeamID string
	Kind            string // an offset label, e.g. "24h", "90m"
	Deadline        time.Time
}

// JudgeReminderDue is one judge of one event due a judging-close reminder now,
// at this offset, not yet claimed for this deadline.
type JudgeReminderDue struct {
	EventID  string
	UserID   string
	Kind     string // an offset label, e.g. "24h", "90m"
	Deadline time.Time
}

// TeamMessage is one event_team_messages row.
type TeamMessage struct {
	ID, EventID, TeamID string
	SenderID            sql.NullString
	SenderName          string
	Body                string
	CreatedAt           time.Time
	FromTeam            bool // written by a member of the team to the organisers
}

type GalleryCard struct {
	ID, Slug, Name, Title, Tagline string
	Tools                          []string
	HasScreenshot                  bool
}

type Notice struct {
	ID, EventID, UserID, Kind, TeamID, RefID, Detail string
	CreatedAt                                        time.Time
}

type PendingJoinRequest struct{ ID, DisplayName string }

type PendingInvite struct{ ID, TeamName string }

type MailBatch struct {
	UserID, Kind string
	Recipient    string // the current sign-in address; "" when they may not be emailed now
	Notices      []MailNotice
}

type MailNotice struct {
	Notice
	EventSlug, EventTitle, EventTimeZone string
}

type HackPreferences struct {
	Theme                    string `json:"theme"`
	OrganiserWalkthroughDone bool   `json:"organiser_walkthrough_done"`
	JudgeWalkthroughDone     bool   `json:"judge_walkthrough_done"`
	// EmailUpdates: "Email me when something waits on me".
	EmailUpdates bool `json:"email_updates"`
}

type DirectoryEvent struct {
	Slug, Title, Tagline, Stage, TimeZone string
	AccentColor                           string
	StartsAt, EndsAt                      sql.NullTime
	// The organiser's switch and whether the platform admin has taken the event
	// down, for the applications_open rule (always false here: the WHERE clause
	// already excludes taken-down events, but the row carries it so the rule is
	// computed the one shared way everywhere).
	ApplicationsOpen bool
	TakenDownAt      sql.NullTime
}

type VotingSettings struct {
	Enabled           bool
	OpensAt, ClosesAt sql.NullTime
	Eligibility       string
	DirectoryListed   bool
}

type VoteOption struct {
	TeamID, Slug, Name, Title, Tagline string
	Count                              int
}

// Error is the one hack workflow refusal: Code is the wire code, and the
// transport takes its status from httpx.RefusalStatus.
type Error struct{ Code, Message string }

func (e *Error) Error() string { return e.Message }
