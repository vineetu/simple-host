package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"
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

// entryTextColumns are the plain-text fields of an entry, in the order a
// partial update writes them. tools (an array) and the screenshot bytes are
// separate writes.
var entryTextColumns = []string{"title", "tagline", "description", "video_url", "code_url", "problem", "audience", "maturity"}

const entrySelect = `
	SELECT e.team_id, e.event_id, e.title, e.tagline, e.description, e.video_url, e.code_url,
	       e.problem, e.audience, e.tools, e.maturity,
	       (e.screenshot IS NOT NULL AND octet_length(e.screenshot) > 0),
	       e.screenshot_type, e.updated_at, COALESCE(m.display_name, ''),
	       e.submitted_at, COALESCE(sm.display_name, ''), e.submit_auto, e.submit_settled_at,
	       e.excluded_at, COALESCE(xm.display_name, '')
	  FROM event_entries e
	  LEFT JOIN event_members m
	    ON m.event_id = e.event_id AND m.user_id = e.updated_by
	  LEFT JOIN event_members sm
	    ON sm.event_id = e.event_id AND sm.user_id = e.submitted_by
	  LEFT JOIN event_members xm
	    ON xm.event_id = e.event_id AND xm.user_id = e.excluded_by`

type entryScanner interface {
	Scan(dest ...any) error
}

func scanEventEntry(row entryScanner) (EventEntry, error) {
	var e EventEntry
	err := row.Scan(&e.TeamID, &e.EventID, &e.Title, &e.Tagline, &e.Description, &e.VideoURL, &e.CodeURL,
		&e.Problem, &e.Audience, pq.Array(&e.Tools), &e.Maturity,
		&e.HasScreenshot, &e.ScreenshotType, &e.UpdatedAt, &e.UpdatedByName,
		&e.SubmittedAt, &e.SubmittedByName, &e.SubmitAuto, &e.SubmitSettledAt,
		&e.ExcludedAt, &e.ExcludedByName)
	if err != nil {
		return EventEntry{}, err
	}
	e.Exists = true
	return e, nil
}

// GetEventEntry loads the team's entry. sql.ErrNoRows when it has none.
func GetEventEntry(ctx context.Context, q Querier, teamID string) (EventEntry, error) {
	return scanEventEntry(q.QueryRowContext(ctx, entrySelect+` WHERE e.team_id = $1`, teamID))
}

// GetEventEntryForUpdate is GetEventEntry with the row locked FOR UPDATE
// (the sweep and submit/unsubmit take it after the team row, per the lock
// order: event, member, team, entry).
func GetEventEntryForUpdate(ctx context.Context, q Querier, teamID string) (EventEntry, error) {
	return scanEventEntry(q.QueryRowContext(ctx, entrySelect+` WHERE e.team_id = $1 FOR UPDATE OF e`, teamID))
}

// ListEventEntries loads every entry written for the event.
func ListEventEntries(ctx context.Context, q Querier, eventID string) ([]EventEntry, error) {
	rows, err := queryContext(ctx, q, entrySelect+` WHERE e.event_id = $1`, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EventEntry
	for rows.Next() {
		e, err := scanEventEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// UpsertEventEntryText inserts the team's entry or updates the text fields
// present in fields (column names from entryTextColumns) and, when tools is
// not nil, the tools array. Omitted fields stay as stored. updated_at and
// updated_by always move to this write.
func UpsertEventEntryText(ctx context.Context, q Querier, eventID, teamID, userID string, fields map[string]string, tools *[]string) error {
	allowed := map[string]bool{}
	for _, col := range entryTextColumns {
		allowed[col] = true
	}
	for col := range fields {
		if !allowed[col] {
			return fmt.Errorf("db: unknown entry column %q", col)
		}
	}
	cols := []string{"team_id", "event_id", "updated_by"}
	vals := []string{"$1", "$2", "$3"}
	args := []any{teamID, eventID, userID}
	sets := []string{"updated_at = now()", "updated_by = EXCLUDED.updated_by"}
	for _, col := range entryTextColumns {
		val, ok := fields[col]
		if !ok {
			continue
		}
		args = append(args, val)
		vals = append(vals, fmt.Sprintf("$%d", len(args)))
		cols = append(cols, col)
		sets = append(sets, col+" = EXCLUDED."+col)
	}
	if tools != nil {
		args = append(args, pq.Array(*tools))
		vals = append(vals, fmt.Sprintf("$%d", len(args)))
		cols = append(cols, "tools")
		sets = append(sets, "tools = EXCLUDED.tools")
	}
	query := `INSERT INTO event_entries (` + strings.Join(cols, ", ") + `, updated_at) VALUES (` +
		strings.Join(vals, ", ") + `, now()) ON CONFLICT (team_id) DO UPDATE SET ` + strings.Join(sets, ", ")
	_, err := q.ExecContext(ctx, query, args...)
	return err
}

// UpsertEventEntryScreenshot stores the team's screenshot (data nil and mime
// "" clear it) and stamps the write. Other text fields are left as they are.
func UpsertEventEntryScreenshot(ctx context.Context, q Querier, eventID, teamID, userID string, data []byte, mime string) error {
	_, err := q.ExecContext(ctx, `
		INSERT INTO event_entries (team_id, event_id, screenshot, screenshot_type, updated_at, updated_by)
		VALUES ($1, $2, $3, $4, now(), $5)
		ON CONFLICT (team_id) DO UPDATE SET
			screenshot = EXCLUDED.screenshot,
			screenshot_type = EXCLUDED.screenshot_type,
			updated_at = now(),
			updated_by = EXCLUDED.updated_by`,
		teamID, eventID, data, mime, userID)
	return err
}

// GetEventScreenshot reads the team's stored image. sql.ErrNoRows when the
// team has no entry or the screenshot is empty.
func GetEventScreenshot(ctx context.Context, q Querier, teamID string) ([]byte, string, error) {
	var data []byte
	var mime string
	err := q.QueryRowContext(ctx, `
		SELECT screenshot, screenshot_type FROM event_entries
		 WHERE team_id = $1 AND screenshot IS NOT NULL AND octet_length(screenshot) > 0`, teamID).Scan(&data, &mime)
	return data, mime, err
}

// TeamSiteLive reports whether the holding account has a live site of that
// name (a row in Recently deleted does not count).
func TeamSiteLive(ctx context.Context, q Querier, accountID, name string) (bool, error) {
	var live bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sites WHERE user_id = $1 AND name = $2 AND deleted_at IS NULL)`, accountID, name).Scan(&live)
	return live, err
}

// EntryJudgeableSQL, over event_teams t, is true when a team's entry has
// been decided one way (submitted, or settled as "not submitted") and an
// organiser has not excluded it by hand. A team with no entry row is never
// judgeable. Coordinator decision 2026-11-02 (INTENT.md, owner may
// overrule): an incomplete, "not submitted" entry stays judged with an
// Incomplete flag rather than being dropped -- this is what EligibleJudgeTeams
// and scoreEligiblePredicate gate on instead of the build plan's original
// "en.submitted_at IS NOT NULL".
const EntryJudgeableSQL = `EXISTS (
	SELECT 1 FROM event_entries en WHERE en.team_id = t.id
	  AND (en.submitted_at IS NOT NULL OR en.submit_settled_at IS NOT NULL)
	  AND en.excluded_at IS NULL)`

// SubmitEventEntry marks the team's entry submitted. Idempotent: a second
// call keeps the first submitted_at, submitted_by and submit_auto (section
// 4.1, "submitting twice"). The caller evaluates completeness first; this
// just writes the decision.
func SubmitEventEntry(ctx context.Context, q Querier, teamID, userID string) (EventEntry, error) {
	if _, err := q.ExecContext(ctx, `
		UPDATE event_entries SET
			submitted_by = COALESCE(submitted_by, $2),
			submit_auto = CASE WHEN submitted_at IS NULL THEN false ELSE submit_auto END,
			submit_settled_at = COALESCE(submit_settled_at, now()),
			submitted_at = COALESCE(submitted_at, now())
		 WHERE team_id = $1`, teamID, userID); err != nil {
		return EventEntry{}, err
	}
	return GetEventEntry(ctx, q, teamID)
}

// UnsubmitEventEntry puts the entry back to draft: submitted_at, submitted_by,
// submit_auto and submit_settled_at all clear, so the sweep reconsiders it if
// the deadline has in fact passed (it is only reachable before the deadline;
// entryWrite's WriteRefusal already guards that).
func UnsubmitEventEntry(ctx context.Context, q Querier, teamID string) (EventEntry, error) {
	if _, err := q.ExecContext(ctx, `
		UPDATE event_entries SET submitted_at = NULL, submitted_by = NULL, submit_auto = false, submit_settled_at = NULL
		 WHERE team_id = $1`, teamID); err != nil {
		return EventEntry{}, err
	}
	return GetEventEntry(ctx, q, teamID)
}

// SettleEntryComplete is the deadline sweep's decision for a complete draft:
// auto-submitted at the earlier of now and the deadline (a sweep that runs
// late must not credit a team with extra time), submit_auto true.
func SettleEntryComplete(ctx context.Context, q Querier, teamID string, deadline time.Time) error {
	_, err := q.ExecContext(ctx, `
		UPDATE event_entries SET submitted_at = LEAST(now(), $2), submit_auto = true, submit_settled_at = now()
		 WHERE team_id = $1 AND submitted_at IS NULL`, teamID, deadline)
	return err
}

// SettleEntryIncomplete is the sweep's decision for an incomplete draft:
// left unsubmitted (status "not_submitted"), just marked settled. Owner
// coordinator decision 2026-11-02 (INTENT.md): it stays judged, carrying an
// Incomplete flag, rather than dropped -- unlike the build plan's original
// "not judged" rule. An organiser excludes it by hand (SetEntryExcluded) if
// they choose to.
func SettleEntryIncomplete(ctx context.Context, q Querier, teamID string) error {
	_, err := q.ExecContext(ctx, `
		UPDATE event_entries SET submit_settled_at = now()
		 WHERE team_id = $1 AND submitted_at IS NULL AND submit_settled_at IS NULL`, teamID)
	return err
}

// UnsettledFrozenTeamIDs lists teams (of eventID, or every event when "")
// whose entry is written, not yet settled one way or the other, and now
// frozen: the team's effective deadline has passed, or the event stage is
// judging, results or archived. A team with no entry row is never listed --
// it has nothing to settle and reads as "Not started".
func UnsettledFrozenTeamIDs(ctx context.Context, q Querier, eventID string) ([]string, error) {
	rows, err := queryContext(ctx, q, `
		SELECT en.team_id FROM event_entries en
		  JOIN event_teams t ON t.id = en.team_id
		  JOIN events e ON e.id = t.event_id
		 WHERE en.submitted_at IS NULL AND en.submit_settled_at IS NULL
		   AND (e.stage IN ('judging', 'results', 'archived')
		        OR (`+EffectiveDeadlineSQL+` IS NOT NULL AND `+EffectiveDeadlineSQL+` <= clock_timestamp()))
		   AND ($1 = '' OR e.id::text = $1)`, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// UnsettleTeamEntry clears submit_settled_at on one team's unsubmitted draft
// (an extension, section 7 "Unsettle"): the sweep reconsiders it against the
// new deadline. A submitted entry is untouched -- tightening or loosening the
// clock never un-submits.
func UnsettleTeamEntry(ctx context.Context, q Querier, teamID string) error {
	_, err := q.ExecContext(ctx, `
		UPDATE event_entries SET submit_settled_at = NULL
		 WHERE team_id = $1 AND submitted_at IS NULL`, teamID)
	return err
}

// UnsettleEventDrafts is UnsettleTeamEntry for every unsubmitted draft of the
// event: the stage moving back to building, or the event deadline moving
// later (section 7).
func UnsettleEventDrafts(ctx context.Context, q Querier, eventID string) error {
	_, err := q.ExecContext(ctx, `
		UPDATE event_entries SET submit_settled_at = NULL
		 WHERE event_id = $1 AND submitted_at IS NULL`, eventID)
	return err
}

// SetEntryExcluded is the organiser's hand exclude/include of a flagged
// entry from judging (coordinator decision 2026-11-02, INTENT.md). Allowed
// on any entry, not only an incomplete one -- an organiser call, not gated
// on status.
func SetEntryExcluded(ctx context.Context, q Querier, teamID, userID string, excluded bool) (EventEntry, error) {
	var err error
	if excluded {
		_, err = q.ExecContext(ctx, `
			UPDATE event_entries SET excluded_at = now(), excluded_by = $2 WHERE team_id = $1`, teamID, userID)
	} else {
		_, err = q.ExecContext(ctx, `
			UPDATE event_entries SET excluded_at = NULL, excluded_by = NULL WHERE team_id = $1`, teamID)
	}
	if err != nil {
		return EventEntry{}, err
	}
	return GetEventEntry(ctx, q, teamID)
}

// UpdateEventEntryRules writes the organiser's entry rules: the preset name,
// the expanded+validated rules JSON (never the client's raw JSON -- the
// handler normalises it first) and which evidence judges see first. It also
// writes entry_required for the deprecated alias, kept in sync with the
// rules' required fields among hackEntryFields for one release.
func UpdateEventEntryRules(ctx context.Context, q Querier, eventID, preset string, rulesJSON []byte, judgeFirst string, legacyRequired []string) (Event, error) {
	if legacyRequired == nil {
		legacyRequired = []string{}
	}
	return scanEvent(q.QueryRowContext(ctx, `
		UPDATE events SET entry_preset = $2, entry_rules = $3, judge_first = $4, entry_required = $5, updated_at = now()
		 WHERE id = $1 RETURNING `+eventColumns, eventID, preset, rulesJSON, judgeFirst, pq.Array(legacyRequired)))
}
