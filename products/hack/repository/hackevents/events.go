package hackevents

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/lib/pq"
	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/sqlkit"
	"github.com/vsriram/simple-host/products/hack/model"
)

type Event = model.Event

type AdminEvent = model.AdminEvent

type EventPerson = model.EventPerson

// EventColumns is the event scan column order.
const EventColumns = `
	id, slug, account_id, created_by, title, stage,
	organiser_name, organisation, contact_email, purpose, expected_participants,
	tagline, website_mode, icon_media_type, accent_color, about, rules, prizes, coc_text, time_zone, starts_at, ends_at,
	team_size_max, join_code, judge_code, submission_deadline, results_visibility,
	results_published_at, closed_at, removal_warned_at, sites_removed_at, keep_sites,
	taken_down_at, taken_down_reason, created_at, updated_at,
	entry_required, gallery_open, submissions_public,
	judge_assignment_mode, judges_per_team, judging_locked_at, judging_lock_reason,
	score_mode, tie_criterion_id, public_scores, public_ranks, team_finding, applications_open, directory_listed,
	entry_preset, entry_rules, judge_first, reminder_offsets, judging_ends_at, entry_rules_chosen, theme, organiser_judges, overall_prizes`

func ScanEventFields(e *Event) []any {
	return []any{
		&e.ID, &e.Slug, &e.AccountID, &e.CreatedBy, &e.Title, &e.Stage,
		&e.OrganiserName, &e.Organisation, &e.ContactEmail, &e.Purpose, &e.ExpectedParticipants,
		&e.Tagline, &e.WebsiteMode, &e.IconMediaType, &e.AccentColor, &e.About, &e.Rules, &e.Prizes, &e.CocText, &e.TimeZone, &e.StartsAt, &e.EndsAt,
		&e.TeamSizeMax, &e.JoinCode, &e.JudgeCode, &e.SubmissionDeadline, &e.ResultsVisibility,
		&e.ResultsPublishedAt, &e.ClosedAt, &e.RemovalWarnedAt, &e.SitesRemovedAt, &e.KeepSites,
		&e.TakenDownAt, &e.TakenDownReason, &e.CreatedAt, &e.UpdatedAt,
		pq.Array(&e.EntryRequired), &e.GalleryOpen, &e.SubmissionsPublic,
		&e.JudgeAssignmentMode, &e.JudgesPerTeam, &e.JudgingLockedAt, &e.JudgingLockReason,
		&e.ScoreMode, &e.TieCriterionID, &e.PublicScores, &e.PublicRanks, &e.TeamFinding, &e.ApplicationsOpen, &e.DirectoryListed,
		&e.EntryPreset, &e.EntryRules, &e.JudgeFirst, pq.Array(&e.ReminderOffsets), &e.JudgingEndsAt, &e.EntryRulesChosen, &e.Theme, pq.Array(&e.OrganiserJudges), pq.Array(&e.OverallPrizes),
	}
}

func ScanEvent(row *sql.Row) (Event, error) {
	var e Event
	err := row.Scan(ScanEventFields(&e)...)
	return e, err
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

func prefixedEventColumns(alias string) string {
	parts := strings.Split(strings.ReplaceAll(EventColumns, "\n", " "), ",")
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

func GetEventBySlug(ctx context.Context, q sqlkit.Querier, slug string) (Event, error) {
	return ScanEvent(q.QueryRowContext(ctx, `SELECT `+EventColumns+` FROM events WHERE slug = $1`, slug))
}

// GetEventByID loads an event by its id, for a route that only has a team's event_id in hand (GET /v1/hack/team-links/{code}).
func GetEventByID(ctx context.Context, q sqlkit.Querier, eventID string) (Event, error) {
	return ScanEvent(q.QueryRowContext(ctx, `SELECT `+EventColumns+` FROM events WHERE id = $1`, eventID))
}

// DemoEventCandidates is the open events of the standing demo's name, newest
// first; the demo is the first an operator organises.
func DemoEventCandidates(ctx context.Context, q sqlkit.Querier, slug string) ([]Event, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+EventColumns+`
		FROM events
		WHERE (slug = $1 OR slug LIKE $1 || '-%') AND taken_down_at IS NULL AND stage IN ('open', 'building')
		ORDER BY created_at DESC`, slug)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(ScanEventFields(&e)...); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

func GetEventByAccount(ctx context.Context, q sqlkit.Querier, accountID string) (Event, error) {
	return ScanEvent(q.QueryRowContext(ctx, `SELECT `+EventColumns+` FROM events WHERE account_id = $1`, accountID))
}

// SetEventWebsiteMode switches a running event's website (the caller checked
// the organiser); sql.ErrNoRows when it has ended or is taken down.
func SetEventWebsiteMode(ctx context.Context, q sqlkit.Querier, eventID, mode string) (Event, error) {
	return ScanEvent(q.QueryRowContext(ctx, `UPDATE events SET website_mode = $2, updated_at = now()
		WHERE id = $1 AND stage <> 'archived' AND taken_down_at IS NULL
		RETURNING `+EventColumns, eventID, mode))
}

func GetEventByJoinCode(ctx context.Context, q sqlkit.Querier, code string) (Event, error) {
	return ScanEvent(q.QueryRowContext(ctx, `SELECT `+EventColumns+` FROM events WHERE join_code = $1`, code))
}

func GetEventByJudgeCode(ctx context.Context, q sqlkit.Querier, code string) (Event, error) {
	return ScanEvent(q.QueryRowContext(ctx, `SELECT `+EventColumns+` FROM events WHERE judge_code = $1`, code))
}

func EventSlugTaken(ctx context.Context, q sqlkit.Querier, slug string) (bool, error) {
	var taken bool
	// A name that had a participant, judge or team stays taken after its event is gone (event_used_names): its team origins never pass to someone else.
	err := q.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM events WHERE slug = $1)
		    OR EXISTS (SELECT 1 FROM event_used_names WHERE event_slug = $1)`, slug).Scan(&taken)
	return taken, err
}

func CountEventCreatesSince(ctx context.Context, q sqlkit.Querier, userID string, since time.Time) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM events WHERE created_by = $1 AND created_at > $2`,
		userID, since).Scan(&n)
	return n, err
}

// CountActiveEventsByOrganiser is EVENT_MAX_ACTIVE_PER_ORGANISER: events they created that are not archived.
func CountActiveEventsByOrganiser(ctx context.Context, q sqlkit.Querier, userID string) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM events WHERE created_by = $1 AND stage <> 'archived'`, userID).Scan(&n)
	return n, err
}

// InsertEventCreateLog records one creation (kept after the event is deleted).
func InsertEventCreateLog(ctx context.Context, q sqlkit.Querier, userID string) error {
	_, err := q.ExecContext(ctx, `INSERT INTO event_create_log (user_id) VALUES ($1)`, userID)
	return err
}

func InsertEvent(ctx context.Context, q sqlkit.Querier, e Event) (Event, error) {
	// entry_preset/entry_rules: new events start on the "video or website" preset.
	row := q.QueryRowContext(ctx, `
		INSERT INTO events (
			id, slug, account_id, created_by, title, stage,
			organiser_name, organisation, contact_email, purpose, expected_participants,
			tagline, about, rules, prizes, coc_text, time_zone, starts_at, ends_at,
			team_size_max, join_code, judge_code, entry_preset, entry_rules, entry_rules_chosen
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, $8, $9, $10, $11,
			$12, $13, $14, $15, $16, $17, $18, $19,
			$20, $21, $22, $23, $24, $25
		) RETURNING `+EventColumns,
		e.ID, e.Slug, e.AccountID, e.CreatedBy, e.Title, e.Stage,
		e.OrganiserName, e.Organisation, e.ContactEmail, e.Purpose, nullInt(e.ExpectedParticipants),
		e.Tagline, e.About, e.Rules, e.Prizes, e.CocText, e.TimeZone, nullTime(e.StartsAt), nullTime(e.EndsAt),
		e.TeamSizeMax, e.JoinCode, e.JudgeCode, e.EntryPreset, e.EntryRules, e.EntryRulesChosen,
	)
	out, err := ScanEvent(row)
	if err != nil && isUniqueViolation(err) {
		return Event{}, err
	}
	return out, err
}

func UpdateEventPatch(ctx context.Context, q sqlkit.Querier, e Event) (Event, error) {
	row := q.QueryRowContext(ctx, `
		UPDATE events SET
			title = $2, tagline = $3, about = $4, rules = $5, prizes = $6, coc_text = $7,
			time_zone = $8, starts_at = $9, ends_at = $10, team_size_max = $11,
			organiser_name = $12, organisation = $13, contact_email = $14, purpose = $15,
			expected_participants = $16, accent_color = $17, updated_at = now(), team_finding = $18, applications_open = $19,
			judging_ends_at = $20, theme = $21, overall_prizes = $22, submissions_public = $23
		WHERE id = $1
		RETURNING `+EventColumns,
		e.ID, e.Title, e.Tagline, e.About, e.Rules, e.Prizes, e.CocText,
		e.TimeZone, nullTime(e.StartsAt), nullTime(e.EndsAt), e.TeamSizeMax,
		e.OrganiserName, e.Organisation, e.ContactEmail, e.Purpose, nullInt(e.ExpectedParticipants), e.AccentColor, e.TeamFinding, e.ApplicationsOpen,
		nullTime(e.JudgingEndsAt), e.Theme, pq.Array(e.OverallPrizes), e.SubmissionsPublic,
	)
	return ScanEvent(row)
}

// SetEventReminderOffsets saves the validated, normalised reminder offsets.
func SetEventReminderOffsets(ctx context.Context, q sqlkit.Querier, eventID string, offsets []int64) (Event, error) {
	return ScanEvent(q.QueryRowContext(ctx, `
		UPDATE events SET reminder_offsets = $2, updated_at = now() WHERE id = $1 RETURNING `+EventColumns,
		eventID, pq.Array(offsets)))
}

// GetEventForUpdate reads the event and locks its row until the transaction ends.
func GetEventForUpdate(ctx context.Context, q *sql.Tx, eventID string) (Event, error) {
	return ScanEvent(q.QueryRowContext(ctx, `SELECT `+EventColumns+` FROM events WHERE id = $1 FOR UPDATE`, eventID))
}

// GetEventForShare reads the event under FOR SHARE, the lock join and apply take before inserting a member row.
func GetEventForShare(ctx context.Context, q sqlkit.Querier, eventID string) (Event, error) {
	return ScanEvent(q.QueryRowContext(ctx, `SELECT `+EventColumns+` FROM events WHERE id = $1 FOR SHARE`, eventID))
}

// UpdateEventEntrySettings writes the submission deadline, the required entry fields and whether the gallery is open.
func UpdateEventEntrySettings(ctx context.Context, q sqlkit.Querier, eventID string, deadline sql.NullTime, required []string, gallery bool) (Event, error) {
	if required == nil {
		required = []string{}
	}
	return ScanEvent(q.QueryRowContext(ctx, `
		UPDATE events SET submission_deadline = $2, entry_required = $3, gallery_open = $4, updated_at = now()
		 WHERE id = $1 RETURNING `+EventColumns, eventID, nullTime(deadline), pq.Array(required), gallery))
}

// SetEventStage sets stage and, when moving to archived, stamps closed_at.
func SetEventStage(ctx context.Context, q sqlkit.Querier, eventID, stage string) (Event, error) {
	var row *sql.Row
	if stage == "archived" {
		row = q.QueryRowContext(ctx, `
			UPDATE events SET stage = $2, closed_at = COALESCE(closed_at, now()), updated_at = now()
			WHERE id = $1 RETURNING `+EventColumns, eventID, stage)
	} else {
		row = q.QueryRowContext(ctx, `
			UPDATE events SET stage = $2, updated_at = now()
			WHERE id = $1 RETURNING `+EventColumns, eventID, stage)
	}
	return ScanEvent(row)
}

func SetEventJoinCode(ctx context.Context, q sqlkit.Querier, eventID, code string) (Event, error) {
	return ScanEvent(q.QueryRowContext(ctx, `
		UPDATE events SET join_code = $2, updated_at = now() WHERE id = $1 RETURNING `+EventColumns, eventID, code))
}

func SetEventJudgeCode(ctx context.Context, q sqlkit.Querier, eventID, code string) (Event, error) {
	return ScanEvent(q.QueryRowContext(ctx, `
		UPDATE events SET judge_code = $2, updated_at = now() WHERE id = $1 RETURNING `+EventColumns, eventID, code))
}

// TakeDownEvent stamps the take-down. The caller also suspends the holding account.
func TakeDownEvent(ctx context.Context, q sqlkit.Querier, eventID, reason string) (Event, error) {
	return ScanEvent(q.QueryRowContext(ctx, `
		UPDATE events SET taken_down_at = COALESCE(taken_down_at, now()), taken_down_reason = $2, updated_at = now()
		WHERE id = $1 RETURNING `+EventColumns, eventID, reason))
}

// RestoreEvent clears a take-down. The caller also unsuspends the holding account.
func RestoreEvent(ctx context.Context, q sqlkit.Querier, eventID string) (Event, error) {
	return ScanEvent(q.QueryRowContext(ctx, `
		UPDATE events SET taken_down_at = NULL, taken_down_reason = '', updated_at = now()
		WHERE id = $1 RETURNING `+EventColumns, eventID))
}

// DeleteEvent deletes the event row; members, teams and the rest cascade.
func DeleteEvent(ctx context.Context, tx *sql.Tx, eventID string) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM events WHERE id = $1`, eventID)
	return err
}

// ListAdminEvents is every event with its creator email (the caller adds the
// head and team counts).
func ListAdminEvents(ctx context.Context, q sqlkit.Querier) ([]AdminEvent, error) {
	rows, err := queryContext(ctx, q, `
		SELECT `+prefixedEventColumns("e")+`
		  FROM events e
		 ORDER BY e.created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AdminEvent
	var creators []string
	for rows.Next() {
		var a AdminEvent
		err := rows.Scan(ScanEventFields(&a.Event)...)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
		if a.CreatedBy.Valid {
			creators = append(creators, a.CreatedBy.String)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	users, err := accounts.UsersByIDs(ctx, q, creators)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].CreatorEmail = users[out[i].CreatedBy.String].Username
	}
	return out, nil
}

func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	// lib/pq and pgx both surface the SQLSTATE in the error text as "23505".
	msg := err.Error()
	return strings.Contains(msg, "23505") || strings.Contains(strings.ToLower(msg), "unique")
}

// EventsByIDs loads these events by id; an id with no event is absent.
func EventsByIDs(ctx context.Context, q sqlkit.Querier, ids []string) (map[string]Event, error) {
	out := map[string]Event{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := q.QueryContext(ctx, `SELECT `+EventColumns+` FROM events WHERE id = ANY($1::uuid[])`, pq.Array(ids))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var e Event
		if err := rows.Scan(ScanEventFields(&e)...); err != nil {
			return nil, err
		}
		out[e.ID] = e
	}
	return out, rows.Err()
}

// ReserveEventName keeps the event's name taken after the event is gone
// (someone besides its organisers was in it).
func ReserveEventName(ctx context.Context, q sqlkit.Querier, eventID string) error {
	_, err := q.ExecContext(ctx, `INSERT INTO event_used_names (event_slug, team_slug)
		SELECT slug, '' FROM events WHERE id=$1 ON CONFLICT DO NOTHING`, eventID)
	return err
}

// AllEvents lists every event, for the sweeps that filter teams or sites
// across events (one query; the callers filter with the values).
func AllEvents(ctx context.Context, q sqlkit.Querier) ([]Event, error) {
	rows, err := queryContext(ctx, q, `SELECT `+EventColumns+` FROM events ORDER BY created_at DESC, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(ScanEventFields(&e)...); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// DeadlinePassed reports whether the event's submission deadline has passed
// by the database clock; share holds the event row FOR SHARE.
func DeadlinePassed(ctx context.Context, q sqlkit.Querier, eventID string, share bool) (bool, error) {
	lock := ""
	if share {
		lock = " FOR SHARE"
	}
	var passed bool
	err := q.QueryRowContext(ctx, `SELECT COALESCE(submission_deadline <= clock_timestamp(), false) FROM events WHERE id = $1`+lock, eventID).Scan(&passed)
	return passed, err
}

// OpenEventSlugs lists the events still taking work (open, building or
// closed, not taken down), whose addresses need certificates.
func OpenEventSlugs(ctx context.Context, q sqlkit.Querier) ([]string, error) {
	rows, err := queryContext(ctx, q, `SELECT slug FROM events WHERE stage IN ('open','building','closed') AND taken_down_at IS NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var slug string
		if err := rows.Scan(&slug); err != nil {
			return nil, err
		}
		out = append(out, slug)
	}
	return out, rows.Err()
}

// NameEverUsed reports whether teamSlug was ever a team of the event named
// eventSlug ("" for the event's own name).
func NameEverUsed(ctx context.Context, q sqlkit.Querier, eventSlug, teamSlug string) (bool, error) {
	var used bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM event_used_names WHERE event_slug = $1 AND team_slug = $2)`, eventSlug, teamSlug).Scan(&used)
	return used, err
}

// RecordUsedNames keeps the event's name and a new team's name for good.
func RecordUsedNames(ctx context.Context, q sqlkit.Querier, eventSlug, teamSlug string) error {
	_, err := q.ExecContext(ctx, `
		INSERT INTO event_used_names (event_slug, team_slug) VALUES ($1, ''), ($1, $2)
		ON CONFLICT DO NOTHING`, eventSlug, teamSlug)
	return err
}

// EventIDsByAccounts maps each holding account id among accountIDs to its
// event's id; an account holding no event is absent.
func EventIDsByAccounts(ctx context.Context, q sqlkit.Querier, accountIDs []string) (map[string]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT account_id::text, id FROM events WHERE account_id::text = ANY($1::text[])`, pq.Array(accountIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var account, id string
		if err := rows.Scan(&account, &id); err != nil {
			return nil, err
		}
		out[account] = id
	}
	return out, rows.Err()
}
