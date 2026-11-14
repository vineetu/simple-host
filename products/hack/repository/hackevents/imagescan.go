package hackevents

import (
	"context"

	"github.com/vsriram/simple-host/internal/repository/sqlkit"
)

// ClearEventIcon removes an event's icon (the image bytes and their type),
// only while the stored bytes still hash to sha. Reports whether it did.
func ClearEventIcon(ctx context.Context, q sqlkit.Querier, eventID, sha string) (bool, error) {
	res, err := q.ExecContext(ctx, `UPDATE events SET icon_media_type = '', icon_bytes = NULL, updated_at = now()
		 WHERE id = $1 AND icon_bytes IS NOT NULL AND encode(sha256(icon_bytes), 'hex') = $2`, eventID, sha)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}
