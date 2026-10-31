package papermetrics

import (
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"time"
)

// Sets the export can produce. Each is a table: columns plus rows.
var exportSets = map[string]func(c *Collector, ctx context.Context, from, to string, r *http.Request) (table, error){
	"daily":             (*Collector).setDaily,
	"latency":           (*Collector).setLatency,
	"latency_buckets":   (*Collector).setLatencyBuckets,
	"process":           (*Collector).setProcess,
	"process_daily":     (*Collector).setProcessDaily,
	"footprint":         (*Collector).setFootprint,
	"footprint_summary": (*Collector).setFootprintSummary,
	"api_routes":        (*Collector).setAPIRoutes,
}

type table struct {
	Set     string   `json:"set"`
	From    string   `json:"from"`
	To      string   `json:"to"`
	Columns []string `json:"columns"`
	Rows    [][]any  `json:"rows"`
}

// Who reports whether a request is signed in and whether it is the
// platform admin (the caller owns auth; this package stays a leaf).
type Who func(r *http.Request) (signedIn, admin bool)

// Export is GET /v1/admin/paper-metrics: platform admin only (401 signed
// out, 404 anyone else). Query: set (see exportSets, default "summary" =
// every set as JSON), from/to (YYYY-MM-DD, default the last 90 days),
// format=csv for one set as CSV.
func (c *Collector) Export(who Who) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		signedIn, admin := who(r)
		if !signedIn {
			writeErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		if !admin {
			writeErr(w, http.StatusNotFound, "not found")
			return
		}
		c.export(w, r)
	})
}

func (c *Collector) export(w http.ResponseWriter, r *http.Request) {
	c.flush(r.Context()) // fold in the last minute
	q := r.URL.Query()
	to := q.Get("to")
	if to == "" {
		to = time.Now().UTC().Format("2006-01-02")
	}
	from := q.Get("from")
	if from == "" {
		from = time.Now().UTC().AddDate(0, 0, -89).Format("2006-01-02")
	}
	for _, d := range []string{from, to} {
		if _, err := time.Parse("2006-01-02", d); err != nil {
			writeErr(w, http.StatusBadRequest, "from and to are dates, YYYY-MM-DD")
			return
		}
	}
	set := q.Get("set")
	if set == "" || set == "summary" {
		if q.Get("format") == "csv" {
			writeErr(w, http.StatusBadRequest, "csv needs one set: "+setList())
			return
		}
		out := map[string]any{"from": from, "to": to, "classes": ClassNames(), "bucket_upper_us": BucketBounds(), "methodology": "https://github.com/vineetu/simple-host/blob/main/docs/paper-metrics.md"}
		for _, name := range sortedSets() {
			t, err := exportSets[name](c, r.Context(), from, to, r)
			if err != nil {
				out[name] = map[string]string{"error": err.Error()}
				continue
			}
			out[name] = t
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
		return
	}
	fn := exportSets[set]
	if fn == nil {
		writeErr(w, http.StatusBadRequest, "unknown set; one of: summary, "+setList())
		return
	}
	t, err := fn(c, r.Context(), from, to, r)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "query failed: "+err.Error())
		return
	}
	if q.Get("format") == "csv" {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="paper-%s-%s-%s.csv"`, set, from, to))
		cw := csv.NewWriter(w)
		_ = cw.Write(t.Columns)
		for _, row := range t.Rows {
			rec := make([]string, len(row))
			for i, v := range row {
				rec[i] = fmt.Sprint(v)
			}
			_ = cw.Write(rec)
		}
		cw.Flush()
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(t)
}

func sortedSets() []string {
	names := make([]string, 0, len(exportSets))
	for k := range exportSets {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

func setList() string {
	s := ""
	for i, n := range sortedSets() {
		if i > 0 {
			s += ", "
		}
		s += n
	}
	return s
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// query runs a SELECT and returns every row as plain values.
func (c *Collector) query(ctx context.Context, set, from, to string, cols []string, q string, args ...any) (table, error) {
	rows, err := c.db.QueryContext(ctx, q, args...)
	if err != nil {
		return table{}, err
	}
	defer rows.Close()
	t := table{Set: set, From: from, To: to, Columns: cols, Rows: [][]any{}}
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return table{}, err
		}
		for i, v := range vals {
			switch x := v.(type) {
			case []byte:
				if _, err := strconv.ParseFloat(string(x), 64); err == nil {
					vals[i] = json.Number(x) // numeric columns come back as text
				} else {
					vals[i] = string(x)
				}
			case time.Time:
				vals[i] = x.UTC().Format(time.RFC3339)
			}
		}
		t.Rows = append(t.Rows, vals)
	}
	return t, rows.Err()
}

func (c *Collector) setDaily(ctx context.Context, from, to string, _ *http.Request) (table, error) {
	return c.query(ctx, "daily", from, to, []string{"day", "metric", "value"},
		`SELECT to_char(day, 'YYYY-MM-DD'), metric, value FROM paper_daily WHERE day BETWEEN $1 AND $2 ORDER BY day, metric`, from, to)
}

func (c *Collector) setLatencyBuckets(ctx context.Context, from, to string, _ *http.Request) (table, error) {
	return c.query(ctx, "latency_buckets", from, to, []string{"day", "class", "le_us", "requests"},
		`SELECT to_char(day, 'YYYY-MM-DD'), class, le_us, requests FROM paper_latency_daily WHERE day BETWEEN $1 AND $2 ORDER BY day, class, le_us = -1, le_us`, from, to)
}

// setLatency gives per day and class: requests, 5xx share, bytes, mean and
// percentiles. With all=1 the range is folded into one row per class.
func (c *Collector) setLatency(ctx context.Context, from, to string, r *http.Request) (table, error) {
	all := r != nil && r.URL.Query().Get("all") == "1"
	dayExpr := "to_char(day, 'YYYY-MM-DD')"
	if all {
		dayExpr = "'all'"
	}
	classes, err := c.query(ctx, "", from, to, []string{"day", "class", "requests", "errors", "total_us", "bytes"},
		`SELECT `+dayExpr+`, class, sum(requests), sum(server_errors), sum(total_us), sum(bytes_out) FROM paper_class_daily
		 WHERE day BETWEEN $1 AND $2 GROUP BY 1, 2 ORDER BY 1, 2`, from, to)
	if err != nil {
		return table{}, err
	}
	buckets, err := c.query(ctx, "", from, to, []string{"day", "class", "le_us", "requests"},
		`SELECT `+dayExpr+`, class, le_us, sum(requests) FROM paper_latency_daily
		 WHERE day BETWEEN $1 AND $2 GROUP BY 1, 2, 3`, from, to)
	if err != nil {
		return table{}, err
	}
	hist := map[string]map[int64]int64{}
	for _, b := range buckets.Rows {
		k := fmt.Sprint(b[0], "|", b[1])
		if hist[k] == nil {
			hist[k] = map[int64]int64{}
		}
		hist[k][toInt(b[2])] += toInt(b[3])
	}
	t := table{Set: "latency", From: from, To: to, Rows: [][]any{},
		Columns: []string{"day", "class", "requests", "server_error_pct", "bytes_out", "mean_ms", "p50_ms", "p90_ms", "p99_ms", "p999_ms"}}
	for _, row := range classes.Rows {
		n, errs, totalUS, bytes := toInt(row[2]), toInt(row[3]), toInt(row[4]), toInt(row[5])
		h := hist[fmt.Sprint(row[0], "|", row[1])]
		var errPct, mean float64
		if n > 0 {
			errPct = round(float64(errs)*100/float64(n), 3)
			mean = round(float64(totalUS)/float64(n)/1000, 3)
		}
		t.Rows = append(t.Rows, []any{row[0], row[1], n, errPct, bytes, mean,
			pct(h, 0.50), pct(h, 0.90), pct(h, 0.99), pct(h, 0.999)})
	}
	return t, nil
}

// pct estimates a quantile in milliseconds from bucket counts, interpolating
// linearly inside the bucket it falls in. The overflow bucket reports its
// lower bound (10 s): a floor, flagged in the methodology note.
func pct(h map[int64]int64, q float64) float64 {
	var total int64
	for _, n := range h {
		total += n
	}
	if total == 0 {
		return 0
	}
	target := q * float64(total)
	var seen float64
	lower := int64(0)
	for _, up := range BucketBounds() {
		n := float64(h[up])
		if up == -1 {
			return round(float64(lower)/1000, 3)
		}
		if seen+n >= target && n > 0 {
			frac := (target - seen) / n
			return round((float64(lower)+frac*float64(up-lower))/1000, 3)
		}
		seen += n
		lower = up
	}
	return round(float64(lower)/1000, 3)
}

func (c *Collector) setProcess(ctx context.Context, from, to string, _ *http.Request) (table, error) {
	cols := []string{"at", "started_at", "proc_cpu_cores", "proc_rss_bytes", "go_heap_bytes", "goroutines", "open_fds", "pg_rss_bytes", "nginx_rss_bytes", "box_cpu_cores", "box_cpus", "box_mem_total_bytes", "box_mem_avail_bytes", "load1"}
	return c.query(ctx, "process", from, to, cols,
		`SELECT at, started_at, proc_cpu_cores, proc_rss_bytes, go_heap_bytes, goroutines, open_fds, pg_rss_bytes, nginx_rss_bytes, box_cpu_cores, box_cpus, box_mem_total_bytes, box_mem_avail_bytes, load1
		 FROM paper_process_sample WHERE at >= $1::date AND at < $2::date + 1 ORDER BY at`, from, to)
}

func (c *Collector) setProcessDaily(ctx context.Context, from, to string, _ *http.Request) (table, error) {
	cols := []string{"day", "samples", "restarts", "proc_cpu_mean", "proc_cpu_p99", "proc_rss_median", "proc_rss_max", "pg_rss_median", "nginx_rss_median", "box_cpu_mean", "box_mem_used_median", "goroutines_median"}
	return c.query(ctx, "process_daily", from, to, cols, `
		SELECT to_char(at, 'YYYY-MM-DD'), count(*), count(DISTINCT started_at) - 1,
			round(avg(proc_cpu_cores)::numeric, 4), round(percentile_cont(0.99) WITHIN GROUP (ORDER BY proc_cpu_cores)::numeric, 4),
			percentile_disc(0.5) WITHIN GROUP (ORDER BY proc_rss_bytes), max(proc_rss_bytes),
			percentile_disc(0.5) WITHIN GROUP (ORDER BY pg_rss_bytes), percentile_disc(0.5) WITHIN GROUP (ORDER BY nginx_rss_bytes),
			round(avg(box_cpu_cores)::numeric, 4), percentile_disc(0.5) WITHIN GROUP (ORDER BY box_mem_total_bytes - box_mem_avail_bytes),
			percentile_disc(0.5) WITHIN GROUP (ORDER BY goroutines)
		FROM paper_process_sample WHERE at >= $1::date AND at < $2::date + 1 GROUP BY 1 ORDER BY 1`, from, to)
}

var footprintCols = []string{"week", "versions", "live_files", "live_bytes", "total_bytes", "html_bytes", "js_bytes", "css_bytes", "media_bytes", "other_bytes", "sqlite_bytes", "file_store_bytes", "kv_bytes", "kv_rows", "legacy_rows", "views_30d", "age_days", "days_since_update"}

// setFootprint returns the anonymous per-site rows of one week (week=,
// default the latest), in random order.
func (c *Collector) setFootprint(ctx context.Context, from, to string, r *http.Request) (table, error) {
	week := ""
	if r != nil {
		week = r.URL.Query().Get("week")
	}
	if week == "" {
		var w sql.NullString
		_ = c.db.QueryRowContext(ctx, `SELECT to_char(max(week), 'YYYY-MM-DD') FROM paper_site_footprint`).Scan(&w)
		week = w.String
	}
	return c.query(ctx, "footprint", week, week, footprintCols, `
		SELECT to_char(week, 'YYYY-MM-DD'), versions, live_files, live_bytes, total_bytes, html_bytes, js_bytes, css_bytes, media_bytes, other_bytes,
			sqlite_bytes, file_store_bytes, kv_bytes, kv_rows, legacy_rows, views_30d, age_days, days_since_update
		FROM paper_site_footprint WHERE week = $1::date ORDER BY random()`, week)
}

// setFootprintSummary: per week, the distribution the paper reports. An
// idle site is one with no person views in 30 days.
func (c *Collector) setFootprintSummary(ctx context.Context, from, to string, _ *http.Request) (table, error) {
	cols := []string{"week", "sites", "idle_sites", "live_bytes_p50", "live_bytes_p90", "live_bytes_p99", "live_bytes_max", "total_bytes_sum",
		"js_bytes_p50", "js_bytes_p90", "html_bytes_p50", "data_bytes_p50", "data_bytes_p99", "sites_with_data", "idle_total_bytes_mean", "versions_p50"}
	return c.query(ctx, "footprint_summary", from, to, cols, `
		SELECT to_char(week, 'YYYY-MM-DD'), count(*), count(*) FILTER (WHERE views_30d = 0),
			percentile_disc(0.5) WITHIN GROUP (ORDER BY live_bytes), percentile_disc(0.9) WITHIN GROUP (ORDER BY live_bytes),
			percentile_disc(0.99) WITHIN GROUP (ORDER BY live_bytes), max(live_bytes), sum(total_bytes),
			percentile_disc(0.5) WITHIN GROUP (ORDER BY js_bytes), percentile_disc(0.9) WITHIN GROUP (ORDER BY js_bytes),
			percentile_disc(0.5) WITHIN GROUP (ORDER BY html_bytes),
			percentile_disc(0.5) WITHIN GROUP (ORDER BY sqlite_bytes + file_store_bytes + kv_bytes),
			percentile_disc(0.99) WITHIN GROUP (ORDER BY sqlite_bytes + file_store_bytes + kv_bytes),
			count(*) FILTER (WHERE sqlite_bytes + file_store_bytes + kv_bytes + legacy_rows > 0),
			COALESCE(round(avg(total_bytes) FILTER (WHERE views_30d = 0)), 0),
			percentile_disc(0.5) WITHIN GROUP (ORDER BY versions)
		FROM paper_site_footprint WHERE week BETWEEN $1::date - 6 AND $2::date GROUP BY 1 ORDER BY 1`, from, to)
}

// setAPIRoutes is the existing per-route counts (30-day retention there),
// for the storage read/write mix and the attack-surface table.
func (c *Collector) setAPIRoutes(ctx context.Context, from, to string, _ *http.Request) (table, error) {
	return c.query(ctx, "api_routes", from, to, []string{"route", "status", "calls"},
		`SELECT route, status, sum(calls) FROM api_request_daily WHERE day BETWEEN $1 AND $2 GROUP BY 1, 2 ORDER BY 3 DESC`, from, to)
}

func toInt(v any) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case float64:
		return int64(x)
	case json.Number:
		f, _ := x.Float64()
		return int64(f)
	case string:
		n, _ := strconv.ParseInt(x, 10, 64)
		return n
	}
	return 0
}

func round(v float64, places int) float64 {
	p := math.Pow(10, float64(places))
	return math.Round(v*p) / p
}
