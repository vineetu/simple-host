package hackevents

import (
	"context"

	"github.com/vsriram/simple-host/host/sqlkit"
)

func NewUUID(ctx context.Context, q sqlkit.Querier) (string, error) {
	var id string
	err := q.QueryRowContext(ctx, `SELECT gen_random_uuid()`).Scan(&id)
	return id, err
}
