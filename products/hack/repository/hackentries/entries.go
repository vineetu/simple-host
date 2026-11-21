package hackentries

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/sqlkit"

	"github.com/lib/pq"
	"github.com/vsriram/simple-host/products/hack/model"
	"github.com/vsriram/simple-host/products/hack/repository/hackevents"
	"github.com/vsriram/simple-host/products/hack/repository/hackteams"
)

type EventEntry = model.EventEntry

// entryTextColumns are the plain-text fields of an entry, in the order a
// partial update writes them.
var entryTextColumns = []string{"title", "tagline", "description", "video_url", "code_url", "problem", "audience", "maturity"}

const entrySelect = `
	SELECT e.team_id, e.event_id, e.title, e.tagline, e.description, e.video_url, e.code_url,
	       e.problem, e.audience, e.tools, e.maturity,
	       (e.screenshot IS NOT NULL AND octet_length(e.screenshot) > 0),
	       e.screenshot_type, e.updated_at, COALESCE(e.updated_by::text, ''),
	       e.submitted_at, COALESCE(e.submitted_by::text, ''), e.submit_auto, e.submit_settled_at,
	       e.excluded_at, COALESCE(e.excluded_by::text, '')
	  FROM event_entries e`

type entryScanner interface {
	Scan(dest ...any) error
}

// scanEventEntry reads an entrySelect row; the three ...ByName fields hold
// account ids until nameEntries turns them into display names.
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

// nameEntries replaces the ids scanEventEntry left in the ...ByName fields
// with display names ("" when there is no such account), in one query.
func nameEntries(ctx context.Context, q sqlkit.Querier, entries []EventEntry) error {
	var ids []string
	for _, e := range entries {
		for _, id := range []string{e.UpdatedByName, e.SubmittedByName, e.ExcludedByName} {
			if id != "" {
				ids = append(ids, id)
			}
		}
	}
	users, err := accounts.UsersByIDs(ctx, q, ids)
	if err != nil {
		return err
	}
	for i := range entries {
		e := &entries[i]
		e.UpdatedByName = users[e.UpdatedByName].DisplayName.String
		e.SubmittedByName = users[e.SubmittedByName].DisplayName.String
		e.ExcludedByName = users[e.ExcludedByName].DisplayName.String
	}
	return nil
}

// getEventEntry is one entrySelect row with its names filled in.
func getEventEntry(ctx context.Context, q sqlkit.Querier, row *sql.Row) (EventEntry, error) {
	e, err := scanEventEntry(row)
	if err != nil {
		return EventEntry{}, err
	}
	one := []EventEntry{e}
	if err := nameEntries(ctx, q, one); err != nil {
		return EventEntry{}, err
	}
	return one[0], nil
}

// GetEventEntry loads the team's entry. sql.ErrNoRows when it has none.
func GetEventEntry(ctx context.Context, q sqlkit.Querier, teamID string) (EventEntry, error) {
	return getEventEntry(ctx, q, q.QueryRowContext(ctx, entrySelect+` WHERE e.team_id = $1`, teamID))
}

// GetEventEntryForUpdate is GetEventEntry with the row locked FOR UPDATE
// (the sweep and submit/unsubmit take it after the team row, per the lock
// order: event, member, team, entry).
func GetEventEntryForUpdate(ctx context.Context, q *sql.Tx, teamID string) (EventEntry, error) {
	return getEventEntry(ctx, q, q.QueryRowContext(ctx, entrySelect+` WHERE e.team_id = $1 FOR UPDATE OF e`, teamID))
}

func ListEventEntries(ctx context.Context, q sqlkit.Querier, eventID string) ([]EventEntry, error) {
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nameEntries(ctx, q, out)
}

// UpsertEventEntryText inserts the team's entry or updates the text fields
// present in fields (column names from entryTextColumns) and, when tools is not
// nil, the tools array.
func UpsertEventEntryText(ctx context.Context, q sqlkit.Querier, eventID, teamID, userID string, fields map[string]string, tools *[]string) error {
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
func UpsertEventEntryScreenshot(ctx context.Context, q sqlkit.Querier, eventID, teamID, userID string, data []byte, mime string) error {
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

func GetEventScreenshot(ctx context.Context, q sqlkit.Querier, teamID string) ([]byte, string, error) {
	var data []byte
	var mime string
	err := q.QueryRowContext(ctx, `
		SELECT screenshot, screenshot_type FROM event_entries
		 WHERE team_id = $1 AND screenshot IS NOT NULL AND octet_length(screenshot) > 0`, teamID).Scan(&data, &mime)
	return data, mime, err
}

// EntryJudgeableSQL implements rules/hackentries.Judgeable over event_teams t;
// TestEntryJudgeableSQL checks the contract.
const EntryJudgeableSQL = `EXISTS (
	SELECT 1 FROM event_entries en WHERE en.team_id = t.id
	  AND (en.submitted_at IS NOT NULL OR en.submit_settled_at IS NOT NULL)
	  AND en.excluded_at IS NULL)`

func SubmitEventEntry(ctx context.Context, q sqlkit.Querier, teamID, userID string) (EventEntry, error) {
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
// submit_auto and submit_settled_at clear, so the sweep reconsiders it. Only
// reachable before the deadline (entryWrite's WriteRefusal guards that).
func UnsubmitEventEntry(ctx context.Context, q sqlkit.Querier, teamID string) (EventEntry, error) {
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
func SettleEntryComplete(ctx context.Context, q sqlkit.Querier, teamID string, deadline time.Time) error {
	_, err := q.ExecContext(ctx, `
		UPDATE event_entries SET submitted_at = LEAST(now(), $2), submit_auto = true, submit_settled_at = now()
		 WHERE team_id = $1 AND submitted_at IS NULL`, teamID, deadline)
	return err
}

// SettleEntryIncomplete is the sweep's decision for an incomplete draft: left
// unsubmitted (status "not_submitted"), just marked settled.
func SettleEntryIncomplete(ctx context.Context, q sqlkit.Querier, teamID string) error {
	_, err := q.ExecContext(ctx, `
		UPDATE event_entries SET submit_settled_at = now()
		 WHERE team_id = $1 AND submitted_at IS NULL AND submit_settled_at IS NULL`, teamID)
	return err
}

// UnsettledTeam is one team of an event whose entry waits to be settled.
type UnsettledTeam struct{ EventID, TeamID string }

// UnsettledFrozenTeamIDs lists teams (of eventID, or every event when "") whose
// entry is written, not yet settled one way or the other, and now frozen: the
// team's effective deadline has passed, or the event stage is judging, results
// or archived. Four statements per sweep, none per team.

func UnsettledFrozenTeamIDs(ctx context.Context, q sqlkit.Querier, eventID string) ([]UnsettledTeam, error) {
	var events []model.Event
	if eventID == "" {
		all, err := hackevents.AllEvents(ctx, q)
		if err != nil {
			return nil, err
		}
		events = all
	} else {
		ev, err := hackevents.GetEventByID(ctx, q, eventID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		events = []model.Event{ev}
	}
	var ids, deadlines []string
	var frozen []bool
	for _, e := range events {
		ids, frozen = append(ids, e.ID), append(frozen, hackevents.FrozenStage(e.Stage))
		d := ""
		if e.SubmissionDeadline.Valid {
			d = e.SubmissionDeadline.Time.UTC().Format(time.RFC3339Nano)
		}
		deadlines = append(deadlines, d)
	}
	open, err := unsettledEntryTeams(ctx, q, ids)
	if err != nil {
		return nil, err
	}
	byID, err := hackteams.TeamsByIDs(ctx, q, open)
	if err != nil {
		return nil, err
	}
	teams := make([]model.EventTeam, 0, len(byID))
	for _, t := range byID {
		teams = append(teams, t)
	}
	deadline := hackevents.EffectiveDeadline(`NULLIF(d.deadline, '')::timestamptz`)
	rows, err := queryContext(ctx, q, `
		SELECT t.event_id, en.team_id FROM event_entries en
		  JOIN `+teamRows(4)+` ON t.id = en.team_id
		  JOIN unnest($1::uuid[], $2::text[], $3::bool[]) AS d(event_id, deadline, frozen) ON d.event_id = t.event_id
		 WHERE en.submitted_at IS NULL AND en.submit_settled_at IS NULL
		   AND (d.frozen OR (`+deadline+` IS NOT NULL AND `+deadline+` <= clock_timestamp()))`,
		append([]any{pq.Array(ids), pq.Array(deadlines), pq.Array(frozen)}, teamArgs(teams)...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UnsettledTeam
	for rows.Next() {
		var u UnsettledTeam
		if err := rows.Scan(&u.EventID, &u.TeamID); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// unsettledEntryTeams is the teams of these events whose entry is neither
// submitted nor settled.
func unsettledEntryTeams(ctx context.Context, q sqlkit.Querier, eventIDs []string) ([]string, error) {
	rows, err := queryContext(ctx, q, `
		SELECT team_id FROM event_entries
		 WHERE event_id = ANY($1::uuid[]) AND submitted_at IS NULL AND submit_settled_at IS NULL`, pq.Array(eventIDs))
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

// FrozenTeamWithoutEntry is a team the settle sweep may start an entry for:
// it has members (or was kept after its last member left), its write window
// has closed, and it never saved an entry row.
type FrozenTeamWithoutEntry struct {
	TeamID        string
	Slug          string
	SiteTakenDown bool
}

// FrozenTeamsWithoutEntry lists the event's frozen teams that have no entry
// row.
func FrozenTeamsWithoutEntry(ctx context.Context, q sqlkit.Querier, eventID string) ([]FrozenTeamWithoutEntry, error) {
	teams, err := hackteams.JudgedTeams(ctx, q, eventID)
	if err != nil {
		return nil, err
	}
	ev, err := hackevents.GetEventByID(ctx, q, eventID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	deadline := hackevents.EffectiveDeadline("$3::timestamptz")
	rows, err := queryContext(ctx, q, `
		SELECT t.id, t.slug, t.site_taken_down FROM `+teamRows(4)+`
		 WHERE t.event_id = $1
		   AND NOT EXISTS (SELECT 1 FROM event_entries en WHERE en.team_id = t.id)
		   AND ($2 OR (`+deadline+` IS NOT NULL AND `+deadline+` <= clock_timestamp()))
		 ORDER BY t.id`, append([]any{eventID, hackevents.FrozenStage(ev.Stage), ev.SubmissionDeadline}, teamArgs(teams)...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FrozenTeamWithoutEntry
	for rows.Next() {
		var t FrozenTeamWithoutEntry
		if err := rows.Scan(&t.TeamID, &t.Slug, &t.SiteTakenDown); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// StartEmptyEventEntry inserts an empty entry row for the team, leaving any
// existing row alone. updated_by stays NULL: nobody on the team wrote it.
func StartEmptyEventEntry(ctx context.Context, q sqlkit.Querier, eventID, teamID string) error {
	_, err := q.ExecContext(ctx, `
		INSERT INTO event_entries (team_id, event_id, updated_at)
		VALUES ($1, $2, now())
		ON CONFLICT (team_id) DO NOTHING`, teamID, eventID)
	return err
}

// UnsettleTeamEntry clears submit_settled_at on one team's unsubmitted draft
// (an extension, section 7 "Unsettle"): the sweep reconsiders it against the
// new deadline.
func UnsettleTeamEntry(ctx context.Context, q sqlkit.Querier, teamID string) error {
	_, err := q.ExecContext(ctx, `
		UPDATE event_entries SET submit_settled_at = NULL
		 WHERE team_id = $1 AND submitted_at IS NULL`, teamID)
	return err
}

// UnsettleEventDrafts is UnsettleTeamEntry for every unsubmitted draft of the
// event: the stage moving back to building, or the event deadline moving
// later (section 7).
func UnsettleEventDrafts(ctx context.Context, q sqlkit.Querier, eventID string) error {
	_, err := q.ExecContext(ctx, `
		UPDATE event_entries SET submit_settled_at = NULL
		 WHERE event_id = $1 AND submitted_at IS NULL`, eventID)
	return err
}

// SetEntryExcluded is the organiser's hand exclude/include of a flagged entry
// from judging.
func SetEntryExcluded(ctx context.Context, q sqlkit.Querier, teamID, userID string, excluded bool) (EventEntry, error) {
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
