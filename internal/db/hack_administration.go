package db

import (
	"context"
	"database/sql"
	"errors"
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

// RemoveCoOrganiser drops an organiser (Grok security review, SIM-340):
// their own event_scores rows deleted outright -- TestHackOrganisersCanJudge
// AndRemovedOrganiserScoresStopCounting already establishes "a removed
// organiser's scores stop counting" as the intended behaviour, unlike a
// voluntary leave's frozen-but-kept scores, so removal deletes rather than
// freezes -- plus LeaveMembership's shared cleanup (name reservation,
// pending team requests, unsent announcement/entry mail, the member row).
// Previously a single atomic DELETE that skipped the mail cleanup only.
func RemoveCoOrganiser(ctx context.Context, q *sql.Tx, eventID, actorID, userID string) error {
	// Serialise removals so a legacy event without its creator cannot lose
	// both remaining organisers through concurrent requests.
	var createdBy sql.NullString
	if err := q.QueryRowContext(ctx, `SELECT created_by FROM events WHERE id=$1 FOR UPDATE`, eventID).Scan(&createdBy); err != nil {
		return err
	}
	var role string
	err := q.QueryRowContext(ctx, `SELECT role FROM event_members WHERE event_id=$1 AND user_id=$2 FOR UPDATE`, eventID, userID).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return sql.ErrNoRows
	}
	if err != nil {
		return err
	}
	if role != "organiser" || (createdBy.Valid && createdBy.String == userID) {
		return sql.ErrNoRows
	}
	var actorIsOrganiser, anotherOrganiser bool
	if err := q.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM event_members WHERE event_id=$1 AND user_id=$2 AND role='organiser'),
		       EXISTS (SELECT 1 FROM event_members WHERE event_id=$1 AND role='organiser' AND user_id<>$3)`,
		eventID, actorID, userID).Scan(&actorIsOrganiser, &anotherOrganiser); err != nil {
		return err
	}
	if !actorIsOrganiser || !anotherOrganiser {
		return sql.ErrNoRows
	}
	if _, err := q.ExecContext(ctx, `DELETE FROM event_scores WHERE event_id=$1 AND judge_id=$2`, eventID, userID); err != nil {
		return err
	}
	_, err = LeaveMembership(ctx, q, eventID, userID)
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
