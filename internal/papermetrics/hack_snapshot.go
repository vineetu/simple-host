package papermetrics

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"
)

// These columns are also the export contract. Only stage is text, constrained
// to the fixed lifecycle enum by the table. Identifiers stay in local variables.
var hackEventCols = []string{
	"snapshot_day", "event_key", "run_id", "synthetic", "stage", "created_week", "duration_hours", "expected_participants",
	"participants", "organisers", "judges", "applications", "applications_approved",
	"teams", "teams_with_entry", "teams_kept_after_leave", "team_size_p50", "team_size_max",
	"team_sites", "team_site_bytes_sum", "team_site_bytes_p50", "team_site_versions_sum", "event_storage_bytes",
	"submissions", "entries_with_code_url", "entries_with_video", "scores", "score_criteria", "assignments", "conflicts",
	"votes", "announcements", "results_published", "deploys_total", "minutes_create_to_first_deploy",
}

// Every scalar is queried separately: a missing source column only leaves that
// metric NULL; other columns and events are still snapshotted.
var hackEventQueries = map[string]string{
	"participants":           `SELECT count(*) FROM event_members WHERE event_id=$1 AND role='participant' AND approval_status='approved'`,
	"organisers":             `SELECT count(*) FROM event_members WHERE event_id=$1 AND role='organiser'`,
	"judges":                 `SELECT count(*) FROM event_members WHERE event_id=$1 AND role='judge'`,
	"applications":           `SELECT count(*) FROM event_members WHERE event_id=$1 AND role='participant'`,
	"applications_approved":  `SELECT count(*) FROM event_members WHERE event_id=$1 AND role='participant' AND approval_status='approved'`,
	"teams":                  `SELECT count(*) FROM event_teams WHERE event_id=$1`,
	"teams_with_entry":       `SELECT count(*) FROM event_teams t WHERE event_id=$1 AND EXISTS (SELECT 1 FROM event_entries e WHERE e.team_id=t.id)`,
	"teams_kept_after_leave": `SELECT count(*) FROM event_teams WHERE event_id=$1 AND kept_after_leave`,
	"team_size_p50":          `SELECT COALESCE(percentile_disc(0.5) WITHIN GROUP (ORDER BY n),0) FROM (SELECT count(m.user_id) n FROM event_teams t LEFT JOIN event_members m ON m.team_id=t.id WHERE t.event_id=$1 GROUP BY t.id) sizes`,
	"team_size_max":          `SELECT COALESCE(max(n),0) FROM (SELECT count(m.user_id) n FROM event_teams t LEFT JOIN event_members m ON m.team_id=t.id WHERE t.event_id=$1 GROUP BY t.id) sizes`,
	"submissions":            `SELECT count(*) FROM event_entries WHERE event_id=$1`,
	"entries_with_code_url":  `SELECT count(*) FROM event_entries WHERE event_id=$1 AND code_url<>''`,
	"entries_with_video":     `SELECT count(*) FROM event_entries WHERE event_id=$1 AND video_url<>''`,
	"scores":                 `SELECT count(*) FROM event_scores WHERE event_id=$1`,
	"score_criteria":         `SELECT count(*) FROM rubric_criteria WHERE event_id=$1`,
	"assignments":            `SELECT count(*) FROM event_assignments WHERE event_id=$1`,
	"conflicts":              `SELECT count(*) FROM event_conflicts WHERE event_id=$1`,
	"votes":                  `SELECT count(*) FROM event_votes WHERE event_id=$1`,
	"announcements":          `SELECT count(*) FROM event_announcements WHERE event_id=$1`,
	"results_published":      `SELECT results_published_at IS NOT NULL OR EXISTS(SELECT 1 FROM event_results r WHERE r.event_id=e.id) FROM events e WHERE id=$1`,
	"duration_hours":         `SELECT EXTRACT(epoch FROM ends_at-starts_at)/3600 FROM events WHERE id=$1`,
	"expected_participants":  `SELECT expected_participants FROM events WHERE id=$1`,
	"minutes_create_to_first_deploy": `SELECT round(EXTRACT(epoch FROM (
  (SELECT min(v.created_at) FROM versions v JOIN sites s ON s.id=v.site_id
   WHERE s.user_id=e.account_id AND s.name<>e.id::text AND s.deleted_at IS NULL AND v.status IN ('active','ready'))
  - e.starts_at))/600)*10 FROM events e WHERE e.id=$1`,
}

type hackSnapshotEvent struct {
	id, account string
	values      map[string]any
}

func (c *Collector) hackSnapshot(ctx context.Context, now time.Time) (resultErr error) {
	defer func() {
		if resultErr != nil {
			c.tracker.Fail("hackSnapshot", resultErr)
		}
	}()
	if err := c.ensureHackKeys(ctx); err != nil {
		return err
	}
	run, err := c.ensureRun(ctx)
	if err != nil {
		return err
	}
	if err := c.hackTimestampPhases(ctx); err != nil {
		c.tracker.Fail("paper metrics timestamp phases", err)
		log.Printf("paper metrics timestamp phases: %v", err)
	}
	day := now.UTC().Format("2006-01-02")
	var done bool
	if err := c.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM paper_hack_event WHERE snapshot_day=$1)`, day).Scan(&done); err != nil {
		return err
	}
	if done {
		return nil
	}
	rows, err := c.db.QueryContext(ctx, `SELECT e.id::text,e.account_id::text,k.event_key,k.synthetic,e.stage,date_trunc('week',e.created_at AT TIME ZONE 'UTC')::date FROM events e JOIN paper_hack_key k ON k.event_id=e.id`)
	if err != nil {
		return err
	}
	var events []hackSnapshotEvent
	for rows.Next() {
		e := hackSnapshotEvent{values: map[string]any{"snapshot_day": day}}
		var key int64
		var synthetic bool
		var stage string
		var week time.Time
		if err := rows.Scan(&e.id, &e.account, &key, &synthetic, &stage, &week); err != nil {
			c.tracker.Fail("scan Hack metadata", err)
			rows.Close()
			return err
		}
		e.values["event_key"], e.values["run_id"], e.values["synthetic"], e.values["stage"], e.values["created_week"] = key, run, synthetic, stage, week
		events = append(events, e)
	}
	rows.Close()
	if rows.Err() != nil {
		c.tracker.Fail("read Hack metadata", rows.Err())
		return rows.Err()
	}
	for _, e := range events {
		for col, query := range hackEventQueries {
			var value any
			if err := c.db.QueryRowContext(ctx, query, e.id).Scan(&value); err != nil {
				c.tracker.Fail("Hack column "+col, err)
				log.Printf("paper metrics Hack column %s: %v", col, err)
				continue
			}
			e.values[col] = value
		}
		c.hackSiteSnapshot(ctx, e)
	}
	var written int64
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	// Keep daily history so stable event keys can be joined across snapshots.
	for _, e := range events {
		values := make([]any, len(hackEventCols))
		marks := make([]string, len(hackEventCols))
		for i, col := range hackEventCols {
			values[i] = e.values[col]
			marks[i] = fmt.Sprintf("$%d", i+1)
		}
		if _, err := c.execMetric(ctx, tx, &written, `INSERT INTO paper_hack_event (`+strings.Join(hackEventCols, ",")+`) VALUES (`+strings.Join(marks, ",")+`) ON CONFLICT (snapshot_day,event_key) DO NOTHING`, values...); err != nil {
			return err
		}
	}
	return c.commitMetrics(tx, written)
}

func (c *Collector) hackSiteSnapshot(ctx context.Context, e hackSnapshotEvent) {
	rows, err := c.db.QueryContext(ctx, `SELECT name FROM sites WHERE user_id=$1 AND deleted_at IS NULL`, e.account)
	if err != nil {
		c.tracker.Fail("paper metrics Hack sites", err)
		log.Printf("paper metrics Hack sites: %v", err)
		return
	}
	sites := map[string]*siteFootprint{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			c.tracker.Fail("scan Hack metadata", err)
			rows.Close()
			return
		}
		sites[e.account+"/"+name] = &siteFootprint{}
	}
	rows.Close()
	if rows.Err() != nil {
		c.tracker.Fail("read Hack metadata", rows.Err())
		return
	}
	c.walkFootprints(sites)
	var storage, versions, teamVersions, teamBytes int64
	var sizes []int64
	for key, f := range sites {
		storage += f.sqliteBytes + f.fileStoreBytes
		versions += int64(f.versions)
		if key == e.account+"/"+e.id {
			continue
		} // custom event website
		sizes = append(sizes, f.totalBytes)
		teamBytes += f.totalBytes
		teamVersions += int64(f.versions)
	}
	e.values["team_sites"] = len(sizes)
	if c.dataDir != "" {
		e.values["team_site_bytes_sum"], e.values["team_site_versions_sum"], e.values["deploys_total"] = teamBytes, teamVersions, versions
		sort.Slice(sizes, func(i, j int) bool { return sizes[i] < sizes[j] })
		var median int64
		if len(sizes) > 0 {
			median = sizes[(len(sizes)-1)/2]
		}
		e.values["team_site_bytes_p50"] = median
		var kv int64
		if err := c.db.QueryRowContext(ctx, `SELECT COALESCE(sum(pg_column_size(k.value)),0) FROM site_storage_kv k JOIN sites s ON s.id=k.site_id WHERE s.user_id=$1 AND s.deleted_at IS NULL`, e.account).Scan(&kv); err == nil {
			e.values["event_storage_bytes"] = storage + kv
		} else {
			c.tracker.Fail("paper metrics Hack storage", err)
			log.Printf("paper metrics Hack storage: %v", err)
		}
	}
}

var hackDailyMetrics = []struct{ name, query string }{
	{"paper_tables_bytes", `SELECT COALESCE(sum(pg_total_relation_size(oid)),0) FROM pg_class WHERE relnamespace=current_schema()::regnamespace AND relkind='r' AND relname LIKE 'paper\_%'`},
	{"hack_events_total", `SELECT count(*) FROM events`},
	{"hack_events_created_day", `SELECT count(*) FROM events WHERE created_at >= $1::date AND created_at < $1::date+1`},
	{"hack_participants_total", `SELECT count(*) FROM event_members WHERE role='participant' AND approval_status='approved'`},
	{"hack_judges_total", `SELECT count(*) FROM event_members WHERE role='judge'`},
	{"hack_teams_total", `SELECT count(*) FROM event_teams`},
	{"hack_entries_total", `SELECT count(*) FROM event_entries`},
	{"hack_scores_total", `SELECT count(*) FROM event_scores`},
	{"hack_votes_total", `SELECT count(*) FROM event_votes`},
	{"hack_applications_day", `SELECT count(*) FROM event_members WHERE role='participant' AND joined_via='application' AND joined_at >= $1::date AND joined_at < $1::date+1`},
	{"hack_team_sites_live", `SELECT count(*) FROM sites s JOIN events e ON e.account_id=s.user_id WHERE s.deleted_at IS NULL AND s.name<>e.id::text`},
	{"hack_event_websites_published", `SELECT count(*) FROM events e JOIN sites s ON s.user_id=e.account_id AND s.name=e.id::text WHERE s.deleted_at IS NULL AND s.active_version>0`},
}

func init() {
	for _, stage := range []string{"draft", "open", "building", "closed", "judging", "results", "archived"} {
		hackDailyMetrics = append(hackDailyMetrics, struct{ name, query string }{"hack_events_by_stage_" + stage, `SELECT count(*) FROM events WHERE stage='` + stage + `'`})
	}
}
