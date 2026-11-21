package hackevents

import (
	"context"
	"database/sql"
	"github.com/vsriram/simple-host/host/sqlkit"
	"github.com/vsriram/simple-host/products/hack/model"
	"time"
)

type EventStorageUsage = model.EventStorageUsage

func SetOrganiserInvite(ctx context.Context, q sqlkit.Querier, eventID, actor, hash string) (time.Time, error) {
	var expires time.Time
	err := q.QueryRowContext(ctx, `INSERT INTO event_organiser_invites (event_id,token_hash,created_by,expires_at)
 SELECT $1,$2,$3,now()+interval '7 days' WHERE EXISTS
 (SELECT 1 FROM event_members WHERE event_id=$1 AND user_id=$3 AND role='organiser')
 ON CONFLICT (event_id) DO UPDATE SET token_hash=EXCLUDED.token_hash, created_by=EXCLUDED.created_by, expires_at=EXCLUDED.expires_at RETURNING expires_at`, eventID, hash, actor).Scan(&expires)
	return expires, err
}

func DeleteOrganiserInvite(ctx context.Context, q sqlkit.Querier, eventID string) error {
	_, err := q.ExecContext(ctx, `DELETE FROM event_organiser_invites WHERE event_id=$1`, eventID)
	return err
}

// OrganiserInviteEvent refuses expired invitations and invitations whose issuer no longer organises the event.
func OrganiserInviteEvent(ctx context.Context, q sqlkit.Querier, hash string, lock bool) (Event, error) {
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

func RenameEventTeam(ctx context.Context, q sqlkit.Querier, eventID, slug, name string) error {
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

func EventScreenshotBytes(ctx context.Context, q sqlkit.Querier, eventID string) (int64, error) {
	var n int64
	err := q.QueryRowContext(ctx, `SELECT COALESCE(sum(octet_length(screenshot)),0) FROM event_entries WHERE event_id=$1`, eventID).Scan(&n)
	return n, err
}
