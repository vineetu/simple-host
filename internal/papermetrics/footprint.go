package papermetrics

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"path/filepath"
	"strings"
	"time"
)

// siteFootprint is one live site's weekly row. It carries no site, owner or
// name: the paper needs distributions, and an anonymous row cannot be tied
// back to anyone.
type siteFootprint struct {
	versions                                        int
	liveFiles                                       int
	liveBytes, totalBytes                           int64
	htmlBytes, jsBytes, cssBytes, mediaBytes, other int64
	sqliteBytes, fileStoreBytes, kvBytes            int64
	kvRows, legacyRows                              int64
	views30d                                        int64
	ageDays                                         int
	daysSinceUpdate                                 int
}

// weekStart is the Monday (UTC) of t's week.
func weekStart(t time.Time) time.Time {
	t = t.UTC().Truncate(24 * time.Hour)
	wd := (int(t.Weekday()) + 6) % 7
	return t.AddDate(0, 0, -wd)
}

// weeklyFootprint writes one anonymous row per live site for this week,
// once. The disk walk is the same one the admin page's usage report does.
func (c *Collector) weeklyFootprint(ctx context.Context, now time.Time) error {
	if c.dataDir == "" {
		return nil
	}
	week := weekStart(now).Format("2006-01-02")
	var done bool
	if err := c.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM paper_site_footprint WHERE week = $1)`, week).Scan(&done); err != nil {
		return err
	}
	if done {
		return nil
	}
	rows, err := c.db.QueryContext(ctx, `
		SELECT s.user_id::text, s.name,
			EXTRACT(day FROM now() - s.created_at)::int,
			EXTRACT(day FROM now() - s.updated_at)::int,
			COALESCE((SELECT sum(views) FROM site_view_hourly v WHERE v.site_id = s.id AND v.class = 'person' AND v.hour >= now() - interval '30 days'), 0),
			COALESCE((SELECT count(*) FROM site_storage_kv k WHERE k.site_id = s.id), 0),
			COALESCE((SELECT sum(pg_column_size(value)) FROM site_storage_kv k WHERE k.site_id = s.id), 0),
			COALESCE((SELECT count(*) FROM collection_items ci WHERE ci.site_id = s.id AND ci.deleted_at IS NULL), 0)
		FROM sites s WHERE s.deleted_at IS NULL`)
	if err != nil {
		return err
	}
	sites := map[string]*siteFootprint{}
	for rows.Next() {
		var user, name string
		f := &siteFootprint{}
		if err := rows.Scan(&user, &name, &f.ageDays, &f.daysSinceUpdate, &f.views30d, &f.kvRows, &f.kvBytes, &f.legacyRows); err != nil {
			rows.Close()
			return err
		}
		sites[user+"/"+name] = f
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	root := filepath.Join(c.dataDir, "by-id")
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // an unreadable corner is not worth failing the snapshot
		}
		rel, _ := filepath.Rel(root, path)
		seg := strings.Split(filepath.ToSlash(rel), "/")
		if len(seg) < 3 {
			return nil
		}
		f := sites[seg[0]+"/"+seg[1]]
		if f == nil {
			if d.IsDir() && len(seg) == 2 {
				return filepath.SkipDir // a deleted or unknown site
			}
			return nil
		}
		if d.IsDir() {
			if len(seg) == 3 && len(seg[2]) > 1 && seg[2][0] == 'v' && isDigits(seg[2][1:]) {
				f.versions++
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil //nolint:nilerr
		}
		size := info.Size()
		f.totalBytes += size
		switch {
		case seg[2] == "current" && len(seg) > 3:
			f.liveBytes += size
			f.liveFiles++
			switch strings.ToLower(strings.TrimPrefix(filepath.Ext(path), ".")) {
			case "html", "htm":
				f.htmlBytes += size
			case "js", "mjs":
				f.jsBytes += size
			case "css":
				f.cssBytes += size
			case "png", "jpg", "jpeg", "gif", "webp", "avif", "svg", "ico", "mp4", "webm", "mp3", "wav", "woff", "woff2", "ttf", "otf":
				f.mediaBytes += size
			default:
				f.other += size
			}
		case seg[2] == "runtime" && len(seg) > 4 && seg[3] == "sqlite":
			f.sqliteBytes += size
		case seg[2] == "runtime" && len(seg) > 4 && seg[3] == "files":
			f.fileStoreBytes += size
		}
		return nil
	})

	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	for _, f := range sites {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO paper_site_footprint (week, versions, live_files, live_bytes, total_bytes,
				html_bytes, js_bytes, css_bytes, media_bytes, other_bytes,
				sqlite_bytes, file_store_bytes, kv_bytes, kv_rows, legacy_rows, views_30d, age_days, days_since_update)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)`,
			week, f.versions, f.liveFiles, f.liveBytes, f.totalBytes,
			f.htmlBytes, f.jsBytes, f.cssBytes, f.mediaBytes, f.other,
			f.sqliteBytes, f.fileStoreBytes, f.kvBytes, f.kvRows, f.legacyRows, f.views30d, f.ageDays, f.daysSinceUpdate); err != nil {
			return fmt.Errorf("insert footprint: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	log.Printf("paper metrics: footprint for week of %s, %d sites", week, len(sites))
	return nil
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}
