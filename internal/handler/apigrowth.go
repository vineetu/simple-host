package handler

import (
	"bufio"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/lib/pq"

	"github.com/vsriram/simple-host/internal/analytics"
	"github.com/vsriram/simple-host/internal/auth"
	"github.com/vsriram/simple-host/internal/config"
	"github.com/vsriram/simple-host/internal/geoip"
)

// API growth: the admin page's "how is the API growing" views.
//
// api_growth_daily holds API calls (/v1/* and /mcp, the same calls
// api_request_daily counts) per day twice over: by caller country and by kind
// of call. Both are plain counters written by the same flush as the API
// traffic tables (apimetrics.go), so no address of any kind is stored, and
// they are kept far longer (API_GROWTH_RETENTION_DAYS, default 400) than the
// 30 days of shortened caller IPs.
//
// The country is looked up on this box from the local DB-IP file
// (internal/geoip) at the moment the call is counted, from the shortened
// address. Nothing is sent anywhere.

const (
	growthDimCountry = "country"
	growthDimGroup   = "group"
	// unknownCountry is a caller with no country: this server itself (health
	// checks, local tools), a private address, or one the database lacks.
	unknownCountry = "XX"
)

// growthGroups is every kind of call, in the order the admin page stacks them.
var growthGroups = []string{"deploy", "data", "auth", "connector", "admin", "other"}

// apiRouteGroup sorts one counted route ("METHOD /pattern", as stored in
// api_request_daily) into a kind of call.
//
//	connector  the MCP endpoint AI apps call (/mcp)
//	admin      /v1/admin/*
//	auth       signing in, keys, visitor sign-in, site passcodes
//	data       a site's saved data: state, data, lists, the visitor's own record
//	deploy     sites, versions, files, AI builds, domains and everything else
//	           about a site
//	other      the rest: the account itself, skills, analytics lists, setup
func apiRouteGroup(route string) string {
	p := route
	if i := strings.IndexByte(p, ' '); i >= 0 {
		p = p[i+1:]
	}
	if p == "/mcp" || strings.HasPrefix(p, "/mcp/") {
		return "connector"
	}
	if !strings.HasPrefix(p, "/v1/") {
		return "other"
	}
	seg := strings.Split(strings.Trim(p, "/"), "/")[1:] // drop "v1"
	if len(seg) == 0 {
		return "other"
	}
	switch seg[0] {
	case "admin":
		return "admin"
	case "auth", "visitor", "site-unlock":
		return "auth"
	case "data-notify":
		return "data"
	case "generate", "events", "export":
		return "deploy"
	case "me":
		if len(seg) > 1 {
			switch seg[1] {
			case "keys", "api-key", "email", "identities", "sign-out", "connections":
				return "auth"
			}
		}
		return "other"
	case "u":
		// /v1/u/{handle}/sites/{name}/... is the same API as /v1/sites/{name}/...
		if len(seg) >= 3 && seg[2] == "sites" {
			return siteRouteGroup(seg[3:])
		}
		return "deploy"
	case "sites":
		return siteRouteGroup(seg[1:])
	}
	return "other"
}

// siteRouteGroup classifies what follows /v1/sites: {name}/<rest...>.
func siteRouteGroup(seg []string) string {
	if len(seg) < 2 {
		return "deploy" // the list of sites, or one site itself
	}
	switch seg[1] {
	case "state", "data", "collections", "me", "savers":
		return "data"
	case "visitor":
		return "auth"
	}
	return "deploy"
}

// countryOf is the ISO code of a (shortened) caller address, or XX.
func (m *APIMetrics) countryOf(ip string) string {
	if m.geo == nil || isPrivateIP(ip) {
		return unknownCountry
	}
	if cc := strings.ToUpper(m.geo.Lookup(ip).ISO); len(cc) == 2 {
		return cc
	}
	return unknownCountry
}

type growthKey struct{ dim, key string }

// flushGrowth writes one flush's growth counters, as one statement.
func flushGrowth(ctx context.Context, q interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, day string, g map[growthKey]int64) error {
	if len(g) == 0 {
		return nil
	}
	dims := make([]string, 0, len(g))
	keys := make([]string, 0, len(g))
	calls := make([]int64, 0, len(g))
	for k, n := range g {
		dims = append(dims, k.dim)
		keys = append(keys, k.key)
		calls = append(calls, n)
	}
	dayExpr := "CURRENT_DATE"
	args := []any{pq.Array(dims), pq.Array(keys), pq.Array(calls)}
	if day != "" {
		dayExpr = "$4::date"
		args = append(args, day)
	}
	_, err := q.ExecContext(ctx, `
		INSERT INTO api_growth_daily (day, dim, key, calls)
		SELECT `+dayExpr+`, d, k, c FROM unnest($1::text[], $2::text[], $3::bigint[]) AS t(d, k, c)
		ON CONFLICT (day, dim, key) DO UPDATE SET calls = api_growth_daily.calls + EXCLUDED.calls`, args...)
	return err
}

// ---------------------------------------------------------------------------
// Backfill
// ---------------------------------------------------------------------------

// NewAPIGrowthBackfiller is an APIMetrics for the one-off backfill command:
// it counts nothing live and starts no flush loop.
func NewAPIGrowthBackfiller(db *sql.DB, geo *geoip.DB) *APIMetrics {
	return &APIMetrics{db: db, geo: geo, routes: map[routeKey]int64{}, ips: map[string]*ipAgg{}}
}

// BackfillGrowth fills api_growth_daily from the API traffic tables for every
// day they still hold (API_METRICS_RETENTION_DAYS): kinds of call from
// api_request_daily's routes, countries from api_ip_daily's shortened
// addresses.
//
// It adds only what is missing: for each day and key it adds the amount by
// which the source exceeds what is already there. The live flush writes both
// sets of tables together, so a second run adds nothing, and the day of the
// upgrade gets exactly the calls counted before the new build started. It runs
// at every start and from `simple-host api-growth-backfill`.
func (m *APIMetrics) BackfillGrowth(ctx context.Context) (added int64, err error) {
	m.flushMu.Lock()
	defer m.flushMu.Unlock()

	type dk struct {
		day string
		growthKey
	}
	src := map[dk]int64{}

	rows, err := m.db.QueryContext(ctx, `SELECT day::text, route, SUM(calls) FROM api_request_daily GROUP BY day, route`)
	if err != nil {
		return 0, fmt.Errorf("read api_request_daily: %w", err)
	}
	for rows.Next() {
		var day, route string
		var n int64
		if err := rows.Scan(&day, &route, &n); err != nil {
			rows.Close()
			return 0, err
		}
		src[dk{day, growthKey{growthDimGroup, apiRouteGroup(route)}}] += n
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	rows, err = m.db.QueryContext(ctx, `SELECT day::text, ip, SUM(calls) FROM api_ip_daily GROUP BY day, ip`)
	if err != nil {
		return 0, fmt.Errorf("read api_ip_daily: %w", err)
	}
	for rows.Next() {
		var day, ip string
		var n int64
		if err := rows.Scan(&day, &ip, &n); err != nil {
			rows.Close()
			return 0, err
		}
		src[dk{day, growthKey{growthDimCountry, m.countryOf(ip)}}] += n
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(src) == 0 {
		return 0, nil
	}

	have := map[dk]int64{}
	rows, err = m.db.QueryContext(ctx, `
		SELECT day::text, dim, key, calls FROM api_growth_daily
		WHERE day >= (SELECT LEAST(
			(SELECT MIN(day) FROM api_request_daily),
			(SELECT MIN(day) FROM api_ip_daily)))`)
	if err != nil {
		return 0, fmt.Errorf("read api_growth_daily: %w", err)
	}
	for rows.Next() {
		var k dk
		var n int64
		if err := rows.Scan(&k.day, &k.dim, &k.key, &n); err != nil {
			rows.Close()
			return 0, err
		}
		have[k] = n
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	byDay := map[string]map[growthKey]int64{}
	for k, n := range src {
		if d := n - have[k]; d > 0 {
			if byDay[k.day] == nil {
				byDay[k.day] = map[growthKey]int64{}
			}
			byDay[k.day][k.growthKey] = d
			added += d
		}
	}
	if len(byDay) == 0 {
		return 0, nil
	}
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	for day, g := range byDay {
		if err := flushGrowth(ctx, tx, day, g); err != nil {
			return 0, err
		}
	}
	return added, tx.Commit()
}

// BackfillGrowthFromLogs adds /mcp calls found in nginx access logs (the
// default "combined" format) for days on which api_growth_daily has no
// connector calls at all. Before this build, /mcp was not counted anywhere,
// so the logs still on the box are the only record of it. A day that already
// has connector calls is left alone, so running it twice changes nothing.
//
// Addresses are read from the log, shortened, turned into a country on this
// box and dropped; none is stored. Files ending in .gz are decompressed.
func (m *APIMetrics) BackfillGrowthFromLogs(ctx context.Context, paths []string) (added int64, err error) {
	perDay := map[string]map[growthKey]int64{}
	for _, p := range paths {
		if err := m.scanLogForMCP(p, perDay); err != nil {
			return 0, err
		}
	}
	if len(perDay) == 0 {
		return 0, nil
	}

	m.flushMu.Lock()
	defer m.flushMu.Unlock()
	covered := map[string]bool{}
	rows, err := m.db.QueryContext(ctx, `SELECT DISTINCT day::text FROM api_growth_daily WHERE dim = $1 AND key = 'connector' AND calls > 0`, growthDimGroup)
	if err != nil {
		return 0, err
	}
	for rows.Next() {
		var d string
		if rows.Scan(&d) == nil {
			covered[d] = true
		}
	}
	rows.Close()

	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	for day, g := range perDay {
		if covered[day] {
			continue
		}
		if err := flushGrowth(ctx, tx, day, g); err != nil {
			return 0, err
		}
		added += g[growthKey{growthDimGroup, "connector"}]
	}
	return added, tx.Commit()
}

// scanLogForMCP counts /mcp requests per UTC day in one combined-format log.
func (m *APIMetrics) scanLogForMCP(path string, perDay map[string]map[growthKey]int64) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	var r io.Reader = f
	if strings.HasSuffix(path, ".gz") {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		defer gz.Close()
		r = gz
	}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		ip, day, ok := parseMCPLogLine(sc.Text())
		if !ok {
			continue
		}
		g := perDay[day]
		if g == nil {
			g = map[growthKey]int64{}
			perDay[day] = g
		}
		g[growthKey{growthDimGroup, "connector"}]++
		g[growthKey{growthDimCountry, m.countryOf(truncateIP(ip))}]++
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// parseMCPLogLine reads one "combined" line:
//
//	1.2.3.4 - - [30/Sep/2026:00:00:03 +0000] "POST /mcp HTTP/1.1" 200 ...
//
// and reports the caller and UTC day when it is a call to /mcp. Preflight
// OPTIONS requests are left out, as the live count leaves them out.
func parseMCPLogLine(line string) (ip, day string, ok bool) {
	sp := strings.IndexByte(line, ' ')
	lb := strings.IndexByte(line, '[')
	rb := strings.IndexByte(line, ']')
	if sp <= 0 || lb < 0 || rb < lb {
		return "", "", false
	}
	q1 := strings.IndexByte(line[rb:], '"')
	if q1 < 0 {
		return "", "", false
	}
	req := line[rb+q1+1:]
	if q2 := strings.IndexByte(req, '"'); q2 >= 0 {
		req = req[:q2]
	}
	parts := strings.Fields(req)
	if len(parts) < 2 || parts[0] == "OPTIONS" {
		return "", "", false
	}
	p := parts[1]
	if i := strings.IndexByte(p, '?'); i >= 0 {
		p = p[:i]
	}
	if p != "/mcp" && !strings.HasPrefix(p, "/mcp/") {
		return "", "", false
	}
	ts, err := time.Parse("02/Jan/2006:15:04:05 -0700", line[lb+1:rb])
	if err != nil {
		return "", "", false
	}
	return line[:sp], ts.UTC().Format("2006-01-02"), true
}

// ---------------------------------------------------------------------------
// Admin endpoint: GET /v1/admin/growth?range=7d|14d|30d|6m
// ---------------------------------------------------------------------------

// growthRanges are the ranges the admin page offers, in days. Six months is
// 26 weeks, so both it and its previous period start on the same weekday.
var growthRanges = map[string]int{"7d": 7, "14d": 14, "30d": 30, "6m": 182}

const growthCacheTTL = time.Minute

// growthWindow is one range's current and previous periods, inclusive days.
type growthWindow struct {
	Days             int
	From, To         time.Time
	PrevFrom, PrevTo time.Time
	dates            []string
}

// newGrowthWindow ends the period on today and puts the previous period of
// the same length right before it.
func newGrowthWindow(today time.Time, days int) growthWindow {
	today = time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC)
	w := growthWindow{Days: days, To: today, From: today.AddDate(0, 0, -(days - 1))}
	w.PrevTo = w.From.AddDate(0, 0, -1)
	w.PrevFrom = w.From.AddDate(0, 0, -days)
	for d := w.From; !d.After(w.To); d = d.AddDate(0, 0, 1) {
		w.dates = append(w.dates, d.Format("2006-01-02"))
	}
	return w
}

// changePct is the change from prev to cur in percent, one decimal, or nil
// when there is nothing to compare with.
func changePct(cur, prev int64, comparable bool) *float64 {
	if !comparable || prev <= 0 {
		return nil
	}
	v := math.Round(float64(cur-prev)/float64(prev)*1000) / 10
	return &v
}

type growthSeries struct {
	Total     int64              `json:"total"`
	Previous  int64              `json:"previous"`
	ChangePct *float64           `json:"change_pct"`
	Daily     []int64            `json:"daily"`
	Groups    map[string][]int64 `json:"groups,omitempty"`
}

type growthCountry struct {
	Code  string `json:"code"`
	Name  string `json:"name"`
	Calls int64  `json:"calls"`
}

type growthResponse struct {
	Range         string          `json:"range"`
	Days          int             `json:"days"`
	From          string          `json:"from"`
	To            string          `json:"to"`
	TrackingSince *string         `json:"tracking_since"`
	Dates         []string        `json:"dates"`
	GroupOrder    []string        `json:"group_order"`
	API           growthSeries    `json:"api"`
	Users         growthSeries    `json:"users"`
	Sites         growthSeries    `json:"sites"`
	Countries     []growthCountry `json:"countries"`
	UnknownCalls  int64           `json:"unknown_country_calls"`
	Retention     int             `json:"retention_days"`
	GeneratedAt   string          `json:"generated_at"`
}

type growthCacheEntry struct {
	at   time.Time
	body []byte
}

// AdminGrowth answers GET /v1/admin/growth. Admin-gated like
// /v1/admin/api-analytics (a signed-in non-admin gets 404). Every number comes
// from day-granular aggregates plus two GROUP BYs over users and sites, and
// each range's answer is cached for a minute.
func (m *APIMetrics) AdminGrowth(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
		return
	}
	if !user.IsAdmin {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "not found"})
		return
	}
	rng := r.URL.Query().Get("range")
	if rng == "" {
		rng = "30d"
	}
	days, ok := growthRanges[rng]
	if !ok {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "range must be one of 7d, 14d, 30d, 6m"})
		return
	}

	m.cacheMu.Lock()
	if e, ok := m.cache[rng]; ok && time.Since(e.at) < growthCacheTTL {
		m.cacheMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.Write(e.body)
		return
	}
	m.cacheMu.Unlock()

	m.flush() // fold in the calls of the last few seconds
	out, err := m.growthReport(r.Context(), rng, days)
	if err != nil {
		log.Printf("api growth: %v", err)
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "growth query failed"})
		return
	}
	body, _ := json.Marshal(out)
	m.cacheMu.Lock()
	if m.cache == nil {
		m.cache = map[string]growthCacheEntry{}
	}
	m.cache[rng] = growthCacheEntry{at: time.Now(), body: body}
	m.cacheMu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(body)
}

// growthReport builds one range's answer. "Today" is the database's CURRENT_DATE,
// the same clock every counter is written with.
func (m *APIMetrics) growthReport(ctx context.Context, rng string, days int) (*growthResponse, error) {
	var today time.Time
	if err := m.db.QueryRowContext(ctx, `SELECT CURRENT_DATE`).Scan(&today); err != nil {
		return nil, err
	}
	win := newGrowthWindow(today, days)
	idx := make(map[string]int, len(win.dates))
	for i, d := range win.dates {
		idx[d] = i
	}
	out := &growthResponse{
		Range: rng, Days: days,
		From: win.dates[0], To: win.dates[len(win.dates)-1],
		Dates: win.dates, GroupOrder: growthGroups,
		Retention:   config.Active().APIGrowthRetention,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		API:         growthSeries{Daily: make([]int64, days), Groups: map[string][]int64{}},
		Users:       growthSeries{Daily: make([]int64, days)},
		Sites:       growthSeries{Daily: make([]int64, days)},
		Countries:   []growthCountry{},
	}
	for _, g := range growthGroups {
		out.API.Groups[g] = make([]int64, days)
	}
	prevFrom, to := win.PrevFrom.Format("2006-01-02"), win.To.Format("2006-01-02")
	from := win.From.Format("2006-01-02")

	var since sql.NullString
	if err := m.db.QueryRowContext(ctx, `SELECT MIN(day)::text FROM api_growth_daily`).Scan(&since); err != nil {
		return nil, err
	}
	if since.Valid {
		out.TrackingSince = &since.String
	}

	// Calls by kind, per day, over both periods.
	rows, err := m.db.QueryContext(ctx, `
		SELECT day::text, key, calls FROM api_growth_daily
		WHERE dim = $1 AND day BETWEEN $2::date AND $3::date`, growthDimGroup, prevFrom, to)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var day, key string
		var n int64
		if err := rows.Scan(&day, &key, &n); err != nil {
			rows.Close()
			return nil, err
		}
		i, cur := idx[day]
		if !cur {
			out.API.Previous += n
			continue
		}
		if _, known := out.API.Groups[key]; !known {
			key = "other"
		}
		out.API.Groups[key][i] += n
		out.API.Daily[i] += n
		out.API.Total += n
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Only compare with a previous period that was counted from its first day.
	apiComparable := since.Valid && since.String <= prevFrom
	out.API.ChangePct = changePct(out.API.Total, out.API.Previous, apiComparable)

	// Calls by country over the current period.
	rows, err = m.db.QueryContext(ctx, `
		SELECT key, SUM(calls) FROM api_growth_daily
		WHERE dim = $1 AND day BETWEEN $2::date AND $3::date
		GROUP BY key ORDER BY SUM(calls) DESC, key`, growthDimCountry, from, to)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var c growthCountry
		if err := rows.Scan(&c.Code, &c.Calls); err != nil {
			rows.Close()
			return nil, err
		}
		if c.Code == unknownCountry {
			out.UnknownCalls += c.Calls
			continue
		}
		c.Name = analytics.CountryName(c.Code)
		out.Countries = append(out.Countries, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// New accounts and new sites per day. Sites include ones later deleted
	// (they stay rows until purged); a purged site no longer counts.
	for _, q := range []struct {
		table string
		s     *growthSeries
	}{{"users", &out.Users}, {"sites", &out.Sites}} {
		rows, err := m.db.QueryContext(ctx, `
			SELECT created_at::date::text, COUNT(*) FROM `+q.table+`
			WHERE created_at >= $1::date AND created_at < $2::date + 1
			GROUP BY 1`, prevFrom, to)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var day string
			var n int64
			if err := rows.Scan(&day, &n); err != nil {
				rows.Close()
				return nil, err
			}
			if i, cur := idx[day]; cur {
				q.s.Daily[i] += n
				q.s.Total += n
			} else {
				q.s.Previous += n
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
		q.s.ChangePct = changePct(q.s.Total, q.s.Previous, true)
	}
	sort.SliceStable(out.Countries, func(i, j int) bool { return out.Countries[i].Calls > out.Countries[j].Calls })
	return out, nil
}
