package papermetrics

import (
	"context"
	"net/http"
	"strings"
)

var peopleColumns = map[string]bool{"expected_participants": true, "participants": true, "organisers": true, "judges": true, "applications": true, "applications_approved": true, "active_members": true, "votes": true, "team_size_p50": true, "team_size_max": true, "assignments": true, "scores": true, "conflicts": true}

func peopleMetric(metric string) bool {
	switch metric {
	case "accounts", "accounts_new", "accounts_with_live_site", "accounts_suspended", "visitor_signins_total", "connector_accounts_active_30d", "hack_participants_total", "hack_judges_total", "hack_votes_total", "hack_applications_day":
		return true
	}
	return false
}
func suppressPeople(t Table) Table {
	positions := map[string]int{}
	var indexes []int
	for i, col := range t.Columns {
		positions[col] = i
		if peopleColumns[col] {
			indexes = append(indexes, i)
		}
	}
	for _, i := range indexes {
		t.Columns = append(t.Columns, t.Columns[i]+"_suppressed")
	}
	for j, row := range t.Rows {
		hidden := map[int]bool{}
		for _, i := range indexes {
			col := t.Columns[i]
			if col != "assignments" && col != "scores" && col != "conflicts" {
				hidden[i] = row[i] != nil && ToInt(row[i]) < 5
			}
		}
		if judge, ok := positions["judges"]; ok && (row[judge] == nil || hidden[judge]) {
			for _, col := range []string{"assignments", "scores", "conflicts"} {
				if i, ok := positions[col]; ok {
					hidden[i] = row[i] != nil
				}
			}
		}
		a, hasA := positions["applications"]
		b, hasB := positions["applications_approved"]
		if hasA && hasB && (row[a] == nil || row[b] == nil || hidden[a] || hidden[b] || ToInt(row[a])-ToInt(row[b]) < 5) {
			hidden[a] = row[a] != nil
			hidden[b] = row[b] != nil
		}
		for _, i := range indexes {
			if hidden[i] {
				row[i] = nil
			}
			row = append(row, hidden[i])
		}
		t.Rows[j] = row
	}
	return t
}
func includeSmall(r *http.Request) bool { return r != nil && r.URL.Query().Get("include_small") == "1" }

// Apply to every per-event set, including phase and latency series. Eligibility
// uses the latest retained snapshot, never the private source-ID lookup.
const eligibleEvent = `($3 OR COALESCE((SELECT teams FROM paper_hack_event e WHERE e.event_key=t.event_key ORDER BY snapshot_day DESC LIMIT 1),0)>=5)`

func (c *Collector) setHackEvents(ctx context.Context, from, to string, r *http.Request) (Table, error) {
	cols := []string{}
	for _, col := range hackEventCols {
		if col != "snapshot_day" && col != "created_week" {
			cols = append(cols, col)
		}
	}
	exprs := append([]string{}, cols...)
	for i, col := range exprs {
		if col == "event_key" {
			exprs[i] = "event_key::text"
		}
	}
	cols = append(cols, "metric_def_version")
	t, err := c.platform.Query(ctx, "hack_events", from, to, cols, `SELECT `+strings.Join(exprs, ",")+`, (SELECT metric_def_version FROM paper_run r WHERE r.run_id=t.run_id) FROM paper_hack_event t WHERE snapshot_day BETWEEN $1 AND $2 AND ($3 OR teams>=5) ORDER BY random()`, from, to, includeSmall(r))
	return suppressPeople(t), err
}

const hackMinuteOffset = `(SELECT round(EXTRACT(epoch FROM (t.slot-e.starts_at))/60)::bigint FROM paper_hack_key k JOIN events e ON e.id=k.event_id WHERE k.event_key=t.event_key)`

var hackMinuteCols = []string{"run_id", "minute_offset", "slot_secs", "event_key", "synthetic", "requests", "writes", "data_writes", "deploys", "active_members", "inflight_max", "peak_rps_1s", "arrivals_1s", "writes_1s", "metric_def_version"}

func (c *Collector) setHackMinutes(ctx context.Context, from, to string, r *http.Request) (Table, error) {
	cols := hackMinuteCols
	t, err := c.platform.Query(ctx, "hack_minutes", from, to, cols, `SELECT run_id,`+hackMinuteOffset+`,slot_secs,event_key::text,synthetic,requests,writes,data_writes,deploys,active_members,inflight_max,peak_rps_1s,arrivals_1s,writes_1s, (SELECT metric_def_version FROM paper_run r WHERE r.run_id=t.run_id) FROM paper_hack_minute t WHERE slot >= $1::date AND slot < $2::date+1 AND `+eligibleEvent+` ORDER BY slot,event_key`, from, to, includeSmall(r))
	return suppressPeople(t), err
}

var hackLatencyCols = []string{"run_id", "minute_offset", "event_key", "synthetic", "class", "status_group", "n", "sum_us", "max_us", "bytes_out", "idx_offset", "counts", "arrivals_1s", "writes_1s", "metric_def_version"}

func (c *Collector) setHackLatency(ctx context.Context, from, to string, r *http.Request) (Table, error) {
	return c.platform.Query(ctx, "hack_latency", from, to, hackLatencyCols, `SELECT run_id,`+hackMinuteOffset+`,event_key::text,synthetic,class,status_group,n,sum_us,max_us,bytes_out,idx_offset,counts,arrivals_1s,writes_1s, (SELECT metric_def_version FROM paper_run r WHERE r.run_id=t.run_id) FROM paper_hack_latency t WHERE slot >= $1::date AND slot < $2::date+1 AND `+eligibleEvent+` ORDER BY slot,event_key,class,status_group`, from, to, includeSmall(r))
}

var hackPhaseCols = []string{"run_id", "event_key", "synthetic", "stage", "minute_offset", "observed", "baseline", "metric_def_version"}

func (c *Collector) setHackPhases(ctx context.Context, from, to string, r *http.Request) (Table, error) {
	return c.platform.Query(ctx, "hack_phases", from, to, hackPhaseCols, `SELECT run_id,event_key::text,synthetic,stage,minute_offset,observed,baseline, (SELECT metric_def_version FROM paper_run r WHERE r.run_id=t.run_id) FROM paper_hack_phase t WHERE `+eligibleEvent+` AND phase_slot >= $1::date AND phase_slot < $2::date+1 ORDER BY event_key,phase_slot,stage`, from, to, includeSmall(r))
}

var runCols = []string{"run_id", "started_at", "role", "vcs_revision", "vcs_modified", "go_version", "binary_sha256", "config_hash", "metric_def_version", "bucket_scheme", "gomaxprocs", "num_cpu", "cgroup_memory_max", "cgroup_cpu_max"}

func (c *Collector) setRuns(ctx context.Context, from, to string, _ *http.Request) (Table, error) {
	return c.platform.Query(ctx, "runs", from, to, runCols, `SELECT `+strings.Join(runCols, ",")+` FROM paper_run WHERE (started_at >= $1::date AND started_at < $2::date+1) OR run_id IN (
 SELECT run_id FROM paper_hack_minute WHERE slot >= $1::date AND slot < $2::date+1
 UNION SELECT run_id FROM paper_hack_latency WHERE slot >= $1::date AND slot < $2::date+1
 UNION SELECT run_id FROM paper_hack_event WHERE snapshot_day BETWEEN $1 AND $2
 UNION SELECT run_id FROM paper_hack_daily WHERE day BETWEEN $1 AND $2
 UNION SELECT run_id FROM paper_hack_phase WHERE phase_slot >= $1::date AND phase_slot < $2::date+1
 ) ORDER BY run_id`, from, to)
}

// Suppression is evaluated per day before mutating any values, so sibling
// totals cannot reveal hidden judge/application counts by multiplication or
// subtraction. Missing source metrics are treated as unknown, not zero.
func suppressHackDaily(t Table) Table {
	groups := map[string]map[string]int{}
	hidden := make([]bool, len(t.Rows))
	for i, row := range t.Rows {
		day, metric := row[0].(string), row[1].(string)
		if groups[day] == nil {
			groups[day] = map[string]int{}
		}
		groups[day][metric] = i
		hidden[i] = peopleMetric(metric) && row[2] != nil && ToInt(row[2]) < 5
	}
	for _, metrics := range groups {
		j, ok := metrics["hack_judges_total"]
		if !ok || t.Rows[j][2] == nil || hidden[j] {
			for _, metric := range []string{"hack_scores_total", "hack_assignments_total", "hack_conflicts_total"} {
				if i, ok := metrics[metric]; ok {
					hidden[i] = t.Rows[i][2] != nil
				}
			}
		}
		for _, suffix := range []string{"day", "total"} {
			a, hasA := metrics["hack_applications_"+suffix]
			b, hasB := metrics["hack_applications_approved_"+suffix]
			if !hasA && !hasB {
				continue
			}
			if !hasA || !hasB || t.Rows[a][2] == nil || t.Rows[b][2] == nil || ToInt(t.Rows[a][2]) < 5 || ToInt(t.Rows[b][2]) < 5 || ToInt(t.Rows[a][2])-ToInt(t.Rows[b][2]) < 5 {
				if hasA {
					hidden[a] = t.Rows[a][2] != nil
				}
				if hasB {
					hidden[b] = t.Rows[b][2] != nil
				}
			}
		}
	}
	t.Columns = append(t.Columns, "value_suppressed")
	for i, row := range t.Rows {
		if hidden[i] {
			row[2] = nil
		}
		t.Rows[i] = append(row, hidden[i])
	}
	return t
}

func (c *Collector) setDaily(ctx context.Context, from, to string, _ *http.Request) (Table, error) {
	t, err := c.platform.Query(ctx, "daily", from, to, []string{"day", "metric", "value", "run_id", "metric_def_version"}, `SELECT to_char(d.day,'YYYY-MM-DD'),d.metric,d.value,h.run_id,COALESCE(r.metric_def_version,1) FROM paper_daily d LEFT JOIN paper_hack_daily h ON h.day=d.day AND h.metric=d.metric LEFT JOIN paper_run r ON r.run_id=h.run_id WHERE d.day BETWEEN $1 AND $2 ORDER BY d.day,d.metric`, from, to)
	if err != nil {
		return t, err
	}
	return suppressHackDaily(t), nil
}
