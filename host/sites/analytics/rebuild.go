package analytics

import (
	"bufio"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
)

// Rebuild discards every v2 aggregate and re-ingests the configured log and
// its rotated archives from byte zero, so history is rewritten with the
// current classifier rather than only being fixed going forward.
//
// This is needed because the pre-classifier aggregates counted the loopback
// monitoring probe as real pageviews -- around 2,880 views and one "visitor"
// per site per day, which swamped the genuine traffic. Reclassifying only new
// lines would leave a permanent step in every chart.
//
// It replays every file ReplayFiles lists, oldest first: logrotate's
// `<log>.N.gz ... <log>.2.gz, <log>.1` or Caddy's `access-<timestamp>.log(.gz)`
// rolls, then the live log. Archives are kept about 30 days (the privacy
// promise for raw logs), so that is how far back a rebuild reaches; anything
// older than the oldest archive is lost from the aggregates. The caller is
// told the range the rebuilt data covers.
//
// Stop the server first: its ingest loop shares the position in
// analytics_ingest_state with the rebuild, and a pass of each racing on it
// would count the same lines twice.
func (i *Ingester) Rebuild(ctx context.Context) error {
	if i.logPath == "" {
		return fmt.Errorf("no analytics log configured")
	}
	files := ReplayFiles(i.logPath)
	var liveInode int64 = -1
	if len(files) > 0 && files[len(files)-1] == i.logPath {
		info, err := os.Stat(i.logPath)
		if err != nil {
			return fmt.Errorf("stat log: %w", err)
		}
		liveInode = fileInode(info)
		files = files[:len(files)-1]
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
	if _, err := tx.ExecContext(ctx, `DELETE FROM site_page_daily`); err != nil {
		return fmt.Errorf("clear pages: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM site_referrer_daily`); err != nil {
		return fmt.Errorf("clear referrers: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM site_traffic_daily`); err != nil {
		return fmt.Errorf("clear site traffic: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM traffic_daily`); err != nil {
		return fmt.Errorf("clear traffic: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM analytics_ingest_state WHERE logfile = $1`, i.logPath); err != nil {
		return fmt.Errorf("reset ingest state: %w", err)
	}
	if liveInode >= 0 {
		// Point the position at the live file's start. Without this, the
		// ingest pass below would see "rotated" and read .1 a second time.
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO analytics_ingest_state (logfile, offset_bytes, inode, updated_at)
			VALUES ($1, 0, $2, now())
		`, i.logPath, liveInode); err != nil {
			return fmt.Errorf("reset ingest state: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit reset: %w", err)
	}

	var lastInode, lastSize int64 = -1, 0
	for _, path := range files {
		inode, size, err := i.replayArchive(ctx, path)
		if err != nil {
			return fmt.Errorf("replay %s: %w", path, err)
		}
		if inode >= 0 {
			lastInode, lastSize = inode, size
		}
	}
	if liveInode < 0 {
		// No live log yet (between a rotation and the first new line). Park
		// the position at the end of the newest archive, so the first ingest
		// once the log reappears finds it fully read (previousLog: matching
		// inode, offset at its end) instead of counting it again from 0.
		if lastInode >= 0 {
			if _, err := i.db.ExecContext(ctx, `
				INSERT INTO analytics_ingest_state (logfile, offset_bytes, inode, updated_at)
				VALUES ($1, $2, $3, now())
			`, i.logPath, lastSize, lastInode); err != nil {
				return fmt.Errorf("seed ingest state: %w", err)
			}
		}
		return nil
	}

	// Drain the live file. Each pass consumes at most maxLinesPerRun lines and
	// persists its offset, so this terminates once the offset stops advancing.
	var lastOffset int64 = -1
	for pass := 1; ; pass++ {
		if err := i.runOnce(ctx); err != nil {
			return fmt.Errorf("pass %d: %w", pass, err)
		}
		offset, _, _, err := i.loadState(ctx)
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

// replayArchive ingests one rotated file (plain or .gz) in maxLinesPerRun
// chunks, without touching the live ingest position. It returns the file's
// inode and the uncompressed bytes read (inode -1 when the file is gone).
func (i *Ingester) replayArchive(ctx context.Context, path string) (inode, size int64, err error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return -1, 0, nil // rotated away while we worked
		}
		return -1, 0, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return -1, 0, err
	}
	inode = fileInode(info)
	var src io.Reader = f
	if strings.HasSuffix(path, ".gz") {
		zr, err := gzip.NewReader(f)
		if err != nil {
			return -1, 0, err
		}
		defer zr.Close()
		src = zr
	}
	r := bufio.NewReaderSize(src, 256*1024)
	total := 0
	for {
		lines, n, err := readChunk(r, maxLinesPerRun)
		if err != nil {
			return -1, 0, err
		}
		if len(lines) == 0 {
			break
		}
		if err := i.commitLines(ctx, lines, false, 0, 0); err != nil {
			return -1, 0, err
		}
		total += len(lines)
		size += n
	}
	log.Printf("analytics rebuild: %s, %d lines", path, total)
	return inode, size, nil
}
