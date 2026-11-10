package hackevents

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/lib/pq"
	"github.com/vsriram/simple-host/internal/model"
	entryrepo "github.com/vsriram/simple-host/internal/repository/hackentries"
	"github.com/vsriram/simple-host/internal/repository/sqlkit"
)

// Event aliases the shared model record.
type Event = model.Event

// AdminEvent aliases the shared model record.
type AdminEvent = model.AdminEvent

// EventPerson aliases the shared model record.
type EventPerson = model.EventPerson

// EventColumns is the event scan column order.
const EventColumns = `
	id, slug, account_id, created_by, title, stage,
	organiser_name, organisation, contact_email, purpose, expected_participants,
	tagline, website_mode, icon_media_type, accent_color, about, rules, prizes, coc_text, time_zone, starts_at, ends_at,
	team_size_max, join_code, judge_code, submission_deadline, results_visibility,
	results_published_at, closed_at, removal_warned_at, sites_removed_at, keep_sites,
	taken_down_at, taken_down_reason, created_at, updated_at,
	entry_required, gallery_open,
	judge_assignment_mode, judges_per_team, judging_locked_at, judging_lock_reason,
	score_mode, tie_criterion_id, public_scores, public_ranks, team_finding, applications_open, directory_listed,
	entry_preset, entry_rules, judge_first, reminder_offsets, judging_ends_at, entry_rules_chosen, theme, organiser_judges`

// ScanEventFields returns destinations in EventColumns order.
func ScanEventFields(e *Event) []any {
	return []any{
		&e.ID, &e.Slug, &e.AccountID, &e.CreatedBy, &e.Title, &e.Stage,
		&e.OrganiserName, &e.Organisation, &e.ContactEmail, &e.Purpose, &e.ExpectedParticipants,
		&e.Tagline, &e.WebsiteMode, &e.IconMediaType, &e.AccentColor, &e.About, &e.Rules, &e.Prizes, &e.CocText, &e.TimeZone, &e.StartsAt, &e.EndsAt,
		&e.TeamSizeMax, &e.JoinCode, &e.JudgeCode, &e.SubmissionDeadline, &e.ResultsVisibility,
		&e.ResultsPublishedAt, &e.ClosedAt, &e.RemovalWarnedAt, &e.SitesRemovedAt, &e.KeepSites,
		&e.TakenDownAt, &e.TakenDownReason, &e.CreatedAt, &e.UpdatedAt,
		pq.Array(&e.EntryRequired), &e.GalleryOpen,
		&e.JudgeAssignmentMode, &e.JudgesPerTeam, &e.JudgingLockedAt, &e.JudgingLockReason,
		&e.ScoreMode, &e.TieCriterionID, &e.PublicScores, &e.PublicRanks, &e.TeamFinding, &e.ApplicationsOpen, &e.DirectoryListed,
		&e.EntryPreset, &e.EntryRules, &e.JudgeFirst, pq.Array(&e.ReminderOffsets), &e.JudgingEndsAt, &e.EntryRulesChosen, &e.Theme, pq.Array(&e.OrganiserJudges),
	}
}

// ScanEvent scans one event row.
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

func nullStr(s sql.NullString) any {
	if !s.Valid {
		return nil
	}
	return s.String
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

func scanEventRole(rows *sql.Rows) (Event, string, error) {
	var e Event
	var role string
	dest := append(ScanEventFields(&e), &role)
	err := rows.Scan(dest...)
	return e, role, err
}

func scanMemberRow(rows *sql.Rows) (EventMember, error) {
	var m EventMember
	err := rows.Scan(&m.EventID, &m.UserID, &m.Role, &m.DisplayName, &m.TeamID, &m.CocAcceptedAt, &m.JoinedAt)
	return m, err
}

// GetEventBySlug loads an event by its slug.
func GetEventBySlug(ctx context.Context, q sqlkit.Querier, slug string) (Event, error) {
	return ScanEvent(q.QueryRowContext(ctx, `SELECT `+EventColumns+` FROM events WHERE slug = $1`, slug))
}

// GetEventByID loads an event by its id, for a route that only has a team's
// event_id in hand (GET /v1/hack/team-links/{code}).
func GetEventByID(ctx context.Context, q sqlkit.Querier, eventID string) (Event, error) {
	return ScanEvent(q.QueryRowContext(ctx, `SELECT `+EventColumns+` FROM events WHERE id = $1`, eventID))
}

// GetDemoEvent finds the standing demo: the newest open or building event whose slug is
// the demo slug or a dated successor (demo-YYYYWW, demo-YYYYWW-2), organised by the admin
// account. Used slugs stay reserved, so the weekly reset moves to a dated name; the admin
// check keeps anyone else from claiming a demo-* name and capturing the demo links.
func GetDemoEvent(ctx context.Context, q sqlkit.Querier, slug string) (Event, error) {
	return ScanEvent(q.QueryRowContext(ctx, `SELECT `+EventColumns+` FROM events
		WHERE (slug = $1 OR slug LIKE $1 || '-%') AND taken_down_at IS NULL AND stage IN ('open', 'building')
		AND EXISTS (SELECT 1 FROM event_members m JOIN users u ON u.id = m.user_id
			WHERE m.event_id = events.id AND m.role = 'organiser' AND COALESCE(u.is_admin, false))
		ORDER BY created_at DESC LIMIT 1`, slug))
}

// GetEventByAccount loads the event held by the given users row.
func GetEventByAccount(ctx context.Context, q sqlkit.Querier, accountID string) (Event, error) {
	return ScanEvent(q.QueryRowContext(ctx, `SELECT `+EventColumns+` FROM events WHERE account_id = $1`, accountID))
}

// SetEventWebsiteMode switches the event host between the built-in and custom page.
func SetEventWebsiteMode(ctx context.Context, q sqlkit.Querier, eventID, organiserID, mode string) (Event, error) {
	return ScanEvent(q.QueryRowContext(ctx, `UPDATE events SET website_mode = $2, updated_at = now()
		WHERE id = $1 AND stage <> 'archived' AND taken_down_at IS NULL
		AND EXISTS (SELECT 1 FROM event_members m WHERE m.event_id = events.id AND m.user_id = $3 AND m.role = 'organiser')
		RETURNING `+EventColumns, eventID, mode, organiserID))
}

// GetEventByJoinCode looks up an event by its participant join code (already normalised).
func GetEventByJoinCode(ctx context.Context, q sqlkit.Querier, code string) (Event, error) {
	return ScanEvent(q.QueryRowContext(ctx, `SELECT `+EventColumns+` FROM events WHERE join_code = $1`, code))
}

// GetEventByJudgeCode looks up an event by its judge code (already normalised).
func GetEventByJudgeCode(ctx context.Context, q sqlkit.Querier, code string) (Event, error) {
	return ScanEvent(q.QueryRowContext(ctx, `SELECT `+EventColumns+` FROM events WHERE judge_code = $1`, code))
}

// CountEventMembers returns how many participants, teams and judges an event has.
func CountEventMembers(ctx context.Context, q sqlkit.Querier, eventID string) (participants, teams, judges int, err error) {
	err = q.QueryRowContext(ctx, `
		SELECT
			(SELECT COUNT(*) FROM event_members WHERE event_id = $1 AND role = 'participant' AND approval_status = 'approved'),
			(SELECT COUNT(*) FROM event_teams WHERE event_id = $1),
			(SELECT COUNT(*) FROM event_members m JOIN events e ON e.id=m.event_id WHERE m.event_id = $1 AND `+entryrepo.JudgeMemberSQL("m", "e")+`)`,
		eventID).Scan(&participants, &teams, &judges)
	return
}

// CountEligibleJudges counts joined judges and selected organiser judges.
func CountEligibleJudges(ctx context.Context, q sqlkit.Querier, eventID string) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM event_members m JOIN events e ON e.id=m.event_id WHERE m.event_id = $1 AND `+entryrepo.JudgeMemberSQL("m", "e")+``, eventID).Scan(&n)
	return n, err
}

// CountOnNoTeam is participants of the event with no team.
func CountOnNoTeam(ctx context.Context, q sqlkit.Querier, eventID string) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM event_members
		 WHERE event_id = $1 AND role = 'participant' AND approval_status='approved' AND team_id IS NULL`, eventID).Scan(&n)
	return n, err
}

// CountEmailableParticipants is approved participants with an email-shaped
// username -- the only ones an announcement's email_participants can reach.
// The organiser-announcements checkbox labels itself with this, not with
// CountEventMembers' participant total, so it never promises mail to people
// who hold key-only test accounts or any other "@"-less username.
func CountEmailableParticipants(ctx context.Context, q sqlkit.Querier, eventID string) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM event_members m JOIN users u ON u.id = m.user_id
		 WHERE m.event_id = $1 AND m.role = 'participant' AND m.approval_status = 'approved' AND u.username LIKE '%@%'`, eventID).Scan(&n)
	return n, err
}

// EventSlugTaken reports whether an events row already uses slug.
func EventSlugTaken(ctx context.Context, q sqlkit.Querier, slug string) (bool, error) {
	var taken bool
	// A name that had a participant, judge or team stays taken after its event is gone
	// (event_used_names): its team origins never pass to someone else.
	err := q.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM events WHERE slug = $1)
		    OR EXISTS (SELECT 1 FROM event_used_names WHERE event_slug = $1)`, slug).Scan(&taken)
	return taken, err
}

// CountEventCreatesSince counts events created since t that still exist.
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

// InsertEvent writes a new events row. Unique violations on slug or codes are
// returned as-is so the caller can retry codes or map a slug clash to name_taken.
func InsertEvent(ctx context.Context, q sqlkit.Querier, e Event) (Event, error) {
	// entry_preset/entry_rules: new events start on the "video or website"
	// preset (the create handler passes its expanded rules JSON;
	// zv-hack-entry-summary.sql section 2 -- a Go table, not a DB default,
	// so the preset's field list lives in one place,
	// internal/rules/hackentries/entries.go). entry_rules_chosen: the
	// create handler passes false (0005-hack-entry-rules-chosen.sql,
	// SIM-382) -- the organiser hasn't saved a choice yet, which is what
	// gates Overview's "Choose what teams hand in" first step.
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

// UpdateEventPatch writes the editable fields and bumps updated_at.
func UpdateEventPatch(ctx context.Context, q sqlkit.Querier, e Event) (Event, error) {
	row := q.QueryRowContext(ctx, `
		UPDATE events SET
			title = $2, tagline = $3, about = $4, rules = $5, prizes = $6, coc_text = $7,
			time_zone = $8, starts_at = $9, ends_at = $10, team_size_max = $11,
			organiser_name = $12, organisation = $13, contact_email = $14, purpose = $15,
			expected_participants = $16, accent_color = $17, updated_at = now(), team_finding = $18, applications_open = $19,
			judging_ends_at = $20, theme = $21
		WHERE id = $1
		RETURNING `+EventColumns,
		e.ID, e.Title, e.Tagline, e.About, e.Rules, e.Prizes, e.CocText,
		e.TimeZone, nullTime(e.StartsAt), nullTime(e.EndsAt), e.TeamSizeMax,
		e.OrganiserName, e.Organisation, e.ContactEmail, e.Purpose, nullInt(e.ExpectedParticipants), e.AccentColor, e.TeamFinding, e.ApplicationsOpen,
		nullTime(e.JudgingEndsAt), e.Theme,
	)
	return ScanEvent(row)
}

// SetEventReminderOffsets saves the validated, normalised reminder offsets
// (0003-hack-notices-judges-messages.sql).
func SetEventReminderOffsets(ctx context.Context, q sqlkit.Querier, eventID string, offsets []int64) (Event, error) {
	return ScanEvent(q.QueryRowContext(ctx, `
		UPDATE events SET reminder_offsets = $2, updated_at = now() WHERE id = $1 RETURNING `+EventColumns,
		eventID, pq.Array(offsets)))
}

// GetEventForUpdate reads the event and locks its row until the transaction
// ends.
func GetEventForUpdate(ctx context.Context, q *sql.Tx, eventID string) (Event, error) {
	return ScanEvent(q.QueryRowContext(ctx, `SELECT `+EventColumns+` FROM events WHERE id = $1 FOR UPDATE`, eventID))
}

// GetEventForShare reads the event and takes FOR SHARE, the lock join and
// apply already take ahead of inserting a member row so a concurrent leave
// (which takes FOR UPDATE) serialises against them -- sql.ErrNoRows when the
// event no longer exists (the last organiser's concurrent delete won).
// Unlike the bare `SELECT 1 ... FOR SHARE` the judge and participant join
// branches used before (Codex review, SIM-340), this returns the row so the
// caller can revalidate stage/takedown against the state as of the lock,
// not the pre-lock snapshot a blocked insert would otherwise go on to use.
func GetEventForShare(ctx context.Context, q *sql.Tx, eventID string) (Event, error) {
	return ScanEvent(q.QueryRowContext(ctx, `SELECT `+EventColumns+` FROM events WHERE id = $1 FOR SHARE`, eventID))
}

// UpdateEventEntrySettings writes the M2 settings: the submission deadline,
// the required entry fields and whether the gallery is open.
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

// SetEventJoinCode replaces the participant join code.
func SetEventJoinCode(ctx context.Context, q sqlkit.Querier, eventID, code string) (Event, error) {
	return ScanEvent(q.QueryRowContext(ctx, `
		UPDATE events SET join_code = $2, updated_at = now() WHERE id = $1 RETURNING `+EventColumns, eventID, code))
}

// SetEventJudgeCode replaces the judge code.
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

// DeleteEventAndAccount deletes the event (cascading members and teams) then
// the holding account, which frees the handle/slug. One transaction.
func DeleteEventAndAccount(ctx context.Context, q *sql.Tx, ev Event) error {
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
	// event_notices has no foreign key to events (zw-hack-wait-notices.sql).
	if _, err := q.ExecContext(ctx, `DELETE FROM event_notices WHERE event_id = $1`, ev.ID); err != nil {
		return err
	}
	if _, err := q.ExecContext(ctx, `DELETE FROM events WHERE id = $1`, ev.ID); err != nil {
		return err
	}
	_, err := q.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, ev.AccountID)
	return err
}

// ListEventsForUser is GET /v1/hack/events: every event the person is in.
func ListEventsForUser(ctx context.Context, q sqlkit.Querier, userID string) ([]Event, []string, error) {
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

// ListAdminEvents is every event with organiser details, counts and creator email.
func ListAdminEvents(ctx context.Context, q sqlkit.Querier) ([]AdminEvent, error) {
	rows, err := queryContext(ctx, q, `
		SELECT `+prefixedEventColumns("e")+`,
		       COALESCE(u.username, ''),
		       (SELECT COUNT(*) FROM event_members m WHERE m.event_id = e.id AND m.role = 'participant'),
		       (SELECT COUNT(*) FROM event_teams t WHERE t.event_id = e.id),
		       (SELECT COUNT(*) FROM event_members m WHERE m.event_id = e.id AND `+entryrepo.JudgeMemberSQL("m", "e")+`)
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
		dest := append(ScanEventFields(&a.Event), &a.CreatorEmail, &a.Participants, &a.Teams, &a.Judges)
		err := rows.Scan(dest...)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// GetEventMember loads one membership. sql.ErrNoRows if none.
func GetEventMember(ctx context.Context, q sqlkit.Querier, eventID, userID string) (EventMember, error) {
	return scanMember(q.QueryRowContext(ctx, `
		SELECT m.event_id, m.user_id, m.role, COALESCE(u.display_name,''), m.team_id, m.coc_accepted_at, m.joined_at
		  FROM event_members m JOIN users u ON u.id=m.user_id WHERE m.event_id = $1 AND m.user_id = $2`, eventID, userID))
}

// InsertEventMember adds a person to an event. Unique (event, user) is already_member.
func InsertEventMember(ctx context.Context, q sqlkit.Querier, eventID, userID, role string) (EventMember, error) {
	row := q.QueryRowContext(ctx, `
		WITH joined AS (
		INSERT INTO event_members (event_id, user_id, role, coc_accepted_at)
		VALUES ($1, $2, $3, now())
		RETURNING event_id, user_id, role, team_id, coc_accepted_at, joined_at
		), reserved AS (
		INSERT INTO event_used_names (event_slug, team_slug)
		SELECT e.slug, '' FROM events e JOIN joined j ON j.event_id=e.id WHERE j.role <> 'organiser'
		ON CONFLICT DO NOTHING
		) SELECT j.event_id,j.user_id,j.role,COALESCE(u.display_name,''),j.team_id,j.coc_accepted_at,j.joined_at FROM joined j JOIN users u ON u.id=j.user_id`,
		eventID, userID, role)
	m, err := scanMember(row)
	if err != nil && isUniqueViolation(err) {
		return EventMember{}, err
	}
	return m, err
}

// ListEventPeople is GET /v1/hack/events/{slug}/people and the participants
// CSV. It is the only EventPerson listing that reads the application profile
// fields (city, country, age_range, gender): the handler decides who sees them.
func ListEventPeople(ctx context.Context, q sqlkit.Querier, eventID string) ([]EventPerson, error) {
	rows, err := queryContext(ctx, q, `
		SELECT m.user_id, u.username, COALESCE(u.display_name,''), m.role, m.team_id,
		       t.slug, t.name, m.joined_at, m.coc_accepted_at, m.approval_status,
		       m.joined_via, m.city, m.country, m.age_range, m.gender, m.team_admin
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
			&p.TeamSlug, &p.TeamName, &p.JoinedAt, &p.CocAcceptedAt, &p.ApprovalStatus,
			&p.JoinedVia, &p.City, &p.Country, &p.AgeRange, &p.Gender, &p.Admin); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ListParticipantsOnNoTeam is the organiser teams view's unteamed people.
func ListParticipantsOnNoTeam(ctx context.Context, q sqlkit.Querier, eventID string) ([]EventPerson, error) {
	rows, err := queryContext(ctx, q, `
		SELECT m.user_id, u.username, COALESCE(u.display_name,''), m.role, m.team_id,
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

// isUniqueViolation detects a Postgres unique-constraint violation (SQLSTATE 23505).
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	// lib/pq and pgx both surface the SQLSTATE in the error text as "23505".
	msg := err.Error()
	return strings.Contains(msg, "23505") || strings.Contains(strings.ToLower(msg), "unique")
}
