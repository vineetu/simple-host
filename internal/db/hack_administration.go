package db

import (
	"context"
	"database/sql"
	"time"
)

func SetOrganiserInvite(ctx context.Context, q Querier, eventID, actor, hash string) (time.Time, error) {
	var expires time.Time
	err := q.QueryRowContext(ctx, `INSERT INTO event_organiser_invites (event_id,token_hash,created_by,expires_at)
 SELECT $1,$2,$3,now()+interval '7 days' WHERE EXISTS
 (SELECT 1 FROM event_members WHERE event_id=$1 AND user_id=$3 AND role='organiser')
 ON CONFLICT (event_id) DO UPDATE SET token_hash=EXCLUDED.token_hash, created_by=EXCLUDED.created_by, expires_at=EXCLUDED.expires_at RETURNING expires_at`, eventID, hash, actor).Scan(&expires)
	return expires, err
}

func DeleteOrganiserInvite(ctx context.Context, q Querier, eventID string) error {
	_, err := q.ExecContext(ctx, `DELETE FROM event_organiser_invites WHERE event_id=$1`, eventID)
	return err
}

// OrganiserInviteEvent refuses expired invitations and invitations whose issuer
// no longer organises the event. A redeeming transaction locks the invitation.
func OrganiserInviteEvent(ctx context.Context, q Querier, hash string, lock bool) (Event, error) {
	query := `SELECT e.slug FROM event_organiser_invites i JOIN events e ON e.id=i.event_id
 JOIN event_members m ON m.event_id=i.event_id AND m.user_id=i.created_by AND m.role='organiser'
 WHERE i.token_hash=$1 AND i.expires_at>now()`
	if lock {
		query += ` FOR UPDATE OF i, m`
	}
	var slug string
	if err := q.QueryRowContext(ctx, query, hash).Scan(&slug); err != nil {
		return Event{}, err
	}
	return GetEventBySlug(ctx, q, slug)
}

func RemoveCoOrganiser(ctx context.Context, q *sql.Tx, eventID, actorID, userID string) error {
	// Serialise removals so a legacy event without its creator cannot lose
	// both remaining organisers through concurrent requests.
	var id string
	if err := q.QueryRowContext(ctx, `SELECT id FROM events WHERE id=$1 FOR UPDATE`, eventID).Scan(&id); err != nil {
		return err
	}
	result, err := q.ExecContext(ctx, `DELETE FROM event_members m USING events e
 WHERE m.event_id=e.id AND e.id=$1 AND m.user_id=$2 AND m.role='organiser'
 AND e.created_by IS DISTINCT FROM m.user_id
	AND EXISTS (SELECT 1 FROM event_members actor WHERE actor.event_id=e.id AND actor.user_id=$3 AND actor.role='organiser')
 AND EXISTS (SELECT 1 FROM event_members other WHERE other.event_id=e.id AND other.role='organiser' AND other.user_id<>m.user_id)`, eventID, userID, actorID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n == 0 {
		return sql.ErrNoRows
	}
	return err
}

func RenameEventTeam(ctx context.Context, q Querier, eventID, slug, name string) error {
	result, err := q.ExecContext(ctx, `UPDATE event_teams SET name=$3 WHERE event_id=$1 AND slug=$2`, eventID, slug, name)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n == 0 {
		return sql.ErrNoRows
	}
	return err
}

type EventStorageUsage struct {
	Sites           int64 `json:"sites"`
	Versions        int64 `json:"versions"`
	SavedDataBytes  int64 `json:"saved_data_bytes"`
	HistoryBytes    int64 `json:"history_bytes"`
	ScreenshotBytes int64 `json:"screenshot_bytes"`
	FileBytes       int64 `json:"file_bytes"`
}

// EventStorage counts this event's holding account only, including retained
// site records. File bytes are measured separately by the storage handler.
func EventStorage(ctx context.Context, q Querier, ev Event) (EventStorageUsage, error) {
	var u EventStorageUsage
	err := q.QueryRowContext(ctx, `SELECT count(*),COALESCE(sum(data_bytes),0),COALESCE(sum(history_bytes),0),
 (SELECT count(*) FROM versions v JOIN sites s ON s.id=v.site_id WHERE s.user_id=$1),
 (SELECT COALESCE(sum(octet_length(screenshot)),0) FROM event_entries WHERE event_id=$2)
 FROM sites WHERE user_id=$1`, ev.AccountID, ev.ID).Scan(&u.Sites, &u.SavedDataBytes, &u.HistoryBytes, &u.Versions, &u.ScreenshotBytes)
	return u, err
}
