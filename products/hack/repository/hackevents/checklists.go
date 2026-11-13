package hackevents

import (
	"context"
	"github.com/vsriram/simple-host/internal/repository/sqlkit"
)

// ChecklistMarks reads shared dismissals and only the caller's personal acknowledgements.
func ChecklistMarks(ctx context.Context, q sqlkit.Querier, eventID, subject string) (map[string]bool, map[string]string, error) {
	marks, acks := map[string]bool{}, map[string]string{}
	rows, err := queryContext(ctx, q, `SELECT item_id FROM event_checklist_dismissals WHERE event_id=$1`, eventID)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, nil, err
		}
		marks[id] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, nil, err
	}
	rows, err = queryContext(ctx, q, `SELECT item_id,version FROM event_checklist_acknowledgements WHERE event_id=$1 AND (subject_id='event' OR ($2<>'' AND subject_id=$2))`, eventID, subject)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, v string
		if err = rows.Scan(&id, &v); err != nil {
			return nil, nil, err
		}
		acks[id] = v
	}
	return marks, acks, rows.Err()
}

func SetChecklistNotNeeded(ctx context.Context, q sqlkit.Querier, eventID, itemID, actorID string, marked bool) error {
	if !marked {
		_, err := q.ExecContext(ctx, `DELETE FROM event_checklist_dismissals WHERE event_id=$1 AND item_id=$2`, eventID, itemID)
		return err
	}
	_, err := q.ExecContext(ctx, `INSERT INTO event_checklist_dismissals(event_id,item_id,marked_by) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, eventID, itemID, actorID)
	return err
}

func AcknowledgeChecklist(ctx context.Context, q sqlkit.Querier, eventID, subject, itemID, version, actorID string) error {
	_, err := q.ExecContext(ctx, `INSERT INTO event_checklist_acknowledgements(event_id,subject_id,item_id,version,acknowledged_by) VALUES($1,$2,$3,$4,$5) ON CONFLICT(event_id,subject_id,item_id) DO UPDATE SET version=EXCLUDED.version, acknowledged_by=EXCLUDED.acknowledged_by, acknowledged_at=now()`, eventID, subject, itemID, version, actorID)
	return err
}

func DeletePersonalChecklistAcknowledgements(ctx context.Context, q sqlkit.Querier, eventID, userID string) error {
	_, err := q.ExecContext(ctx, `DELETE FROM event_checklist_acknowledgements WHERE event_id=$1 AND subject_id=$2`, eventID, userID)
	return err
}
