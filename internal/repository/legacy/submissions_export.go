package legacy

import (
	"context"
	"database/sql"
	"time"
)

type Submission struct {
	ID         int64
	SiteID     string
	Collection string
	Data       []byte
	CreatedAt  time.Time
}

// ListSubmissionsElsewhere returns every list item this person submitted to
// a site that is not theirs, oldest first.
func ListSubmissionsElsewhere(ctx context.Context, database *sql.DB, userID string) ([]Submission, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT ci.id, ci.site_id::text, ci.collection, ci.data, ci.created_at
		  FROM collection_items ci JOIN sites s ON s.id = ci.site_id
		 WHERE ci.submitted_by = $1 AND s.user_id IS DISTINCT FROM $1
		 ORDER BY ci.created_at, ci.id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Submission
	for rows.Next() {
		var s Submission
		if err := rows.Scan(&s.ID, &s.SiteID, &s.Collection, &s.Data, &s.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
