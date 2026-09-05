package analytics

import (
	"context"
	"fmt"
	"log"
)

// Rebuild discards every v2 aggregate and re-ingests the configured log from
// byte zero, so history is rewritten with the current classifier rather than
// only being fixed going forward.
//
// This is needed because the pre-classifier aggregates counted the loopback
// monitoring probe as real pageviews -- around 2,880 views and one "visitor"
// per site per day, which swamped the genuine traffic. Reclassifying only new
// lines would leave a permanent step in every chart.
//
// It reads only the live log file. Rotated archives are not replayed: whatever
// has already scrolled out of the active log is gone from the rebuild, and the
// caller is told how far back the data actually reaches.
//
// Safe to run against a live server: the ingest loop's own transactions either
// run before the truncate (and get discarded with it) or after (and are simply
// re-derived), and both write through the same ON CONFLICT upserts.
func (i *Ingester) Rebuild(ctx context.Context) error {
	if i.logPath == "" {
		return fmt.Errorf("no analytics log configured")
	}

	tx, err := i.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin reset tx: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM site_view_hourly`); err != nil {
		return fmt.Errorf("clear views: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM site_visitor_hourly`); err != nil {
		return fmt.Errorf("clear visitors: %w", err)
	}
	// Not ip_country_ranges: that is reference data loaded by ip-country-load,
	// not something the log can rebuild.
	if _, err := tx.ExecContext(ctx, `DELETE FROM site_geo_daily`); err != nil {
		return fmt.Errorf("clear geo: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM analytics_ingest_state WHERE logfile = $1`, i.logPath); err != nil {
		return fmt.Errorf("reset ingest state: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit reset: %w", err)
	}

	// Drain the whole file. Each pass consumes at most maxLinesPerRun lines and
	// persists its offset, so this terminates once the offset stops advancing.
	var lastOffset int64 = -1
	for pass := 1; ; pass++ {
		if err := i.runOnce(ctx); err != nil {
			return fmt.Errorf("pass %d: %w", pass, err)
		}
		offset, _, err := i.loadState(ctx)
		if err != nil {
			return fmt.Errorf("pass %d: read state: %w", pass, err)
		}
		if offset == lastOffset {
			break
		}
		log.Printf("analytics rebuild: pass %d, offset %d", pass, offset)
		lastOffset = offset
	}
	return nil
}
