package hackevents

import (
	"context"
	"time"

	"github.com/vsriram/simple-host/host/sqlkit"
	"github.com/vsriram/simple-host/products/hack/model"
)

type EventStorageUsage = model.EventStorageUsage

// SetOrganiserInvite stores the event's one organiser invitation, issued by
// actor (the caller checked they organise it), for 7 days.
func SetOrganiserInvite(ctx context.Context, q sqlkit.Querier, eventID, actor, hash string) (time.Time, error) {
	var expires time.Time
	err := q.QueryRowContext(ctx, `INSERT INTO event_organiser_invites (event_id,token_hash,created_by,expires_at)
 VALUES ($1,$2,$3,now()+interval '7 days')
 ON CONFLICT (event_id) DO UPDATE SET token_hash=EXCLUDED.token_hash, created_by=EXCLUDED.created_by, expires_at=EXCLUDED.expires_at RETURNING expires_at`, eventID, hash, actor).Scan(&expires)
	return expires, err
}

func DeleteOrganiserInvite(ctx context.Context, q sqlkit.Querier, eventID string) error {
	_, err := q.ExecContext(ctx, `DELETE FROM event_organiser_invites WHERE event_id=$1`, eventID)
	return err
}

// OrganiserInviteEvent is the event of an unexpired invitation and who
// issued it (the caller checks they still organise it); lock locks the
// invitation.
func OrganiserInviteEvent(ctx context.Context, q sqlkit.Querier, hash string, lock bool) (Event, string, error) {
	query := `SELECT e.slug, i.created_by FROM event_organiser_invites i JOIN events e ON e.id=i.event_id
 WHERE i.token_hash=$1 AND i.expires_at>now()`
	if lock {
		query += ` FOR UPDATE OF i`
	}
	var slug, issuer string
	if err := q.QueryRowContext(ctx, query, hash).Scan(&slug, &issuer); err != nil {
		return Event{}, "", err
	}
	ev, err := GetEventBySlug(ctx, q, slug)
	return ev, issuer, err
}
