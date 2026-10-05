package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/lib/pq"
)

// Sentinel errors for hosted events. Handlers map these to the HTTP contract.
var (
	ErrHackNameTaken             = errors.New("name taken")
	ErrHackTeamNotFound          = errors.New("team not found")
	ErrHackTeamFull              = errors.New("team full")
	ErrHackAlreadyInTeam         = errors.New("already in team")
	ErrHackCannotRemoveOrganiser = errors.New("cannot remove organiser")
	ErrHackNotParticipant        = errors.New("not a participant")
	ErrHackTeamFrozen            = errors.New("team past its deadline")
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
}

// TakenDown reports whether the platform admin has taken the event down.
func (e Event) TakenDown() bool { return e.TakenDownAt.Valid }

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
}

const eventColumns = `
	id, slug, account_id, created_by, title, stage,
	organiser_name, organisation, contact_email, purpose, expected_participants,
	tagline, website_mode, icon_media_type, accent_color, about, rules, prizes, coc_text, time_zone, starts_at, ends_at,
	team_size_max, join_code, judge_code, submission_deadline, results_visibility,
	results_published_at, closed_at, removal_warned_at, sites_removed_at, keep_sites,
	taken_down_at, taken_down_reason, created_at, updated_at,
	entry_required, gallery_open,
	judge_assignment_mode, judges_per_team, judging_locked_at, judging_lock_reason,
	score_mode, tie_criterion_id, public_scores, public_ranks`

// scanEventFields is every events column in eventColumns order.
func scanEventFields(e *Event) []any {
	return []any{
		&e.ID, &e.Slug, &e.AccountID, &e.CreatedBy, &e.Title, &e.Stage,
		&e.OrganiserName, &e.Organisation, &e.ContactEmail, &e.Purpose, &e.ExpectedParticipants,
		&e.Tagline, &e.WebsiteMode, &e.IconMediaType, &e.AccentColor, &e.About, &e.Rules, &e.Prizes, &e.CocText, &e.TimeZone, &e.StartsAt, &e.EndsAt,
		&e.TeamSizeMax, &e.JoinCode, &e.JudgeCode, &e.SubmissionDeadline, &e.ResultsVisibility,
		&e.ResultsPublishedAt, &e.ClosedAt, &e.RemovalWarnedAt, &e.SitesRemovedAt, &e.KeepSites,
		&e.TakenDownAt, &e.TakenDownReason, &e.CreatedAt, &e.UpdatedAt,
		pq.Array(&e.EntryRequired), &e.GalleryOpen,
		&e.JudgeAssignmentMode, &e.JudgesPerTeam, &e.JudgingLockedAt, &e.JudgingLockReason,
		&e.ScoreMode, &e.TieCriterionID, &e.PublicScores, &e.PublicRanks,
	}
}

func scanEvent(row *sql.Row) (Event, error) {
	var e Event
	err := row.Scan(scanEventFields(&e)...)
	return e, err
}

func queryContext(ctx context.Context, q Querier, query string, args ...any) (*sql.Rows, error) {
	switch t := q.(type) {
	case *sql.DB:
		return t.QueryContext(ctx, query, args...)
	case *sql.Tx:
		return t.QueryContext(ctx, query, args...)
	default:
		return nil, fmt.Errorf("db: querier does not support QueryContext")
	}
}

// NewUUID asks Postgres for a fresh UUID.
func NewUUID(ctx context.Context, q Querier) (string, error) {
	var id string
	err := q.QueryRowContext(ctx, `SELECT gen_random_uuid()`).Scan(&id)
	return id, err
}

// GetEventBySlug loads an event by its slug.
func GetEventBySlug(ctx context.Context, q Querier, slug string) (Event, error) {
	return scanEvent(q.QueryRowContext(ctx, `SELECT `+eventColumns+` FROM events WHERE slug = $1`, slug))
}

// GetEventByAccount loads the event held by the given users row.
func GetEventByAccount(ctx context.Context, q Querier, accountID string) (Event, error) {
	return scanEvent(q.QueryRowContext(ctx, `SELECT `+eventColumns+` FROM events WHERE account_id = $1`, accountID))
}

// SetEventWebsiteMode switches the event host between the built-in and custom page.
func SetEventWebsiteMode(ctx context.Context, q Querier, eventID, organiserID, mode string) (Event, error) {
	return scanEvent(q.QueryRowContext(ctx, `UPDATE events SET website_mode = $2, updated_at = now()
		WHERE id = $1 AND stage <> 'archived' AND taken_down_at IS NULL
		AND EXISTS (SELECT 1 FROM event_members m WHERE m.event_id = events.id AND m.user_id = $3 AND m.role = 'organiser')
		RETURNING `+eventColumns, eventID, mode, organiserID))
}

// GetEventByJoinCode looks up an event by its participant join code (already normalised).
func GetEventByJoinCode(ctx context.Context, q Querier, code string) (Event, error) {
	return scanEvent(q.QueryRowContext(ctx, `SELECT `+eventColumns+` FROM events WHERE join_code = $1`, code))
}

// GetEventByJudgeCode looks up an event by its judge code (already normalised).
func GetEventByJudgeCode(ctx context.Context, q Querier, code string) (Event, error) {
	return scanEvent(q.QueryRowContext(ctx, `SELECT `+eventColumns+` FROM events WHERE judge_code = $1`, code))
}

// CountEventMembers returns how many participants, teams and judges an event has.
func CountEventMembers(ctx context.Context, q Querier, eventID string) (participants, teams, judges int, err error) {
	err = q.QueryRowContext(ctx, `
		SELECT
			(SELECT COUNT(*) FROM event_members WHERE event_id = $1 AND role = 'participant' AND approval_status = 'approved'),
			(SELECT COUNT(*) FROM event_teams WHERE event_id = $1),
			(SELECT COUNT(*) FROM event_members WHERE event_id = $1 AND role = 'judge')`,
		eventID).Scan(&participants, &teams, &judges)
	return
}

// CountEligibleJudges includes organisers, who may judge while keeping their role.
func CountEligibleJudges(ctx context.Context, q Querier, eventID string) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM event_members WHERE event_id = $1 AND role IN ('judge', 'organiser')`, eventID).Scan(&n)
	return n, err
}

// CountOnNoTeam is participants of the event with no team.
func CountOnNoTeam(ctx context.Context, q Querier, eventID string) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM event_members
		 WHERE event_id = $1 AND role = 'participant' AND approval_status='approved' AND team_id IS NULL`, eventID).Scan(&n)
	return n, err
}

// EventSlugTaken reports whether an events row already uses slug.
func EventSlugTaken(ctx context.Context, q Querier, slug string) (bool, error) {
	var taken bool
	// A name that had a participant, judge or team stays taken after its event is gone
	// (event_used_names): its team origins never pass to someone else.
	err := q.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM events WHERE slug = $1)
		    OR EXISTS (SELECT 1 FROM event_used_names WHERE event_slug = $1)`, slug).Scan(&taken)
	return taken, err
}

// CountEventCreatesSince is EVENT_CREATE_PER_DAY: rows in event_create_log for user since t.
func CountEventCreatesSince(ctx context.Context, q Querier, userID string, since time.Time) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM event_create_log WHERE user_id = $1 AND created_at > $2`,
		userID, since).Scan(&n)
	return n, err
}

// CountActiveEventsByOrganiser is EVENT_MAX_ACTIVE_PER_ORGANISER: events they created that are not archived.
func CountActiveEventsByOrganiser(ctx context.Context, q Querier, userID string) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM events WHERE created_by = $1 AND stage <> 'archived'`, userID).Scan(&n)
	return n, err
}

// LockUserRow takes FOR UPDATE on a users row so create limits serialise.
func LockUserRow(ctx context.Context, q Querier, userID string) error {
	var one int
	return q.QueryRowContext(ctx, `SELECT 1 FROM users WHERE id = $1 FOR UPDATE`, userID).Scan(&one)
}

// InsertEventCreateLog records one creation (kept after the event is deleted).
func InsertEventCreateLog(ctx context.Context, q Querier, userID string) error {
	_, err := q.ExecContext(ctx, `INSERT INTO event_create_log (user_id) VALUES ($1)`, userID)
	return err
}

// InsertEvent writes a new events row. Unique violations on slug or codes are
// returned as-is so the caller can retry codes or map a slug clash to name_taken.
func InsertEvent(ctx context.Context, q Querier, e Event) (Event, error) {
	row := q.QueryRowContext(ctx, `
		INSERT INTO events (
			id, slug, account_id, created_by, title, stage,
			organiser_name, organisation, contact_email, purpose, expected_participants,
			tagline, about, rules, prizes, coc_text, time_zone, starts_at, ends_at,
			team_size_max, join_code, judge_code
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, $8, $9, $10, $11,
			$12, $13, $14, $15, $16, $17, $18, $19,
			$20, $21, $22
		) RETURNING `+eventColumns,
		e.ID, e.Slug, e.AccountID, e.CreatedBy, e.Title, e.Stage,
		e.OrganiserName, e.Organisation, e.ContactEmail, e.Purpose, nullInt(e.ExpectedParticipants),
		e.Tagline, e.About, e.Rules, e.Prizes, e.CocText, e.TimeZone, nullTime(e.StartsAt), nullTime(e.EndsAt),
		e.TeamSizeMax, e.JoinCode, e.JudgeCode,
	)
	out, err := scanEvent(row)
	if err != nil && isUniqueViolation(err) {
		return Event{}, err
	}
	return out, err
}

func nullTime(t sql.NullTime) any {
	if !t.Valid {
		return nil
	}
	return t.Time
}

func nullInt(n sql.NullInt64) any {
	if !n.Valid {
		return nil
	}
	return n.Int64
}

func nullStr(s sql.NullString) any {
	if !s.Valid {
		return nil
	}
	return s.String
}

// UpdateEventPatch writes the editable fields and bumps updated_at.
func UpdateEventPatch(ctx context.Context, q Querier, e Event) (Event, error) {
	row := q.QueryRowContext(ctx, `
		UPDATE events SET
			title = $2, tagline = $3, about = $4, rules = $5, prizes = $6, coc_text = $7,
			time_zone = $8, starts_at = $9, ends_at = $10, team_size_max = $11,
			organiser_name = $12, organisation = $13, contact_email = $14, purpose = $15,
			expected_participants = $16, accent_color = $17, updated_at = now()
		WHERE id = $1
		RETURNING `+eventColumns,
		e.ID, e.Title, e.Tagline, e.About, e.Rules, e.Prizes, e.CocText,
		e.TimeZone, nullTime(e.StartsAt), nullTime(e.EndsAt), e.TeamSizeMax,
		e.OrganiserName, e.Organisation, e.ContactEmail, e.Purpose, nullInt(e.ExpectedParticipants), e.AccentColor,
	)
	return scanEvent(row)
}

// TeamKeepableSQL over event_teams t and events e: a team that may be
// removed when it empties — its deadline has not passed and nothing is
// pinned. A team past its deadline keeps its submission for judging.
const TeamKeepableSQL = `t.pinned_at IS NULL AND (` + EffectiveDeadlineSQL + ` IS NULL OR ` + EffectiveDeadlineSQL + ` > clock_timestamp())`

// GetEventForUpdate reads the event and locks its row until the transaction
// ends.
func GetEventForUpdate(ctx context.Context, q Querier, eventID string) (Event, error) {
	return scanEvent(q.QueryRowContext(ctx, `SELECT `+eventColumns+` FROM events WHERE id = $1 FOR UPDATE`, eventID))
}

// UpdateEventEntrySettings writes the M2 settings: the submission deadline,
// the required entry fields and whether the gallery is open.
func UpdateEventEntrySettings(ctx context.Context, q Querier, eventID string, deadline sql.NullTime, required []string, gallery bool) (Event, error) {
	if required == nil {
		required = []string{}
	}
	return scanEvent(q.QueryRowContext(ctx, `
		UPDATE events SET submission_deadline = $2, entry_required = $3, gallery_open = $4, updated_at = now()
		 WHERE id = $1 RETURNING `+eventColumns, eventID, nullTime(deadline), pq.Array(required), gallery))
}

// SetEventStage sets stage and, when moving to archived, stamps closed_at.
func SetEventStage(ctx context.Context, q Querier, eventID, stage string) (Event, error) {
	var row *sql.Row
	if stage == "archived" {
		row = q.QueryRowContext(ctx, `
			UPDATE events SET stage = $2, closed_at = COALESCE(closed_at, now()), updated_at = now()
			WHERE id = $1 RETURNING `+eventColumns, eventID, stage)
	} else {
		row = q.QueryRowContext(ctx, `
			UPDATE events SET stage = $2, updated_at = now()
			WHERE id = $1 RETURNING `+eventColumns, eventID, stage)
	}
	return scanEvent(row)
}

// SetEventJoinCode replaces the participant join code.
func SetEventJoinCode(ctx context.Context, q Querier, eventID, code string) (Event, error) {
	return scanEvent(q.QueryRowContext(ctx, `
		UPDATE events SET join_code = $2, updated_at = now() WHERE id = $1 RETURNING `+eventColumns, eventID, code))
}

// SetEventJudgeCode replaces the judge code.
func SetEventJudgeCode(ctx context.Context, q Querier, eventID, code string) (Event, error) {
	return scanEvent(q.QueryRowContext(ctx, `
		UPDATE events SET judge_code = $2, updated_at = now() WHERE id = $1 RETURNING `+eventColumns, eventID, code))
}

// TakeDownEvent stamps the take-down. The caller also suspends the holding account.
func TakeDownEvent(ctx context.Context, q Querier, eventID, reason string) (Event, error) {
	return scanEvent(q.QueryRowContext(ctx, `
		UPDATE events SET taken_down_at = COALESCE(taken_down_at, now()), taken_down_reason = $2, updated_at = now()
		WHERE id = $1 RETURNING `+eventColumns, eventID, reason))
}

// RestoreEvent clears a take-down. The caller also unsuspends the holding account.
func RestoreEvent(ctx context.Context, q Querier, eventID string) (Event, error) {
	return scanEvent(q.QueryRowContext(ctx, `
		UPDATE events SET taken_down_at = NULL, taken_down_reason = '', updated_at = now()
		WHERE id = $1 RETURNING `+eventColumns, eventID))
}

// DeleteEventAndAccount deletes the event (cascading members and teams) then
// the holding account, which frees the handle/slug. One transaction.
func DeleteEventAndAccount(ctx context.Context, q Querier, ev Event) error {
	// Lock order: the event row (as PATCH, stage and extension changes
	// take it), the holding account's sites, then the team rows (as a
	// deploy takes them), and the members' team keys go with them.
	if _, err := q.ExecContext(ctx, `SELECT 1 FROM events WHERE id = $1 FOR UPDATE`, ev.ID); err != nil {
		return err
	}
	// The holding account's site rows before the team rows: an update
	// deploy holds its site row, then its team row.
	if _, err := q.ExecContext(ctx, `SELECT 1 FROM sites WHERE user_id = $1 FOR UPDATE`, ev.AccountID); err != nil {
		return err
	}
	if _, err := q.ExecContext(ctx, `SELECT 1 FROM event_teams WHERE event_id = $1 FOR UPDATE`, ev.ID); err != nil {
		return err
	}
	if _, err := q.ExecContext(ctx, `DELETE FROM api_keys WHERE id IN (SELECT key_id FROM event_team_keys WHERE event_id = $1)`, ev.ID); err != nil {
		return err
	}
	// Preserve names of legacy events with people but no team, too.
	if _, err := q.ExecContext(ctx, `INSERT INTO event_used_names (event_slug, team_slug)
		SELECT $2, '' WHERE EXISTS (SELECT 1 FROM event_members WHERE event_id=$1 AND role <> 'organiser')
		ON CONFLICT DO NOTHING`, ev.ID, ev.Slug); err != nil {
		return err
	}
	if _, err := q.ExecContext(ctx, `DELETE FROM events WHERE id = $1`, ev.ID); err != nil {
		return err
	}
	_, err := q.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, ev.AccountID)
	return err
}

// ListEventsForUser is GET /v1/hack/events: every event the person is in.
func ListEventsForUser(ctx context.Context, q Querier, userID string) ([]Event, []string, error) {
	rows, err := queryContext(ctx, q, `
		SELECT `+prefixedEventColumns("e")+`, m.role
		  FROM event_members m
		  JOIN events e ON e.id = m.event_id
		 WHERE m.user_id = $1
		 ORDER BY e.created_at DESC`, userID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var events []Event
	var roles []string
	for rows.Next() {
		e, role, err := scanEventRole(rows)
		if err != nil {
			return nil, nil, err
		}
		events = append(events, e)
		roles = append(roles, role)
	}
	return events, roles, rows.Err()
}

func prefixedEventColumns(alias string) string {
	parts := strings.Split(strings.ReplaceAll(eventColumns, "\n", " "), ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, alias+"."+p)
	}
	return strings.Join(out, ", ")
}

func scanEventRole(rows *sql.Rows) (Event, string, error) {
	var e Event
	var role string
	dest := append(scanEventFields(&e), &role)
	err := rows.Scan(dest...)
	return e, role, err
}

// AdminEvent is one row of GET /v1/admin/hack/events.
type AdminEvent struct {
	Event
	CreatorEmail string
	Participants int
	Teams        int
	Judges       int
}

// ListAdminEvents is every event with organiser details, counts and creator email.
func ListAdminEvents(ctx context.Context, q Querier) ([]AdminEvent, error) {
	rows, err := queryContext(ctx, q, `
		SELECT `+prefixedEventColumns("e")+`,
		       COALESCE(u.username, ''),
		       (SELECT COUNT(*) FROM event_members m WHERE m.event_id = e.id AND m.role = 'participant'),
		       (SELECT COUNT(*) FROM event_teams t WHERE t.event_id = e.id),
		       (SELECT COUNT(*) FROM event_members m WHERE m.event_id = e.id AND m.role = 'judge')
		  FROM events e
		  LEFT JOIN users u ON u.id = e.created_by
		 ORDER BY e.created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AdminEvent
	for rows.Next() {
		var a AdminEvent
		dest := append(scanEventFields(&a.Event), &a.CreatorEmail, &a.Participants, &a.Teams, &a.Judges)
		err := rows.Scan(dest...)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// GetEventMember loads one membership. sql.ErrNoRows if none.
func GetEventMember(ctx context.Context, q Querier, eventID, userID string) (EventMember, error) {
	return scanMember(q.QueryRowContext(ctx, `
		SELECT event_id, user_id, role, display_name, team_id, coc_accepted_at, joined_at
		  FROM event_members WHERE event_id = $1 AND user_id = $2`, eventID, userID))
}

func scanMember(row *sql.Row) (EventMember, error) {
	var m EventMember
	err := row.Scan(&m.EventID, &m.UserID, &m.Role, &m.DisplayName, &m.TeamID, &m.CocAcceptedAt, &m.JoinedAt)
	return m, err
}

func scanMemberRow(rows *sql.Rows) (EventMember, error) {
	var m EventMember
	err := rows.Scan(&m.EventID, &m.UserID, &m.Role, &m.DisplayName, &m.TeamID, &m.CocAcceptedAt, &m.JoinedAt)
	return m, err
}

// InsertEventMember adds a person to an event. Unique (event, user) is already_member.
func InsertEventMember(ctx context.Context, q Querier, eventID, userID, role, displayName string) (EventMember, error) {
	row := q.QueryRowContext(ctx, `
		WITH joined AS (
		INSERT INTO event_members (event_id, user_id, role, display_name, coc_accepted_at)
		VALUES ($1, $2, $3, $4, now())
		RETURNING event_id, user_id, role, display_name, team_id, coc_accepted_at, joined_at
		), reserved AS (
		INSERT INTO event_used_names (event_slug, team_slug)
		SELECT e.slug, '' FROM events e JOIN joined j ON j.event_id=e.id WHERE j.role <> 'organiser'
		ON CONFLICT DO NOTHING
		) SELECT * FROM joined`,
		eventID, userID, role, displayName)
	m, err := scanMember(row)
	if err != nil && isUniqueViolation(err) {
		return EventMember{}, err
	}
	return m, err
}

// EventPerson is a member as the organiser people/teams lists show them.
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
}

// ListEventPeople is GET /v1/hack/events/{slug}/people.
func ListEventPeople(ctx context.Context, q Querier, eventID string) ([]EventPerson, error) {
	rows, err := queryContext(ctx, q, `
		SELECT m.user_id, u.username, m.display_name, m.role, m.team_id,
		       t.slug, t.name, m.joined_at, m.coc_accepted_at, m.approval_status
		  FROM event_members m
		  JOIN users u ON u.id = m.user_id
		  LEFT JOIN event_teams t ON t.id = m.team_id
		 WHERE m.event_id = $1
		 ORDER BY m.joined_at ASC`, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EventPerson
	for rows.Next() {
		var p EventPerson
		if err := rows.Scan(&p.UserID, &p.Email, &p.DisplayName, &p.Role, &p.TeamID,
			&p.TeamSlug, &p.TeamName, &p.JoinedAt, &p.CocAcceptedAt, &p.ApprovalStatus); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ListParticipantsOnNoTeam is the organiser teams view's unteamed people.
func ListParticipantsOnNoTeam(ctx context.Context, q Querier, eventID string) ([]EventPerson, error) {
	rows, err := queryContext(ctx, q, `
		SELECT m.user_id, u.username, m.display_name, m.role, m.team_id,
		       NULL, NULL, m.joined_at, m.coc_accepted_at, m.approval_status
		  FROM event_members m
		  JOIN users u ON u.id = m.user_id
		 WHERE m.event_id = $1 AND m.role = 'participant' AND m.approval_status='approved' AND m.team_id IS NULL
		 ORDER BY m.joined_at ASC`, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EventPerson
	for rows.Next() {
		var p EventPerson
		if err := rows.Scan(&p.UserID, &p.Email, &p.DisplayName, &p.Role, &p.TeamID,
			&p.TeamSlug, &p.TeamName, &p.JoinedAt, &p.CocAcceptedAt, &p.ApprovalStatus); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ListEventTeams is every team of the event, oldest first.
func ListEventTeams(ctx context.Context, q Querier, eventID string) ([]EventTeam, error) {
	rows, err := queryContext(ctx, q, `
		SELECT id, event_id, slug, name, code, created_by, created_at,
		deadline_override, pinned_version, pinned_at, site_taken_down_at, site_taken_down_reason
		  FROM event_teams WHERE event_id = $1 ORDER BY created_at ASC`, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EventTeam
	for rows.Next() {
		t, err := scanTeamRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func scanTeam(row *sql.Row) (EventTeam, error) {
	var t EventTeam
	err := row.Scan(&t.ID, &t.EventID, &t.Slug, &t.Name, &t.Code, &t.CreatedBy, &t.CreatedAt,
		&t.DeadlineOverride, &t.PinnedVersion, &t.PinnedAt, &t.SiteTakenDownAt, &t.SiteTakenDownReason)
	return t, err
}

func scanTeamRow(rows *sql.Rows) (EventTeam, error) {
	var t EventTeam
	err := rows.Scan(&t.ID, &t.EventID, &t.Slug, &t.Name, &t.Code, &t.CreatedBy, &t.CreatedAt,
		&t.DeadlineOverride, &t.PinnedVersion, &t.PinnedAt, &t.SiteTakenDownAt, &t.SiteTakenDownReason)
	return t, err
}

// GetEventTeamByID loads a team by primary key.
func GetEventTeamByID(ctx context.Context, q Querier, teamID string) (EventTeam, error) {
	return scanTeam(q.QueryRowContext(ctx, `
		SELECT id, event_id, slug, name, code, created_by, created_at,
		deadline_override, pinned_version, pinned_at, site_taken_down_at, site_taken_down_reason
		  FROM event_teams WHERE id = $1`, teamID))
}

// GetEventTeamBySlug loads a team of an event by its slug.
func GetEventTeamBySlug(ctx context.Context, q Querier, eventID, slug string) (EventTeam, error) {
	return scanTeam(q.QueryRowContext(ctx, `
		SELECT id, event_id, slug, name, code, created_by, created_at,
		deadline_override, pinned_version, pinned_at, site_taken_down_at, site_taken_down_reason
		  FROM event_teams WHERE event_id = $1 AND slug = $2`, eventID, slug))
}

// GetEventTeamByCode loads a team of an event by its (normalised) join code.
func GetEventTeamByCode(ctx context.Context, q Querier, eventID, code string) (EventTeam, error) {
	return scanTeam(q.QueryRowContext(ctx, `
		SELECT id, event_id, slug, name, code, created_by, created_at,
		deadline_override, pinned_version, pinned_at, site_taken_down_at, site_taken_down_reason
		  FROM event_teams WHERE event_id = $1 AND code = $2`, eventID, code))
}

// ListTeamPeople is the members of one team as the organiser sees them.
func ListTeamPeople(ctx context.Context, q Querier, teamID string) ([]EventPerson, error) {
	rows, err := queryContext(ctx, q, `
		SELECT m.user_id, u.username, m.display_name, m.role, m.team_id,
		       NULL, NULL, m.joined_at, m.coc_accepted_at, m.approval_status
		  FROM event_members m
		  JOIN users u ON u.id = m.user_id
		 WHERE m.team_id = $1
		 ORDER BY m.joined_at ASC`, teamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EventPerson
	for rows.Next() {
		var p EventPerson
		if err := rows.Scan(&p.UserID, &p.Email, &p.DisplayName, &p.Role, &p.TeamID,
			&p.TeamSlug, &p.TeamName, &p.JoinedAt, &p.CocAcceptedAt, &p.ApprovalStatus); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// CountTeamMembers is how many people are on a team.
func CountTeamMembers(ctx context.Context, q Querier, teamID string) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM event_members WHERE team_id = $1`, teamID).Scan(&n)
	return n, err
}

// TeamSlugExists reports whether the event already has a team with slug.
func TeamSlugExists(ctx context.Context, q Querier, eventID, slug string) (bool, error) {
	var taken bool
	err := q.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM event_teams WHERE event_id = $1 AND slug = $2)`, eventID, slug).Scan(&taken)
	return taken, err
}

// InsertEventTeam writes a team. Unique on (event, slug) or code is returned as-is.
func InsertEventTeam(ctx context.Context, q Querier, eventID, slug, name, code, createdBy string) (EventTeam, error) {
	return scanTeam(q.QueryRowContext(ctx, `
		INSERT INTO event_teams (event_id, slug, name, code, created_by)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, event_id, slug, name, code, created_by, created_at,
		deadline_override, pinned_version, pinned_at, site_taken_down_at, site_taken_down_reason`,
		eventID, slug, name, code, createdBy))
}

// DeleteEventTeam removes the team. Members' team_id becomes NULL (ON DELETE SET NULL).
func DeleteEventTeam(ctx context.Context, q Querier, teamID string) error {
	_, err := q.ExecContext(ctx, `DELETE FROM event_teams WHERE id = $1`, teamID)
	return err
}

// AccountKeyCount is how many API keys the account holds (holding accounts must be 0).
func AccountKeyCount(ctx context.Context, q Querier, userID string) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM api_keys WHERE user_id = $1`, userID).Scan(&n)
	return n, err
}

// EventAccountFlag is users.event_account for the holding account.
func EventAccountFlag(ctx context.Context, q Querier, userID string) (bool, error) {
	var v bool
	err := q.QueryRowContext(ctx, `SELECT event_account FROM users WHERE id = $1`, userID).Scan(&v)
	return v, err
}

const memberForUpdate = `
	SELECT event_id, user_id, role, display_name, team_id, coc_accepted_at, joined_at
	  FROM event_members WHERE event_id = $1 AND user_id = $2 FOR UPDATE`

const teamForUpdate = `
	SELECT id, event_id, slug, name, code, created_by, created_at,
		deadline_override, pinned_version, pinned_at, site_taken_down_at, site_taken_down_reason
	  FROM event_teams WHERE id = $1 FOR UPDATE`

func lockMember(ctx context.Context, q Querier, eventID, userID string) (EventMember, error) {
	return scanMember(q.QueryRowContext(ctx, memberForUpdate, eventID, userID))
}

func lockTeam(ctx context.Context, q Querier, teamID string) (EventTeam, error) {
	return scanTeam(q.QueryRowContext(ctx, teamForUpdate, teamID))
}

func lockTeamByCode(ctx context.Context, q Querier, eventID, code string) (EventTeam, error) {
	return scanTeam(q.QueryRowContext(ctx, `
		SELECT id, event_id, slug, name, code, created_by, created_at,
		deadline_override, pinned_version, pinned_at, site_taken_down_at, site_taken_down_reason
		  FROM event_teams WHERE event_id = $1 AND code = $2 FOR UPDATE`, eventID, code))
}

func lockTeamBySlug(ctx context.Context, q Querier, eventID, slug string) (EventTeam, error) {
	return scanTeam(q.QueryRowContext(ctx, `
		SELECT id, event_id, slug, name, code, created_by, created_at,
		deadline_override, pinned_version, pinned_at, site_taken_down_at, site_taken_down_reason
		  FROM event_teams WHERE event_id = $1 AND slug = $2 FOR UPDATE`, eventID, slug))
}

func setMemberTeam(ctx context.Context, q Querier, eventID, userID string, teamID sql.NullString) error {
	_, err := q.ExecContext(ctx, `
		UPDATE event_members SET team_id = $3 WHERE event_id = $1 AND user_id = $2`,
		eventID, userID, nullStr(teamID))
	return err
}

func deleteTeamIfEmpty(ctx context.Context, q Querier, teamID string) error {
	n, err := CountTeamMembers(ctx, q, teamID)
	if err != nil {
		return err
	}
	if n != 0 {
		return nil
	}
	// A team whose deadline has passed (pinned or about to be) stays, empty: its submission
	// is kept for judging (the organiser can still remove it).
	_, err = q.ExecContext(ctx, `
		DELETE FROM event_teams t USING events e
		 WHERE t.id = $1 AND e.id = t.event_id AND `+TeamKeepableSQL, teamID)
	return err
}

// CreateTeamAndJoin inserts a team and puts the participant on it. The member
// row is locked so a concurrent join cannot land them on two teams.
func CreateTeamAndJoin(ctx context.Context, q Querier, eventID, userID, slug, name, code string) (EventTeam, error) {
	m, err := lockMember(ctx, q, eventID, userID)
	if err != nil {
		return EventTeam{}, err
	}
	if m.Role != "participant" {
		return EventTeam{}, ErrHackNotParticipant
	}
	if m.TeamID.Valid {
		return EventTeam{}, ErrHackAlreadyInTeam
	}
	team, err := InsertEventTeam(ctx, q, eventID, slug, name, code, userID)
	if err != nil {
		return EventTeam{}, err
	}
	if err := setMemberTeam(ctx, q, eventID, userID, sql.NullString{String: team.ID, Valid: true}); err != nil {
		return EventTeam{}, err
	}
	return team, nil
}

// JoinTeamByCode puts a participant on the team with code. Same team is a no-op.
// sizeMax is events.team_size_max. The team and member rows are locked.
func JoinTeamByCode(ctx context.Context, q Querier, eventID, userID, code string, sizeMax int) (EventTeam, error) {
	m, err := lockMember(ctx, q, eventID, userID)
	if err != nil {
		return EventTeam{}, err
	}
	if m.Role != "participant" {
		return EventTeam{}, ErrHackNotParticipant
	}
	team, err := lockTeamByCode(ctx, q, eventID, code)
	if errors.Is(err, sql.ErrNoRows) {
		return EventTeam{}, ErrHackTeamNotFound
	}
	if err != nil {
		return EventTeam{}, err
	}
	if m.TeamID.Valid {
		if m.TeamID.String == team.ID {
			return team, nil
		}
		return EventTeam{}, ErrHackAlreadyInTeam
	}
	n, err := CountTeamMembers(ctx, q, team.ID)
	if err != nil {
		return EventTeam{}, err
	}
	if n >= sizeMax {
		return EventTeam{}, ErrHackTeamFull
	}
	if err := setMemberTeam(ctx, q, eventID, userID, sql.NullString{String: team.ID, Valid: true}); err != nil {
		return EventTeam{}, err
	}
	return team, nil
}

// LeaveTeam takes the participant off their team and deletes the team if empty.
func LeaveTeam(ctx context.Context, q Querier, eventID, userID string) error {
	m, err := lockMember(ctx, q, eventID, userID)
	if err != nil {
		return err
	}
	if !m.TeamID.Valid {
		return nil
	}
	if _, err := lockTeam(ctx, q, m.TeamID.String); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	// Past the team's deadline its members stay (checked under the team
	// row's lock): leaving could empty it and lose its submission.
	var frozen bool
	if err := q.QueryRowContext(ctx, `
		SELECT COALESCE(`+EffectiveDeadlineSQL+` <= clock_timestamp(), false)
		  FROM event_teams t JOIN events e ON e.id = t.event_id WHERE t.id = $1`, m.TeamID.String).Scan(&frozen); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if frozen {
		return ErrHackTeamFrozen
	}
	if err := setMemberTeam(ctx, q, eventID, userID, sql.NullString{}); err != nil {
		return err
	}
	return deleteTeamIfEmpty(ctx, q, m.TeamID.String)
}

// MoveParticipantToTeam is the organiser putting a participant on a team
// (from any team or none). sizeMax is events.team_size_max.
func MoveParticipantToTeam(ctx context.Context, q Querier, eventID, userID, teamSlug string, sizeMax int) (EventTeam, error) {
	m, err := lockMember(ctx, q, eventID, userID)
	if err != nil {
		return EventTeam{}, err
	}
	if m.Role != "participant" {
		return EventTeam{}, ErrHackNotParticipant
	}
	status, err := MemberApprovalStatus(ctx, q, eventID, userID)
	if err != nil {
		return EventTeam{}, err
	}
	if status != "approved" {
		return EventTeam{}, ErrHackNotParticipant
	}
	dest, err := GetEventTeamBySlug(ctx, q, eventID, teamSlug)
	if errors.Is(err, sql.ErrNoRows) {
		return EventTeam{}, ErrHackTeamNotFound
	}
	if err != nil {
		return EventTeam{}, err
	}
	if m.TeamID.Valid && m.TeamID.String == dest.ID {
		return dest, nil
	}
	ids := []string{dest.ID}
	if m.TeamID.Valid {
		ids = append(ids, m.TeamID.String)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if _, err := lockTeam(ctx, q, id); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return EventTeam{}, err
		}
	}
	team, err := lockTeamBySlug(ctx, q, eventID, teamSlug)
	if errors.Is(err, sql.ErrNoRows) {
		return EventTeam{}, ErrHackTeamNotFound
	}
	if err != nil {
		return EventTeam{}, err
	}
	old := m.TeamID
	n, err := CountTeamMembers(ctx, q, team.ID)
	if err != nil {
		return EventTeam{}, err
	}
	if n >= sizeMax {
		return EventTeam{}, ErrHackTeamFull
	}
	if err := setMemberTeam(ctx, q, eventID, userID, sql.NullString{String: team.ID, Valid: true}); err != nil {
		return EventTeam{}, err
	}
	if old.Valid && old.String != team.ID {
		if err := deleteTeamIfEmpty(ctx, q, old.String); err != nil {
			return EventTeam{}, err
		}
	}
	return team, nil
}

// RemoveParticipantFromTeam takes a participant off a team; they stay in the event.
func RemoveParticipantFromTeam(ctx context.Context, q Querier, eventID, teamSlug, userID string) error {
	m, err := lockMember(ctx, q, eventID, userID)
	if err != nil {
		return err
	}
	team, err := lockTeamBySlug(ctx, q, eventID, teamSlug)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrHackTeamNotFound
	}
	if err != nil {
		return err
	}
	if !m.TeamID.Valid || m.TeamID.String != team.ID {
		return sql.ErrNoRows
	}
	if err := setMemberTeam(ctx, q, eventID, userID, sql.NullString{}); err != nil {
		return err
	}
	return deleteTeamIfEmpty(ctx, q, team.ID)
}

// RemovePersonFromEvent deletes a participant or judge. Organisers cannot be removed.
// A team left with nobody is deleted.
func RemovePersonFromEvent(ctx context.Context, q Querier, eventID, userID string) error {
	m, err := lockMember(ctx, q, eventID, userID)
	if err != nil {
		return err
	}
	if m.Role == "organiser" {
		return ErrHackCannotRemoveOrganiser
	}
	if m.TeamID.Valid {
		if _, err := lockTeam(ctx, q, m.TeamID.String); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	// An imported member may predate reservation-on-join. Keep their event's
	// name when they leave, even if no team was ever formed.
	if _, err := q.ExecContext(ctx, `INSERT INTO event_used_names (event_slug, team_slug)
		SELECT slug, '' FROM events WHERE id=$1 ON CONFLICT DO NOTHING`, eventID); err != nil {
		return err
	}
	if _, err := q.ExecContext(ctx, `DELETE FROM event_members WHERE event_id = $1 AND user_id = $2`, eventID, userID); err != nil {
		return err
	}
	if m.TeamID.Valid {
		return deleteTeamIfEmpty(ctx, q, m.TeamID.String)
	}
	return nil
}

// UserEmail is users.username for a person (the address they signed in with).
func UserEmail(ctx context.Context, q Querier, userID string) (string, error) {
	var email string
	err := q.QueryRowContext(ctx, `SELECT username FROM users WHERE id = $1`, userID).Scan(&email)
	return email, err
}
