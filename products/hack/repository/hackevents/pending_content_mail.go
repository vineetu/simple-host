package hackevents

import (
	"context"

	"github.com/vsriram/simple-host/host/sqlkit"
)

// PendingContentMail counts the announcement emails still to send, including delayed retries (the caller adds the receipts' and notices').
func PendingContentMail(ctx context.Context, q sqlkit.Querier) (int64, error) {
	var n int64
	err := q.QueryRowContext(ctx, `SELECT count(*) FROM event_announcement_deliveries WHERE sent_at IS NULL`).Scan(&n)
	return n, err
}
