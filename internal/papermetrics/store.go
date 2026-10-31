package papermetrics

import (
	"bufio"
	"context"
	"database/sql"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// flush moves the in-memory histograms into paper_latency_daily and
// paper_class_daily. Counters are swapped to zero first, so a failed write
// loses at most one minute rather than double counting.
func (c *Collector) flush(ctx context.Context) {
	day := time.Now().UTC().Format("2006-01-02")
	type row struct {
		class                         string
		buckets                       [numBuckets]int64
		requests, errors, totalUS, by int64
	}
	var rows []row
	for i := 1; i < numClasses; i++ {
		h := &c.hist[i]
		r := row{class: classNames[i]}
		r.requests = h.requests.Swap(0)
		if r.requests == 0 {
			continue
		}
		r.errors = h.errors.Swap(0)
		r.totalUS = h.totalUS.Swap(0)
		r.by = h.bytes.Swap(0)
		for b := range h.buckets {
			r.buckets[b] = h.buckets[b].Swap(0)
		}
		rows = append(rows, r)
	}
	if len(rows) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		log.Printf("paper metrics flush: %v", err)
		return
	}
	defer tx.Rollback() //nolint:errcheck
	bounds := BucketBounds()
	for _, r := range rows {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO paper_class_daily (day, class, requests, server_errors, total_us, bytes_out)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (day, class) DO UPDATE SET
				requests = paper_class_daily.requests + EXCLUDED.requests,
				server_errors = paper_class_daily.server_errors + EXCLUDED.server_errors,
				total_us = paper_class_daily.total_us + EXCLUDED.total_us,
				bytes_out = paper_class_daily.bytes_out + EXCLUDED.bytes_out`,
			day, r.class, r.requests, r.errors, r.totalUS, r.by); err != nil {
			log.Printf("paper metrics flush: %v", err)
			return
		}
		for b, n := range r.buckets {
			if n == 0 {
				continue
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO paper_latency_daily (day, class, le_us, requests) VALUES ($1, $2, $3, $4)
				ON CONFLICT (day, class, le_us) DO UPDATE SET requests = paper_latency_daily.requests + EXCLUDED.requests`,
				day, r.class, bounds[b], n); err != nil {
				log.Printf("paper metrics flush: %v", err)
				return
			}
		}
	}
	if err := tx.Commit(); err != nil {
		log.Printf("paper metrics flush: %v", err)
	}
}

// sample is one reading of the cumulative counters a delta is taken from.
type sample struct {
	at                time.Time
	procUser, procSys time.Duration
	boxBusy, boxTotal uint64
	valid             bool
}

// takeSample records this process, Postgres, nginx and the box as a whole.
// CPU is the share of one core used since the previous sample (so 1.0 is a
// whole core); memory is resident bytes now.
func (c *Collector) takeSample(ctx context.Context) {
	var ru syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &ru)
	now := sample{
		at:       time.Now().UTC(),
		procUser: time.Duration(ru.Utime.Nano()),
		procSys:  time.Duration(ru.Stime.Nano()),
		valid:    true,
	}
	now.boxBusy, now.boxTotal = readProcStat()
	prev := c.lastSample
	c.lastSample = now
	if !prev.valid {
		return
	}
	wall := now.at.Sub(prev.at).Seconds()
	if wall <= 0 {
		return
	}
	procCPU := (now.procUser + now.procSys - prev.procUser - prev.procSys).Seconds() / wall
	var boxBusy float64
	if dt := now.boxTotal - prev.boxTotal; dt > 0 {
		boxBusy = float64(now.boxBusy-prev.boxBusy) / float64(dt) * float64(runtime.NumCPU())
	}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	pgRSS, nginxRSS := rssOf("postgres"), rssOf("nginx")
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if _, err := c.db.ExecContext(ctx, `
		INSERT INTO paper_process_sample
			(at, started_at, proc_cpu_cores, proc_rss_bytes, go_heap_bytes, goroutines, open_fds,
			 pg_rss_bytes, nginx_rss_bytes, box_cpu_cores, box_cpus, box_mem_total_bytes, box_mem_avail_bytes, load1)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		ON CONFLICT (at) DO NOTHING`,
		now.at, c.started.UTC(), procCPU, selfRSS(), int64(ms.HeapAlloc), runtime.NumGoroutine(), openFDs(),
		pgRSS, nginxRSS, boxBusy, runtime.NumCPU(), memInfo("MemTotal"), memInfo("MemAvailable"), load1()); err != nil {
		log.Printf("paper metrics sample: %v", err)
	}
}

// readProcStat returns busy and total jiffies over all CPUs.
func readProcStat() (busy, total uint64) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return 0, 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	if !sc.Scan() {
		return 0, 0
	}
	fields := strings.Fields(sc.Text())
	if len(fields) < 5 || fields[0] != "cpu" {
		return 0, 0
	}
	for i, s := range fields[1:] {
		v, _ := strconv.ParseUint(s, 10, 64)
		total += v
		if i != 3 && i != 4 { // idle, iowait
			busy += v
		}
	}
	return busy, total
}

func statusKB(path, key string) int64 {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, key+":") {
			fields := strings.Fields(line[len(key)+1:])
			if len(fields) > 0 {
				v, _ := strconv.ParseInt(fields[0], 10, 64)
				return v * 1024
			}
		}
	}
	return 0
}

func selfRSS() int64           { return statusKB("/proc/self/status", "VmRSS") }
func memInfo(key string) int64 { return statusKB("/proc/meminfo", key) }

// rssOf sums resident memory of every process with that command name.
// Postgres workers share their buffer pool, so this overstates Postgres's
// real footprint; the methodology note says so.
func rssOf(comm string) int64 {
	dirs, _ := os.ReadDir("/proc")
	var total int64
	for _, d := range dirs {
		if _, err := strconv.Atoi(d.Name()); err != nil {
			continue
		}
		b, err := os.ReadFile(filepath.Join("/proc", d.Name(), "comm"))
		if err != nil || strings.TrimSpace(string(b)) != comm {
			continue
		}
		total += statusKB(filepath.Join("/proc", d.Name(), "status"), "VmRSS")
	}
	return total
}

func openFDs() int {
	d, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return 0
	}
	return len(d)
}

func load1() float64 {
	b, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0
	}
	f := strings.Fields(string(b))
	if len(f) == 0 {
		return 0
	}
	v, _ := strconv.ParseFloat(f[0], 64)
	return v
}

// dailySnapshot stores counts as of the end of yesterday (UTC), once.
// Each metric is one query against current state; a failing query skips
// only its own metric, so a schema difference on another instance (Simple
// Hack, a small box) costs a number, not the snapshot.
func (c *Collector) dailySnapshot(ctx context.Context, now time.Time) error {
	day := now.AddDate(0, 0, -1).Format("2006-01-02")
	var done bool
	if err := c.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM paper_daily WHERE day = $1)`, day).Scan(&done); err != nil {
		return err
	}
	if done {
		return nil
	}
	n := 0
	for _, m := range dailyMetrics {
		var v sql.NullFloat64
		var args []any
		if strings.Contains(m.query, "$1") {
			args = append(args, day)
		}
		if err := c.db.QueryRowContext(ctx, m.query, args...).Scan(&v); err != nil {
			log.Printf("paper metrics daily %s: %v", m.name, err)
			continue
		}
		if !v.Valid {
			v.Float64 = 0
		}
		if _, err := c.db.ExecContext(ctx, `INSERT INTO paper_daily (day, metric, value) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`, day, m.name, v.Float64); err != nil {
			return err
		}
		n++
	}
	log.Printf("paper metrics: daily snapshot for %s, %d metrics", day, n)
	return nil
}

// dailyMetrics: a query with $1 gets the day (its end is the cut-off); one
// without reads current state, so it is only true when taken that night.
// Names are stored; add new ones, never rename.
var dailyMetrics = []struct{ name, query string }{
	{"accounts", `SELECT count(*) FROM users WHERE created_at < $1::date + 1`},
	{"accounts_new", `SELECT count(*) FROM users WHERE created_at >= $1::date AND created_at < $1::date + 1`},
	{"accounts_with_live_site", `SELECT count(DISTINCT user_id) FROM sites WHERE deleted_at IS NULL`},
	{"sites_live", `SELECT count(*) FROM sites WHERE deleted_at IS NULL`},
	{"sites_new", `SELECT count(*) FROM sites WHERE created_at >= $1::date AND created_at < $1::date + 1`},
	{"sites_updated_day", `SELECT count(*) FROM sites WHERE deleted_at IS NULL AND updated_at >= $1::date AND updated_at < $1::date + 1`},
	{"sites_updated_30d", `SELECT count(*) FROM sites WHERE deleted_at IS NULL AND updated_at >= $1::date - 29`},
	{"sites_offline", `SELECT count(*) FROM sites WHERE deleted_at IS NULL AND offline_at IS NOT NULL`},
	{"sites_viewed_day", `SELECT count(DISTINCT site_id) FROM site_view_hourly WHERE class = 'person' AND hour >= $1::date AND hour < $1::date + 1`},
	{"sites_viewed_7d", `SELECT count(DISTINCT site_id) FROM site_view_hourly WHERE class = 'person' AND hour >= $1::date - 6 AND hour < $1::date + 1`},
	{"sites_viewed_30d", `SELECT count(DISTINCT site_id) FROM site_view_hourly WHERE class = 'person' AND hour >= $1::date - 29 AND hour < $1::date + 1`},
	{"page_views_person_day", `SELECT COALESCE(sum(views), 0) FROM site_view_hourly WHERE class = 'person' AND hour >= $1::date AND hour < $1::date + 1`},
	{"page_views_bot_day", `SELECT COALESCE(sum(views), 0) FROM site_view_hourly WHERE class = 'bot' AND hour >= $1::date AND hour < $1::date + 1`},
	{"sites_with_storage", `SELECT count(DISTINCT r.site_id) FROM site_storage_resources r JOIN sites s ON s.id = r.site_id WHERE s.deleted_at IS NULL`},
	{"sites_with_kv", `SELECT count(DISTINCT r.site_id) FROM site_storage_resources r JOIN sites s ON s.id = r.site_id WHERE s.deleted_at IS NULL AND r.kind = 'kv'`},
	{"sites_with_sqlite", `SELECT count(DISTINCT r.site_id) FROM site_storage_resources r JOIN sites s ON s.id = r.site_id WHERE s.deleted_at IS NULL AND r.kind = 'sqlite'`},
	{"sites_with_files_store", `SELECT count(DISTINCT r.site_id) FROM site_storage_resources r JOIN sites s ON s.id = r.site_id WHERE s.deleted_at IS NULL AND r.kind = 'files'`},
	{"sites_with_legacy_data", `SELECT count(DISTINCT c.site_id) FROM collection_items c JOIN sites s ON s.id = c.site_id WHERE s.deleted_at IS NULL AND c.deleted_at IS NULL`},
	{"sites_with_visitor_signin", `SELECT count(DISTINCT v.site_id) FROM site_visitor_signins v JOIN sites s ON s.id = v.site_id WHERE s.deleted_at IS NULL`},
	{"visitor_signins_total", `SELECT count(*) FROM site_visitor_signins WHERE first_at < $1::date + 1`},
	{"sites_access_specific", `SELECT count(*) FROM sites WHERE deleted_at IS NULL AND access = 'specific'`},
	{"sites_with_named_viewers", `SELECT count(DISTINCT v.site_id) FROM site_viewers v JOIN sites s ON s.id = v.site_id WHERE s.deleted_at IS NULL`},
	{"sites_with_passcode", `SELECT count(*) FROM sites WHERE deleted_at IS NULL AND passcode_enc IS NOT NULL`},
	{"sites_custom_domain", `SELECT count(*) FROM sites WHERE deleted_at IS NULL AND custom_domain IS NOT NULL`},
	{"sites_custom_domain_cert_live", `SELECT count(*) FROM sites WHERE deleted_at IS NULL AND custom_domain IS NOT NULL AND domain_cert_status = 'live'`},
	{"address_families", `SELECT count(*) FROM address_families`},
	{"certs_person_wildcard", `SELECT count(*) FROM domain_cert_requests`},
	{"certs_family", `SELECT count(*) FROM family_cert_requests`},
	{"sites_with_notifications", `SELECT count(DISTINCT site_id) FROM site_notifications`},
	{"connector_grants_active_30d", `SELECT count(*) FROM oauth_grants WHERE last_used_at >= $1::date - 29`},
	{"connector_accounts_active_30d", `SELECT count(DISTINCT user_id) FROM oauth_grants WHERE last_used_at >= $1::date - 29`},
	{"storage_resources", `SELECT count(*) FROM site_storage_resources`},
	{"resources_read_anyone", `SELECT count(*) FROM site_storage_resources WHERE acc_read = 'anyone'`},
	{"resources_add_anyone", `SELECT count(*) FROM site_storage_resources WHERE acc_add = 'anyone'`},
	{"resources_add_checked", `SELECT count(*) FROM site_storage_resources WHERE acc_add = 'checked'`},
	{"resources_add_signed_in", `SELECT count(*) FROM site_storage_resources WHERE acc_add = 'signed-in'`},
	{"resources_own_reads", `SELECT count(*) FROM site_storage_resources WHERE acc_read = 'own'`},
	{"sites_suspended", `SELECT count(*) FROM sites WHERE deleted_at IS NULL AND suspended_at IS NOT NULL`},
	{"accounts_suspended", `SELECT count(*) FROM users WHERE suspended_at IS NOT NULL`},
	{"kv_rows", `SELECT count(*) FROM site_storage_kv`},
	{"kv_value_bytes", `SELECT COALESCE(sum(pg_column_size(value)), 0) FROM site_storage_kv`},
	{"legacy_rows", `SELECT count(*) FROM collection_items WHERE deleted_at IS NULL`},
	{"db_bytes", `SELECT pg_database_size(current_database())`},
	{"api_calls_day", `SELECT COALESCE(sum(calls), 0) FROM api_request_daily WHERE day = $1::date`},
	{"api_errors_5xx_day", `SELECT COALESCE(sum(calls), 0) FROM api_request_daily WHERE day = $1::date AND status >= 500`},
	{"api_refused_429_day", `SELECT COALESCE(sum(calls), 0) FROM api_request_daily WHERE day = $1::date AND status = 429`},
	{"api_denied_401_403_day", `SELECT COALESCE(sum(calls), 0) FROM api_request_daily WHERE day = $1::date AND status IN (401, 403)`},
	{"api_mcp_calls_day", `SELECT COALESCE(sum(calls), 0) FROM api_request_daily WHERE day = $1::date AND route = 'POST /mcp'`},
	{"api_data_reads_day", `SELECT COALESCE(sum(calls), 0) FROM api_request_daily WHERE day = $1::date AND route ~ '/storage/|/collections|/state|/data$|/data/' AND (route LIKE 'GET %' OR route LIKE 'HEAD %')`},
	{"api_data_writes_day", `SELECT COALESCE(sum(calls), 0) FROM api_request_daily WHERE day = $1::date AND route ~ '/storage/|/collections|/state|/data$|/data/' AND route !~ '^(GET|HEAD) '`},
	{"api_deploys_day", `SELECT COALESCE(sum(calls), 0) FROM api_request_daily WHERE day = $1::date AND status < 300 AND route ~ '^(POST|PUT) /v1/sites/\{sitename\}(/files)?$'`},
	{"bytes_out_pages_day", `SELECT COALESCE(sum(bytes), 0) FROM traffic_daily WHERE day = $1::date AND kind = 'pages'`},
	{"bytes_out_api_day", `SELECT COALESCE(sum(bytes), 0) FROM traffic_daily WHERE day = $1::date AND kind = 'api'`},
	{"requests_pages_day", `SELECT COALESCE(sum(requests), 0) FROM traffic_daily WHERE day = $1::date AND kind = 'pages'`},
	{"net_tx_bytes_day", `SELECT COALESCE(sum(tx_bytes), 0) FROM net_usage_daily WHERE day = $1::date`},
	{"net_rx_bytes_day", `SELECT COALESCE(sum(rx_bytes), 0) FROM net_usage_daily WHERE day = $1::date`},
	{"process_restarts_day", `SELECT count(DISTINCT started_at) FROM paper_process_sample WHERE at >= $1::date AND at < $1::date + 1 AND started_at >= $1::date`},
	{"sample_minutes_missing_day", `SELECT GREATEST(0, 1440 - 5 * count(*)) FROM paper_process_sample WHERE at >= $1::date AND at < $1::date + 1`},
}
