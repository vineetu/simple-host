package papermetrics

import (
	"context"
	"net/http"
	"slices"
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

// suppressPeople blanks each event row's small people counts (and what they would reveal) and adds a <column>_suppressed flag per people column.
func suppressPeople(t Table) Table {
	var indexes []int
	for i, col := range t.Columns {
		if peopleColumns[col] {
			indexes = append(indexes, i)
		}
	}
	for _, i := range indexes {
		t.Columns = append(t.Columns, t.Columns[i]+"_suppressed")
	}
	for j, row := range t.Rows {
		counts := map[string]any{}
		for _, i := range indexes {
			counts[t.Columns[i]] = row[i]
		}
		hidden := hiddenPeople(counts)
		for _, i := range indexes {
			if hidden[t.Columns[i]] {
				row[i] = nil
			}
			row = append(row, hidden[t.Columns[i]])
		}
		t.Rows[j] = row
	}
	return t
}

// judgeWorkColumns follow the judges count: they would reveal it.
var judgeWorkColumns = []string{"assignments", "scores", "conflicts"}

// hiddenPeople decides which of one event's people counts to hide.
func hiddenPeople(counts map[string]any) map[string]bool {
	hidden := map[string]bool{}
	for col, v := range counts {
		if !slices.Contains(judgeWorkColumns, col) {
			hidden[col] = v != nil && ToInt(v) < 5
		}
	}
	if _, ok := counts["judges"]; ok {
		hideJudgeWork(hidden, counts, "judges", judgeWorkColumns)
	}
	_, hasA := counts["applications"]
	_, hasB := counts["applications_approved"]
	if hasA && hasB {
		hideApplicationPair(hidden, counts, "applications", "applications_approved")
	}
	return hidden
}

// hideJudgeWork hides each known judge-work count when the judges count is unknown or hidden.
func hideJudgeWork(hidden map[string]bool, counts map[string]any, judges string, work []string) {
	if counts[judges] != nil && !hidden[judges] {
		return
	}
	for _, name := range work {
		hidden[name] = counts[name] != nil
	}
}

// hideApplicationPair hides both counts of an applications/approved pair when either is unknown or below 5, or so is their difference.
func hideApplicationPair(hidden map[string]bool, counts map[string]any, applied, approved string) {
	a, b := counts[applied], counts[approved]
	if a == nil || b == nil || ToInt(a) < 5 || ToInt(b) < 5 || ToInt(a)-ToInt(b) < 5 {
		hidden[applied], hidden[approved] = a != nil, b != nil
	}
}

func includeSmall(r *http.Request) bool { return r != nil && r.URL.Query().Get("include_small") == "1" }

// Apply to every per-event set, including phase and latency series.
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

// Suppression is evaluated per day before mutating any values, so sibling totals cannot reveal hidden judge/application counts by multiplication or subtraction.
func suppressHackDaily(t Table) Table {
	days := map[string]map[string]any{}
	for _, row := range t.Rows {
		day := row[0].(string)
		if days[day] == nil {
			days[day] = map[string]any{}
		}
		days[day][row[1].(string)] = row[2]
	}
	hidden := map[string]map[string]bool{}
	for day, values := range days {
		hidden[day] = hiddenDaily(values)
	}
	t.Columns = append(t.Columns, "value_suppressed")
	for i, row := range t.Rows {
		h := hidden[row[0].(string)][row[1].(string)]
		if h {
			row[2] = nil
		}
		t.Rows[i] = append(row, h)
	}
	return t
}

// hiddenDaily decides which of one day's metrics to hide.
func hiddenDaily(values map[string]any) map[string]bool {
	hidden := map[string]bool{}
	for metric, v := range values {
		hidden[metric] = peopleMetric(metric) && v != nil && ToInt(v) < 5
	}
	hideJudgeWork(hidden, values, "hack_judges_total", []string{"hack_scores_total", "hack_assignments_total", "hack_conflicts_total"})
	for _, suffix := range []string{"day", "total"} {
		hideApplicationPair(hidden, values, "hack_applications_"+suffix, "hack_applications_approved_"+suffix)
	}
	return hidden
}

func (c *Collector) setDaily(ctx context.Context, from, to string, _ *http.Request) (Table, error) {
	t, err := c.platform.Query(ctx, "daily", from, to, []string{"day", "metric", "value", "run_id", "metric_def_version"}, `SELECT to_char(d.day,'YYYY-MM-DD'),d.metric,d.value,h.run_id,COALESCE(r.metric_def_version,1) FROM paper_daily d LEFT JOIN paper_hack_daily h ON h.day=d.day AND h.metric=d.metric LEFT JOIN paper_run r ON r.run_id=h.run_id WHERE d.day BETWEEN $1 AND $2 ORDER BY d.day,d.metric`, from, to)
	if err != nil {
		return t, err
	}
	return suppressHackDaily(t), nil
}
