package hackevents

import (
	"context"

	"github.com/vsriram/simple-host/internal/repository/sqlkit"
)

// PendingContentMail counts each existing partial pending index, including delayed retries.
func PendingContentMail(ctx context.Context, q sqlkit.Querier) (int64, error) {
	var n int64
	err := q.QueryRowContext(ctx, `SELECT
 (SELECT count(*) FROM event_announcement_deliveries WHERE sent_at IS NULL) +
 (SELECT count(*) FROM event_entry_receipts WHERE sent_at IS NULL) +
 (SELECT count(*) FROM event_notices WHERE email_state='pending')`).Scan(&n)
	return n, err
}
