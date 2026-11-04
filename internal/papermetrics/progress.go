package papermetrics

import (
	"context"
	"database/sql"
)

type metricExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

// Transactional writes become progress only after their commit succeeds.
func (c *Collector) execMetric(ctx context.Context, q metricExecutor, written *int64, query string, args ...any) (sql.Result, error) {
	res, err := q.ExecContext(ctx, query, args...)
	if err != nil {
		c.tracker.Fail("write metrics", err)
		return res, err
	}
	n, countErr := res.RowsAffected()
	if countErr != nil {
		c.tracker.Fail("count metric writes", countErr)
	}
	if written != nil {
		*written += n
	} else if n > 0 {
		c.tracker.Progress()
	}
	return res, err
}
func (c *Collector) commitMetrics(tx *sql.Tx, written int64) error {
	err := tx.Commit()
	if err != nil {
		c.tracker.Fail("commit metrics", err)
	} else if written > 0 {
		c.tracker.Progress()
	}
	return err
}
