package model

import (
	"database/sql"
	"time"
)

// Event is one row of events (db/migrations/hack1-events.sql).
type Event struct {
	ID                   string
	Slug                 string
	AccountID            string
	CreatedBy            sql.NullString
	Title                string
	Stage                string
	OrganiserName        string
	Organisation         string
	ContactEmail         string
	Purpose              string
	ExpectedParticipants sql.NullInt64
	Tagline              string
	WebsiteMode          string
	IconMediaType        string
	AccentColor          string
	About                string
	Rules                string
	Prizes               string
	CocText              string
	TimeZone             string
	StartsAt             sql.NullTime
	EndsAt               sql.NullTime
	TeamFinding          bool
	TeamSizeMax          int
	JoinCode             string
	JudgeCode            string
	SubmissionDeadline   sql.NullTime
	ResultsVisibility    string
	ResultsPublishedAt   sql.NullTime
	ClosedAt             sql.NullTime
	RemovalWarnedAt      sql.NullTime
	SitesRemovedAt       sql.NullTime
	KeepSites            bool
	TakenDownAt          sql.NullTime
	TakenDownReason      string
	CreatedAt            time.Time
	UpdatedAt            time.Time
	// M2 (hack2-team-sites.sql): which entry fields are required, and
	// whether the public gallery is open.
	EntryRequired []string
	GalleryOpen   bool
	// M3 (hack3-judging.sql): how judges are spread, and the score lock.
	// JudgingLockReason is cleared on unlock; the reason itself is logged.
	JudgeAssignmentMode string
	JudgesPerTeam       int
	JudgingLockedAt     sql.NullTime
	JudgingLockReason   string
	ScoreMode           string
	TieCriterionID      sql.NullString
	PublicScores        bool
	PublicRanks         bool
	// Applications without a join link (zp-hack-applications.sql). On by
	// default; the organiser's Settings switch.
	ApplicationsOpen bool
	// Listed in Find a hackathon (Results settings). Applications are taken
	// only from listed events (owner, 2026-10-31).
	DirectoryListed bool
	// zv-hack-entry-summary.sql: the entry rules engine. EntryRules is
	// {"v":1,"fields":{field: mode}}, validated and expanded server-side
	// (internal/rules/hackentries/entries.go) -- never stored as the client
	// sent it. JudgeFirst is which evidence judges see first.
	EntryPreset string
	EntryRules  []byte
	JudgeFirst  string
	// 0005-hack-entry-rules-chosen.sql (SIM-382): whether an organiser has
	// ever saved entry rules on this event. New events start false, with
	// the default preset's fields already in place (so entries can be
	// filled before the organiser visits the setting) -- false only drives
	// the Overview "Choose what teams hand in" first step. Every event
	// that existed before this column was added counts as already chosen.
	// Any real PutRules save (including saving the unchanged default) sets
	// it true for good; it never goes back to false.
	EntryRulesChosen bool
	// 0003-hack-notices-judges-messages.sql: minutes before a deadline, 0..5
	// entries each 15..20160, distinct, stored sorted descending. Drives both
	// entry reminders (participants) and judging reminders (judges).
	// int64, not int: lib/pq's array Scan only knows a fixed set of element
	// types, int64 among them.
	ReminderOffsets []int64
	// When judging closes, set like the event's other times. NULL means
	// judges get no reminder.
	JudgingEndsAt sql.NullTime
}

// EventMember is one row of event_members.
type EventMember struct {
	EventID       string
	UserID        string
	Role          string
	DisplayName   string
	TeamID        sql.NullString
	CocAcceptedAt sql.NullTime
	JoinedAt      time.Time
}

// EventTeam is one row of event_teams.
type EventTeam struct {
	ID        string
	EventID   string
	Slug      string
	Name      string
	Code      string
	CreatedBy sql.NullString
	CreatedAt time.Time
	// M2 (hack2-team-sites.sql).
	DeadlineOverride    sql.NullTime
	PinnedVersion       sql.NullInt64
	PinnedAt            sql.NullTime
	SiteTakenDownAt     sql.NullTime
	SiteTakenDownReason string
	// zr-hack-team-kept-after-leave.sql: kept empty, locked for good.
	KeptAfterLeave bool
	// zt-hack-teams-redesign.sql: who locked the team, and when.
	LockedAt sql.NullTime
	LockedBy sql.NullString
}

// AdminEvent is one row of GET /v1/admin/hack/events.
type AdminEvent struct {
	Event
	CreatorEmail string
	Participants int
	Teams        int
	Judges       int
}

// EventPerson is a member as shown in event people lists.
type EventPerson struct {
	UserID         string
	Email          string
	DisplayName    string
	Role           string
	TeamID         sql.NullString
	TeamSlug       sql.NullString
	TeamName       sql.NullString
	JoinedAt       time.Time
	CocAcceptedAt  sql.NullTime
	ApprovalStatus string
	// zp-hack-applications.sql. JoinedVia is on every EventPerson row
	// (GET /people and the participants CSV); the profile fields are read
	// only where ListEventPeople is used, since the CSV needs them (D3:
	// they never reach GET /people's JSON).
	JoinedVia string
	City      string
	Country   string
	AgeRange  string
	Gender    string
	// zt-hack-teams-redesign.sql. Read by ListTeamPeople and ListEventPeople
	// (the organiser team list groups the latter by team); listPeople's own
	// JSON does not expose it, since GET /people is not a team view.
	Admin bool
}

// TakenDown reports whether the platform admin has taken the event down.
func (e Event) TakenDown() bool { return e.TakenDownAt.Valid }

// Locked reports whether the team currently takes nobody new.
func (t EventTeam) Locked() bool { return t.LockedAt.Valid }

// SignupQuestion is a registration question.
type SignupQuestion struct {
	ID       string `json:"id"`
	Prompt   string `json:"prompt"`
	Required bool   `json:"required"`
}

// SignupAnswer is an answer to a registration question.
type SignupAnswer struct {
	ID     string `json:"id"`
	Prompt string `json:"prompt"`
	Answer string `json:"answer"`
}

// RegistrationApplication is a participant application.
type RegistrationApplication struct {
	UserID      string         `json:"user_id"`
	Email       string         `json:"email"`
	DisplayName string         `json:"display_name"`
	Status      string         `json:"status"`
	Answers     []SignupAnswer `json:"answers"`
	JoinedAt    time.Time      `json:"joined_at"`
	// zp-hack-applications.sql.
	JoinedVia string `json:"joined_via"`
	City      string `json:"city"`
	Country   string `json:"country"`
	AgeRange  string `json:"age_range"`
	Gender    string `json:"gender"`
}

// ApplicantProfile contains optional application profile fields.
type ApplicantProfile struct {
	City, Country, AgeRange, Gender string
}

// MemberStatus records a membership’s registration status.
type MemberStatus struct {
	Role, ApprovalStatus, JoinedVia string
}

// EventStorageUsage counts an event account’s stored data.
type EventStorageUsage struct {
	Sites           int64 `json:"sites"`
	Versions        int64 `json:"versions"`
	SavedDataBytes  int64 `json:"saved_data_bytes"`
	HistoryBytes    int64 `json:"history_bytes"`
	ScreenshotBytes int64 `json:"screenshot_bytes"`
	FileBytes       int64 `json:"file_bytes"`
}

// TeamRequest records a request or invitation to join a team.
type TeamRequest struct {
	ID          string    `json:"id"`
	TeamID      string    `json:"team_id"`
	Team        string    `json:"team"`
	TeamName    string    `json:"team_name"`
	UserID      string    `json:"user_id"`
	DisplayName string    `json:"display_name"`
	Kind        string    `json:"kind"`
	CreatedAt   time.Time `json:"created_at"`
}

// FillPlacement describes a proposed team placement.
type FillPlacement struct {
	Team    string       `json:"team"`
	Name    string       `json:"name"`
	New     bool         `json:"new"`
	Members []FillPerson `json:"members"`
}

// FillPerson identifies a participant in a fill plan.
type FillPerson struct {
	UserID      string `json:"user_id"`
	DisplayName string `json:"display_name"`
}

// FillSkippedTeam is a team with room the planner deliberately passed over.
// Reported back so the preview can explain the skip ("Beta Builders isn't
// taking new people, so it was skipped.") instead of silently starting a
// new one-person team while that team still has room. Teams skipped because
// they're already at the target size need no explanation, so those aren't
// reported here.
type FillSkippedTeam struct {
	Team   string `json:"team"`
	Name   string `json:"name"`
	Reason string `json:"reason"` // currently always "locked"
}

// OtherOrganiser is one organiser of an event, besides whoever asked, as
// GET/POST .../leave shows them: successors (creator case) and
// organisers_staying (co_organiser case).
type OtherOrganiser struct {
	UserID, DisplayName, Email string
}

// FormerJudge is one event_former_judges row, for the judging dashboard.
type FormerJudge struct {
	UserID, DisplayName, Role string
	LeftAt                    time.Time
}
