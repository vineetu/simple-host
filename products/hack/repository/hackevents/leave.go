package hackevents

import (
	"context"
	"errors"

	"github.com/vsriram/simple-host/host/sqlkit"
	"github.com/vsriram/simple-host/products/hack/model"
)

type OtherOrganiser = model.OtherOrganiser

type FormerJudge = model.FormerJudge

// leaveQueuedSQL drops what was waiting on the leaver here, each taking
// (event id, user id).
var leaveQueuedSQL = []string{
	`DELETE FROM event_announcement_deliveries d USING event_announcements a
		WHERE d.announcement_id = a.id AND a.event_id = $1 AND d.user_id = $2 AND d.sent_at IS NULL`,
}

// DropQueuedForLeaver reserves the event's name and drops the announcement
// emails still waiting on someone leaving (hackentries.DropLeaver drops their
// entry receipts).
func DropQueuedForLeaver(ctx context.Context, q sqlkit.Querier, eventID, userID string) error {
	if err := ReserveEventName(ctx, q, eventID); err != nil {
		return err
	}
	return execForMember(ctx, q, leaveQueuedSQL, eventID, userID)
}

// RemoveOrganiserJudge takes the person out of the event's organiser_judges.
func RemoveOrganiserJudge(ctx context.Context, q sqlkit.Querier, eventID, userID string) error {
	_, err := q.ExecContext(ctx, `UPDATE events SET organiser_judges=array_remove(organiser_judges,$2::uuid) WHERE id=$1`, eventID, userID)
	return err
}

func execForMember(ctx context.Context, q sqlkit.Querier, stmts []string, eventID, userID string) error {
	for _, stmt := range stmts {
		if _, err := q.ExecContext(ctx, stmt, eventID, userID); err != nil {
			return err
		}
	}
	return nil
}

var ErrHackCannotRemoveOrganiser = errors.New("cannot remove organiser")
