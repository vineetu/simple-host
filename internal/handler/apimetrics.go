package handler

import (
	"context"
	"database/sql"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/vsriram/simple-host/internal/auth"
	"github.com/vsriram/simple-host/internal/geoip"
)

// APIMetrics counts every /v1/* request into per-day aggregates so the admin
// page can answer "what is being called, by whom, from where" at a glance.
//
// Design constraints, in order:
//   - Never slow a request down: counting is an in-memory map bump; the DB
//     write happens on a background flush tick.
//   - Bounded cardinality: routes are normalized to their mux pattern (or a
//     conservative fallback), so /v1/sites/<any-name>/files is ONE row.
//   - Raw caller IPs are kept — this exists to spot abuse — but only for
//     retentionDays, then pruned.
//
// "Where" and "org" come from a local database on this box (internal/geoip,
// DB-IP Lite) at the moment the admin page asks. No caller IP ever leaves the
// server for this. There is no cache table: a lookup costs microseconds, the
// page shows at most 20 IPs, and resolving live means the monthly data refresh
// applies to every row and nothing outlives the 30-day IP retention. If the
// database files are missing, the columns are simply blank.
type APIMetrics struct {
	db  *sql.DB
	geo *geoip.DB // nil = no geo at all (blank columns)

	mu     sync.Mutex
	routes map[routeKey]int64
	ips    map[string]*ipAgg
}

type routeKey struct {
	route  string
	status int
}

type ipAgg struct {
	calls     int64
	lastRoute string
}

const metricsRetentionDays = 30

func NewAPIMetrics(db *sql.DB, geo *geoip.DB) *APIMetrics {
	m := &APIMetrics{
		db:     db,
		geo:    geo,
		routes: make(map[routeKey]int64),
		ips:    make(map[string]*ipAgg),
	}
	go m.flushLoop()
	return m
}

// Wrap counts /v1/* traffic around the mux. Static pages, health probes, and
// hosted-site content are deliberately not counted — this is API analytics,
// not visitor analytics (that already exists per site).
func (m *APIMetrics) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v1/") {
			next.ServeHTTP(w, r)
			return
		}
		rec := &statusCapture{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		route := r.Pattern // set by ServeMux on match (Go ≥1.23)
		if route == "" {
			route = r.Method + " " + normalizeAPIPath(r.URL.Path)
		}
		m.record(route, rec.status, clientIP(r))
	})
}

type statusCapture struct {
	http.ResponseWriter
	status int
}

func (s *statusCapture) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// normalizeAPIPath collapses per-user path segments so unmatched requests (404s,
// old clients) cannot mint unbounded route rows. Known variable segments become
// placeholders; anything deeper is truncated.
func normalizeAPIPath(p string) string {
	seg := strings.Split(strings.Trim(p, "/"), "/")
	if len(seg) > 6 {
		seg = seg[:6]
	}
	// Positions of variable segments per API shape: /v1/sites/{name}/...,
	// /v1/u/{handle}/sites/{name}/..., /v1/skills/{name}/...
	for i := range seg {
		prev := ""
		if i > 0 {
			prev = seg[i-1]
		}
		switch prev {
		case "sites", "skills", "u", "collections", "oauth":
			seg[i] = "{x}"
		}
	}
	return "/" + strings.Join(seg, "/")
}

func (m *APIMetrics) record(route string, status int, ip string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.routes[routeKey{route, status}]++
	a := m.ips[ip]
	if a == nil {
		if len(m.ips) > 5000 { // abuse guard: never grow without bound between flushes
			return
		}
		a = &ipAgg{}
		m.ips[ip] = a
	}
	a.calls++
	a.lastRoute = route
}

func (m *APIMetrics) flushLoop() {
	tick := time.NewTicker(20 * time.Second)
	prune := time.NewTicker(6 * time.Hour)
	for {
		select {
		case <-tick.C:
			m.flush()
		case <-prune.C:
			m.pruneOld()
		}
	}
}

func (m *APIMetrics) flush() {
	m.mu.Lock()
	routes := m.routes
	ips := m.ips
	m.routes = make(map[routeKey]int64)
	m.ips = make(map[string]*ipAgg)
	m.mu.Unlock()
	if len(routes) == 0 && len(ips) == 0 {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for k, n := range routes {
		if _, err := m.db.ExecContext(ctx, `
			INSERT INTO api_request_daily (day, route, status, calls)
			VALUES (CURRENT_DATE, $1, $2, $3)
			ON CONFLICT (day, route, status) DO UPDATE SET calls = api_request_daily.calls + EXCLUDED.calls`,
			k.route, k.status, n); err != nil {
			log.Printf("api metrics flush (route): %v", err)
			return // DB down: drop this batch rather than queue forever
		}
	}
	for ip, a := range ips {
		if _, err := m.db.ExecContext(ctx, `
			INSERT INTO api_ip_daily (day, ip, calls, last_route, last_seen)
			VALUES (CURRENT_DATE, $1, $2, $3, now())
			ON CONFLICT (day, ip) DO UPDATE SET
				calls = api_ip_daily.calls + EXCLUDED.calls,
				last_route = EXCLUDED.last_route,
				last_seen = now()`,
			ip, a.calls, a.lastRoute); err != nil {
			log.Printf("api metrics flush (ip): %v", err)
			return
		}
	}
}

func (m *APIMetrics) pruneOld() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, q := range []string{
		`DELETE FROM api_request_daily WHERE day < CURRENT_DATE - $1::int`,
		`DELETE FROM api_ip_daily WHERE day < CURRENT_DATE - $1::int`,
	} {
		if _, err := m.db.ExecContext(ctx, q, metricsRetentionDays); err != nil {
			log.Printf("api metrics prune: %v", err)
		}
	}
}

// ---------------------------------------------------------------------------
// Geo resolution — local only (see internal/geoip)
// ---------------------------------------------------------------------------

// locate answers "where / whose network" for one caller IP. Private and
// loopback addresses are this server talking to itself (health checks, the
// Grok sidecar, local tooling) and are labelled as such without a lookup.
func (m *APIMetrics) locate(ip string) (where, org string) {
	if isPrivateIP(ip) {
		return "this box", "local"
	}
	if m.geo == nil {
		return "", ""
	}
	g := m.geo.Lookup(ip)
	var loc []string
	if g.City != "" {
		loc = append(loc, g.City)
	}
	if g.Country != "" {
		loc = append(loc, g.Country)
	}
	return strings.Join(loc, ", "), g.Org
}

func isPrivateIP(s string) bool {
	ip := net.ParseIP(s)
	return ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast())
}

// ---------------------------------------------------------------------------
// Admin summary endpoint
// ---------------------------------------------------------------------------

type apiAnalyticsRoute struct {
	Route  string `json:"route"`
	Today  int64  `json:"today"`
	Week   int64  `json:"week"`
	Errors int64  `json:"errors_week"`
}

type apiAnalyticsIP struct {
	IP       string `json:"ip"`
	Where    string `json:"where"`
	Org      string `json:"org"`
	Today    int64  `json:"today"`
	Week     int64  `json:"week"`
	LastSeen string `json:"last_seen"`
	LastPath string `json:"last_route"`
}

type apiAnalyticsResponse struct {
	CallsToday  int64               `json:"calls_today"`
	CallsWeek   int64               `json:"calls_week"`
	IPsToday    int64               `json:"ips_today"`
	ErrorsToday int64               `json:"errors_today"`
	AIToday     int64               `json:"ai_builds_today"`
	AIWeek      int64               `json:"ai_builds_week"`
	Routes      []apiAnalyticsRoute `json:"routes"`
	IPs         []apiAnalyticsIP    `json:"ips"`
	Retention   int                 `json:"retention_days"`
}

// AdminSummary answers GET /v1/admin/api-analytics. Admin-gated; non-admins get
// the same 404 as /v1/admin/users so the endpoint's existence stays private.
func (m *APIMetrics) AdminSummary(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
		return
	}
	if !user.IsAdmin {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "not found"})
		return
	}

	// Everything below is day-granular on small aggregate tables; a few
	// sequential queries are simpler than one clever one.
	m.flush() // fold in the last ≤20s so "today" looks live
	ctx := r.Context()
	out := apiAnalyticsResponse{Retention: metricsRetentionDays}

	row := m.db.QueryRowContext(ctx, `
		SELECT
			COALESCE(SUM(calls) FILTER (WHERE day = CURRENT_DATE), 0),
			COALESCE(SUM(calls), 0),
			COALESCE(SUM(calls) FILTER (WHERE day = CURRENT_DATE AND status >= 400), 0)
		FROM api_request_daily WHERE day > CURRENT_DATE - 7`)
	if err := row.Scan(&out.CallsToday, &out.CallsWeek, &out.ErrorsToday); err != nil {
		log.Printf("api analytics (totals): %v", err)
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "analytics query failed"})
		return
	}
	_ = m.db.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT ip) FROM api_ip_daily WHERE day = CURRENT_DATE`).Scan(&out.IPsToday)
	_ = m.db.QueryRowContext(ctx, `
		SELECT
			COALESCE(SUM(calls) FILTER (WHERE day = CURRENT_DATE), 0),
			COALESCE(SUM(calls), 0)
		FROM api_request_daily
		WHERE day > CURRENT_DATE - 7 AND route = 'POST /v1/generate' AND status < 400`).Scan(&out.AIToday, &out.AIWeek)

	rows, err := m.db.QueryContext(ctx, `
		SELECT route,
			COALESCE(SUM(calls) FILTER (WHERE day = CURRENT_DATE), 0) AS today,
			SUM(calls) AS week,
			COALESCE(SUM(calls) FILTER (WHERE status >= 400), 0) AS errs
		FROM api_request_daily WHERE day > CURRENT_DATE - 7
		GROUP BY route ORDER BY week DESC LIMIT 20`)
	if err == nil {
		for rows.Next() {
			var rt apiAnalyticsRoute
			if rows.Scan(&rt.Route, &rt.Today, &rt.Week, &rt.Errors) == nil {
				out.Routes = append(out.Routes, rt)
			}
		}
		rows.Close()
	}

	rows, err = m.db.QueryContext(ctx, `
		SELECT d.ip,
			COALESCE(SUM(d.calls) FILTER (WHERE d.day = CURRENT_DATE), 0) AS today,
			SUM(d.calls) AS week,
			MAX(d.last_seen) AS last_seen,
			(ARRAY_AGG(d.last_route ORDER BY d.last_seen DESC))[1] AS last_route
		FROM api_ip_daily d
		WHERE d.day > CURRENT_DATE - 7
		GROUP BY d.ip
		ORDER BY week DESC LIMIT 20`)
	if err == nil {
		for rows.Next() {
			var ip apiAnalyticsIP
			var lastSeen time.Time
			if rows.Scan(&ip.IP, &ip.Today, &ip.Week, &lastSeen, &ip.LastPath) == nil {
				ip.Where, ip.Org = m.locate(ip.IP)
				ip.LastSeen = lastSeen.UTC().Format(time.RFC3339)
				out.IPs = append(out.IPs, ip)
			}
		}
		rows.Close()
	}

	writeJSON(w, http.StatusOK, out)
}
