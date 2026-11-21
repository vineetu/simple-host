package model

import (
	"database/sql"
	"time"
)

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
	// Whole-page style for the public event page; "" is Classic.
	Theme              string
	About              string
	Rules              string
	Prizes             string
	OverallPrizes      []string
	CocText            string
	TimeZone           string
	StartsAt           sql.NullTime
	EndsAt             sql.NullTime
	TeamFinding        bool
	TeamSizeMax        int
	JoinCode           string
	JudgeCode          string
	SubmissionDeadline sql.NullTime
	ResultsVisibility  string
	ResultsPublishedAt sql.NullTime
	ClosedAt           sql.NullTime
	RemovalWarnedAt    sql.NullTime
	SitesRemovedAt     sql.NullTime
	KeepSites          bool
	TakenDownAt        sql.NullTime
	TakenDownReason    string
	CreatedAt          time.Time
	UpdatedAt          time.Time
	// Which entry fields are required, and whether the public gallery is open.
	EntryRequired     []string
	SubmissionsPublic bool
	GalleryOpen       bool
	// How judges are spread, and the score lock.
	OrganiserJudges     []string
	JudgeAssignmentMode string
	JudgesPerTeam       int
	JudgingLockedAt     sql.NullTime
	JudgingLockReason   string
	ScoreMode           string
	TieCriterionID      sql.NullString
	PublicScores        bool
	PublicRanks         bool
	// Applications without a join link (zp-hack-applications.sql).
	ApplicationsOpen bool
	// Listed in Find a hackathon (Results settings).
	DirectoryListed bool
	// zv-hack-entry-summary.sql: the entry rules engine.
	EntryPreset string
	EntryRules  []byte
	JudgeFirst  string
	// Whether an organiser has ever saved entry rules on this event.
	EntryRulesChosen bool
	// 0003-hack-notices-judges-messages.sql: minutes before a deadline, 0..5
	// entries each 15..20160, distinct, stored sorted descending.
	ReminderOffsets []int64
	// When judging closes, set like the event's other times.
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
	ID                  string
	EventID             string
	Slug                string
	Name                string
	Code                string
	CreatedBy           sql.NullString
	CreatedAt           time.Time
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

type AdminEvent struct {
	Event
	CreatorEmail string
	Participants int
	Teams        int
	Judges       int
}

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
	// zp-hack-applications.sql.
	JoinedVia string
	City      string
	Country   string
	AgeRange  string
	Gender    string
	// zt-hack-teams-redesign.sql.
	Admin bool
}

func (e Event) TakenDown() bool { return e.TakenDownAt.Valid }

func (t EventTeam) Locked() bool { return t.LockedAt.Valid }

type SignupQuestion struct {
	ID       string `json:"id"`
	Prompt   string `json:"prompt"`
	Required bool   `json:"required"`
}

type SignupAnswer struct {
	ID     string `json:"id"`
	Prompt string `json:"prompt"`
	Answer string `json:"answer"`
}

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

type ApplicantProfile struct {
	City, Country, AgeRange, Gender string
}

type MemberStatus struct {
	Role, ApprovalStatus, JoinedVia string
}

type EventStorageUsage struct {
	Sites           int64 `json:"sites"`
	Versions        int64 `json:"versions"`
	ScreenshotBytes int64 `json:"screenshot_bytes"`
	FileBytes       int64 `json:"file_bytes"`
}

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

type FillPlacement struct {
	Team    string       `json:"team"`
	Name    string       `json:"name"`
	New     bool         `json:"new"`
	Members []FillPerson `json:"members"`
}

type FillPerson struct {
	UserID      string `json:"user_id"`
	DisplayName string `json:"display_name"`
}

// FillSkippedTeam is a team with room the planner deliberately passed over.
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
