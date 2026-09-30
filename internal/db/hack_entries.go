package db

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// EventEntry is one team's entry (event_entries). Exists is false when the
// team has not written one yet: the text is empty and there is no screenshot.
// UpdatedByName is the writer's display name in this event, or "" when they
// are no longer a member — never an email or an account id.
type EventEntry struct {
	TeamID         string
	EventID        string
	Title          string
	Tagline        string
	Description    string
	VideoURL       string
	CodeURL        string
	HasScreenshot  bool
	ScreenshotType string
	UpdatedAt      time.Time
	UpdatedByName  string
	Exists         bool
}

// entryTextColumns are the text fields of an entry, in the order a partial
// update writes them. Screenshot bytes are a separate write.
var entryTextColumns = []string{"title", "tagline", "description", "video_url", "code_url"}

const entrySelect = `
	SELECT e.team_id, e.event_id, e.title, e.tagline, e.description, e.video_url, e.code_url,
	       (e.screenshot IS NOT NULL AND octet_length(e.screenshot) > 0),
	       e.screenshot_type, e.updated_at, COALESCE(m.display_name, '')
	  FROM event_entries e
	  LEFT JOIN event_members m
	    ON m.event_id = e.event_id AND m.user_id = e.updated_by`

type entryScanner interface {
	Scan(dest ...any) error
}

func scanEventEntry(row entryScanner) (EventEntry, error) {
	var e EventEntry
	err := row.Scan(&e.TeamID, &e.EventID, &e.Title, &e.Tagline, &e.Description, &e.VideoURL, &e.CodeURL,
		&e.HasScreenshot, &e.ScreenshotType, &e.UpdatedAt, &e.UpdatedByName)
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
// present in fields (column names from entryTextColumns). Omitted fields
// stay as stored. updated_at and updated_by always move to this write.
func UpsertEventEntryText(ctx context.Context, q Querier, eventID, teamID, userID string, fields map[string]string) error {
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
