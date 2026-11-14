package papermetrics

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
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
		buckets                       [NumBuckets]int64
		requests, errors, totalUS, by int64
	}
	var rows []row
	for i := 1; i < NumClasses; i++ {
		var r row
		if c.product == nil {
			h := &c.hist[i]
			r = row{class: classNames[i]}
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
		} else {
			r = row{class: classNames[i]}
			histories := []*ClassHist{&c.hist[i]}
			histories = append(histories, c.product.Histories(i)...)
			for _, h := range histories {
				r.requests += h.requests.Swap(0)
				r.errors += h.errors.Swap(0)
				r.totalUS += h.totalUS.Swap(0)
				r.by += h.bytes.Swap(0)
				for b := range h.buckets {
					r.buckets[b] += h.buckets[b].Swap(0)
				}
			}
			if r.requests == 0 {
				continue
			}
		}
		rows = append(rows, r)
	}
	if len(rows) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var written int64
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		c.tracker.Fail("paper metrics flush", err)
		log.Printf("paper metrics flush: %v", err)
		return
	}
	defer tx.Rollback() //nolint:errcheck
	bounds := BucketBounds()
	for _, r := range rows {
		if _, err := c.ExecMetric(ctx, tx, &written, `
			INSERT INTO paper_class_daily (day, class, requests, server_errors, total_us, bytes_out)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (day, class) DO UPDATE SET
				requests = paper_class_daily.requests + EXCLUDED.requests,
				server_errors = paper_class_daily.server_errors + EXCLUDED.server_errors,
				total_us = paper_class_daily.total_us + EXCLUDED.total_us,
				bytes_out = paper_class_daily.bytes_out + EXCLUDED.bytes_out`,
			day, r.class, r.requests, r.errors, r.totalUS, r.by); err != nil {
			c.tracker.Fail("paper metrics flush", err)
			log.Printf("paper metrics flush: %v", err)
			return
		}
		for b, n := range r.buckets {
			if n == 0 {
				continue
			}
			if _, err := c.ExecMetric(ctx, tx, &written, `
				INSERT INTO paper_latency_daily (day, class, le_us, requests) VALUES ($1, $2, $3, $4)
				ON CONFLICT (day, class, le_us) DO UPDATE SET requests = paper_latency_daily.requests + EXCLUDED.requests`,
				day, r.class, bounds[b], n); err != nil {
				c.tracker.Fail("paper metrics flush", err)
				log.Printf("paper metrics flush: %v", err)
				return
			}
		}
	}
	if err := c.CommitMetrics(tx, written); err != nil {
		c.tracker.Fail("paper metrics flush", err)
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
	if _, err := c.ExecMetric(ctx, c.db, nil, `
		INSERT INTO paper_process_sample
			(at, started_at, proc_cpu_cores, proc_rss_bytes, go_heap_bytes, goroutines, open_fds,
			 pg_rss_bytes, nginx_rss_bytes, box_cpu_cores, box_cpus, box_mem_total_bytes, box_mem_avail_bytes, load1)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		ON CONFLICT (at) DO NOTHING`,
		now.at, c.started.UTC(), procCPU, selfRSS(), int64(ms.HeapAlloc), runtime.NumGoroutine(), openFDs(),
		pgRSS, nginxRSS, boxBusy, runtime.NumCPU(), memInfo("MemTotal"), memInfo("MemAvailable"), load1()); err != nil {
		c.tracker.Fail("paper metrics sample", err)
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
func (c *Collector) dailySnapshot(ctx context.Context, now time.Time) (resultErr error) {
	defer func() {
		if resultErr != nil {
			c.tracker.Fail("dailySnapshot", resultErr)
		}
	}()
	day := now.AddDate(0, 0, -1).Format("2006-01-02")
	var done bool
	if err := c.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM paper_daily WHERE day = $1)`, day).Scan(&done); err != nil {
		return err
	}
	if done {
		return nil
	}
	fileEntries, fileBytes, skipped, err := c.kvFileTotals(ctx)
	if err != nil {
		return err
	}
	n := 0
	metrics := dailyMetrics
	if c.product != nil {
		metrics = append(append([]DailyMetric{}, dailyMetrics...), c.product.DailyMetrics()...)
	}
	for _, m := range metrics {
		var v sql.NullFloat64
		var args []any
		if strings.Contains(m.Query, "$1") {
			args = append(args, day)
		}
		if err := c.db.QueryRowContext(ctx, m.Query, args...).Scan(&v); err != nil {
			c.tracker.Fail("daily metric "+m.Name, err)
			log.Printf("paper metrics daily %s: %v", m.Name, err)
			continue
		}
		if !v.Valid {
			v.Float64 = 0
		}
		switch m.Name {
		case "kv_rows":
			v.Float64 += float64(fileEntries)
		case "kv_value_text_bytes":
			v.Float64 += float64(fileBytes)
		case "kv_file_sites_skipped":
			v.Float64 = float64(skipped)
		}
		if m.Store != nil {
			if err := m.Store(ctx, day, v.Float64); err != nil {
				continue
			}
		} else if _, err := c.ExecMetric(ctx, c.db, nil, `INSERT INTO paper_daily(day,metric,value) VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`, day, m.Name, v.Float64); err != nil {
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
var dailyMetrics = []DailyMetric{
	{Name: "accounts", Query: `SELECT count(*) FROM users WHERE created_at < $1::date + 1`},
	{Name: "accounts_new", Query: `SELECT count(*) FROM users WHERE created_at >= $1::date AND created_at < $1::date + 1`},
	{Name: "accounts_with_live_site", Query: `SELECT count(DISTINCT user_id) FROM sites WHERE deleted_at IS NULL`},
	{Name: "sites_live", Query: `SELECT count(*) FROM sites WHERE deleted_at IS NULL`},
	{Name: "sites_new", Query: `SELECT count(*) FROM sites WHERE created_at >= $1::date AND created_at < $1::date + 1`},
	{Name: "sites_updated_day", Query: `SELECT count(*) FROM sites WHERE deleted_at IS NULL AND updated_at >= $1::date AND updated_at < $1::date + 1`},
	{Name: "sites_updated_30d", Query: `SELECT count(*) FROM sites WHERE deleted_at IS NULL AND updated_at >= $1::date - 29`},
	{Name: "sites_offline", Query: `SELECT count(*) FROM sites WHERE deleted_at IS NULL AND offline_at IS NOT NULL`},
	{Name: "sites_viewed_day", Query: `SELECT count(DISTINCT site_id) FROM site_view_hourly WHERE class = 'person' AND hour >= $1::date AND hour < $1::date + 1`},
	{Name: "sites_viewed_7d", Query: `SELECT count(DISTINCT site_id) FROM site_view_hourly WHERE class = 'person' AND hour >= $1::date - 6 AND hour < $1::date + 1`},
	{Name: "sites_viewed_30d", Query: `SELECT count(DISTINCT site_id) FROM site_view_hourly WHERE class = 'person' AND hour >= $1::date - 29 AND hour < $1::date + 1`},
	{Name: "page_views_person_day", Query: `SELECT COALESCE(sum(views), 0) FROM site_view_hourly WHERE class = 'person' AND hour >= $1::date AND hour < $1::date + 1`},
	{Name: "page_views_bot_day", Query: `SELECT COALESCE(sum(views), 0) FROM site_view_hourly WHERE class = 'bot' AND hour >= $1::date AND hour < $1::date + 1`},
	{Name: "sites_with_storage", Query: `SELECT count(DISTINCT r.site_id) FROM site_storage_resources r JOIN sites s ON s.id = r.site_id WHERE s.deleted_at IS NULL`},
	{Name: "sites_with_kv", Query: `SELECT count(DISTINCT r.site_id) FROM site_storage_resources r JOIN sites s ON s.id = r.site_id WHERE s.deleted_at IS NULL AND r.kind = 'kv'`},
	{Name: "sites_with_sqlite", Query: `SELECT count(DISTINCT r.site_id) FROM site_storage_resources r JOIN sites s ON s.id = r.site_id WHERE s.deleted_at IS NULL AND r.kind = 'sqlite'`},
	{Name: "sites_with_files_store", Query: `SELECT count(DISTINCT r.site_id) FROM site_storage_resources r JOIN sites s ON s.id = r.site_id WHERE s.deleted_at IS NULL AND r.kind = 'files'`},
	{Name: "sites_with_legacy_data", Query: `SELECT count(DISTINCT c.site_id) FROM collection_items c JOIN sites s ON s.id = c.site_id WHERE s.deleted_at IS NULL AND c.deleted_at IS NULL`},
	{Name: "sites_with_visitor_signin", Query: `SELECT count(DISTINCT v.site_id) FROM site_visitor_signins v JOIN sites s ON s.id = v.site_id WHERE s.deleted_at IS NULL`},
	{Name: "visitor_signins_total", Query: `SELECT count(*) FROM site_visitor_signins WHERE first_at < $1::date + 1`},
	{Name: "sites_access_specific", Query: `SELECT count(*) FROM sites WHERE deleted_at IS NULL AND access = 'specific'`},
	{Name: "sites_with_named_viewers", Query: `SELECT count(DISTINCT v.site_id) FROM site_viewers v JOIN sites s ON s.id = v.site_id WHERE s.deleted_at IS NULL`},
	{Name: "sites_with_passcode", Query: `SELECT count(*) FROM sites WHERE deleted_at IS NULL AND passcode_enc IS NOT NULL`},
	{Name: "sites_custom_domain", Query: `SELECT count(*) FROM sites WHERE deleted_at IS NULL AND custom_domain IS NOT NULL`},
	{Name: "sites_custom_domain_cert_live", Query: `SELECT count(*) FROM sites WHERE deleted_at IS NULL AND custom_domain IS NOT NULL AND domain_cert_status = 'live'`},
	{Name: "address_families", Query: `SELECT count(*) FROM address_families`},
	{Name: "certs_person_wildcard", Query: `SELECT count(*) FROM domain_cert_requests`},
	{Name: "certs_family", Query: `SELECT count(*) FROM family_cert_requests`},
	{Name: "sites_with_notifications", Query: `SELECT count(DISTINCT site_id) FROM site_notifications`},
	{Name: "connector_grants_active_30d", Query: `SELECT count(*) FROM oauth_grants WHERE last_used_at >= $1::date - 29`},
	{Name: "connector_accounts_active_30d", Query: `SELECT count(DISTINCT user_id) FROM oauth_grants WHERE last_used_at >= $1::date - 29`},
	{Name: "storage_resources", Query: `SELECT count(*) FROM site_storage_resources`},
	{Name: "resources_read_anyone", Query: `SELECT count(*) FROM site_storage_resources WHERE acc_read = 'anyone'`},
	{Name: "resources_add_anyone", Query: `SELECT count(*) FROM site_storage_resources WHERE acc_add = 'anyone'`},
	{Name: "resources_add_checked", Query: `SELECT count(*) FROM site_storage_resources WHERE acc_add = 'checked'`},
	{Name: "resources_add_signed_in", Query: `SELECT count(*) FROM site_storage_resources WHERE acc_add = 'signed-in'`},
	{Name: "resources_own_reads", Query: `SELECT count(*) FROM site_storage_resources WHERE acc_read = 'own'`},
	{Name: "sites_suspended", Query: `SELECT count(*) FROM sites WHERE deleted_at IS NULL AND suspended_at IS NOT NULL`},
	{Name: "accounts_suspended", Query: `SELECT count(*) FROM users WHERE suspended_at IS NOT NULL`},
	{Name: "kv_rows", Query: `SELECT count(*) FROM site_storage_kv`},
	{Name: "kv_value_bytes", Query: `SELECT COALESCE(sum(pg_column_size(value)), 0) FROM site_storage_kv`},
	// Compressed Postgres bytes stay Postgres-only. Text bytes overlap both stores.
	{Name: "kv_value_text_bytes", Query: `SELECT COALESCE(sum(octet_length(value::text)), 0) FROM site_storage_kv`},
	{Name: "kv_file_sites_skipped", Query: `SELECT 0`},
	{Name: "legacy_rows", Query: `SELECT count(*) FROM collection_items WHERE deleted_at IS NULL`},
	{Name: "db_bytes", Query: `SELECT pg_database_size(current_database())`},
	{Name: "api_calls_day", Query: `SELECT COALESCE(sum(calls), 0) FROM api_request_daily WHERE day = $1::date`},
	{Name: "api_errors_5xx_day", Query: `SELECT COALESCE(sum(calls), 0) FROM api_request_daily WHERE day = $1::date AND status >= 500`},
	{Name: "api_refused_429_day", Query: `SELECT COALESCE(sum(calls), 0) FROM api_request_daily WHERE day = $1::date AND status = 429`},
	{Name: "api_denied_401_403_day", Query: `SELECT COALESCE(sum(calls), 0) FROM api_request_daily WHERE day = $1::date AND status IN (401, 403)`},
	{Name: "api_mcp_calls_day", Query: `SELECT COALESCE(sum(calls), 0) FROM api_request_daily WHERE day = $1::date AND route = 'POST /mcp'`},
	{Name: "api_data_reads_day", Query: `SELECT COALESCE(sum(calls), 0) FROM api_request_daily WHERE day = $1::date AND route ~ '/storage/|/collections|/state|/data$|/data/' AND (route LIKE 'GET %' OR route LIKE 'HEAD %')`},
	{Name: "api_data_writes_day", Query: `SELECT COALESCE(sum(calls), 0) FROM api_request_daily WHERE day = $1::date AND route ~ '/storage/|/collections|/state|/data$|/data/' AND route !~ '^(GET|HEAD) '`},
	{Name: "api_deploys_day", Query: `SELECT COALESCE(sum(calls), 0) FROM api_request_daily WHERE day = $1::date AND status < 300 AND route ~ '^(POST|PUT) /v1/sites/\{sitename\}(/files)?$'`},
	{Name: "bytes_out_pages_day", Query: `SELECT COALESCE(sum(bytes), 0) FROM traffic_daily WHERE day = $1::date AND kind = 'pages'`},
	{Name: "bytes_out_api_day", Query: `SELECT COALESCE(sum(bytes), 0) FROM traffic_daily WHERE day = $1::date AND kind = 'api'`},
	{Name: "requests_pages_day", Query: `SELECT COALESCE(sum(requests), 0) FROM traffic_daily WHERE day = $1::date AND kind = 'pages'`},
	{Name: "net_tx_bytes_day", Query: `SELECT COALESCE(sum(tx_bytes), 0) FROM net_usage_daily WHERE day = $1::date`},
	{Name: "net_rx_bytes_day", Query: `SELECT COALESCE(sum(rx_bytes), 0) FROM net_usage_daily WHERE day = $1::date`},
	{Name: "process_restarts_day", Query: `SELECT count(DISTINCT started_at) FROM paper_process_sample WHERE at >= $1::date AND at < $1::date + 1 AND started_at >= $1::date`},
	{Name: "sample_minutes_missing_day", Query: `SELECT GREATEST(0, 1440 - 5 * count(*)) FROM paper_process_sample WHERE at >= $1::date AND at < $1::date + 1`},
}

// Enumerate markers, never walk orphan files. Collection is best effort per
// site; skipped sites are recorded alongside the aggregate.
func (c *Collector) kvFileTotals(ctx context.Context) (entries, bytes, skipped int64, err error) {
	rows, err := c.db.QueryContext(ctx, `SELECT DISTINCT site_id::text FROM site_storage_resources WHERE kind='kv' AND kv_store='sqlite'`)
	if err != nil {
		return 0, 0, 0, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return 0, 0, 0, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, 0, 0, err
	}
	for _, id := range ids {
		n, b, e := c.readKVFileStats(ctx, id)
		if e != nil {
			skipped++
			continue
		}
		entries += n
		bytes += b
	}
	return entries, bytes, skipped, nil
}

func (c *Collector) readKVFileStats(ctx context.Context, siteID string) (int64, int64, error) {
	if c.KVFileStats == nil {
		return 0, 0, fmt.Errorf("KV file statistics reader is not configured")
	}
	return c.KVFileStats(ctx, siteID)
}
