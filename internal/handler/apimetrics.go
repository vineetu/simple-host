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
	"github.com/vsriram/simple-host/internal/config"
	"github.com/vsriram/simple-host/internal/geoip"
)

// APIMetrics counts every /v1/* request into per-day aggregates so the admin
// page can answer "what is being called, by whom, from where" at a glance.
//
// Design constraints, in order:
//   - Never slow a request down: counting is an in-memory map bump; the DB
//     write happens on a background flush tick.
//   - Bounded cardinality: routes are their mux pattern, so
//     /v1/sites/<any-name>/files is ONE row.
//   - Only real API calls count. A request that matches no API route (bots
//     probing random paths, a method a route does not take) is not an API
//     call and is not counted anywhere.
//   - Calls from this server itself (loopback and the box's own public
//     address, CUSTOM_DOMAIN_IP) are our own checks and
//     canaries: they go to api_self_daily, shown as "from this server", and
//     never into the calls, errors, callers or growth numbers.
//   - Caller IPs are stored truncated (IPv4 /24, IPv6 /48; see truncateIP):
//     enough for the network/geo columns, not a person's exact address.
//     Pruned after retentionDays. Rate limiting uses the live request IP and
//     never reads this table.
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

	// mux resolves the route of a request the mux never saw with its
	// pattern set (a middleware cloned it, or refused it before the mux).
	mux *http.ServeMux
	// selfIPs are this server's own public addresses (full, not shortened).
	selfIPs map[string]bool

	mu     sync.Mutex
	routes map[routeKey]int64
	self   map[routeKey]int64 // api_self_daily: calls from this server
	ips    map[string]*ipAgg
	growth map[growthKey]int64 // api_growth_daily counters (apigrowth.go)

	// flushMu serialises writers of the aggregate tables: the flush (from
	// the loop and from the admin endpoints) and the growth backfill, which
	// compares the two sets of tables and must not see half a flush.
	flushMu sync.Mutex

	cacheMu sync.Mutex
	cache   map[string]growthCacheEntry // GET /v1/admin/growth, per range
}

type routeKey struct {
	route  string
	status int
}

type ipAgg struct {
	calls     int64
	lastRoute string
}

func NewAPIMetrics(db *sql.DB, geo *geoip.DB) *APIMetrics {
	m := &APIMetrics{
		db:     db,
		geo:    geo,
		routes: make(map[routeKey]int64),
		self:   make(map[routeKey]int64),
		ips:    make(map[string]*ipAgg),
		growth: make(map[growthKey]int64),
	}
	go m.flushLoop()
	return m
}

// Wrap counts /v1/* and /mcp traffic around the mux. Static pages, health probes, and
// hosted-site content are deliberately not counted — this is API analytics,
// not visitor analytics (that already exists per site).
func (m *APIMetrics) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The "Ask" assistants are left out entirely: its visitors are readers
		// of a public page, and nothing about them is kept for it. The setup
		// helper's check and assistant likewise.
		if !(strings.HasPrefix(r.URL.Path, "/v1/") || r.URL.Path == "/mcp") || r.URL.Path == "/v1/ask" || r.URL.Path == "/v1/setup/check" || r.URL.Path == "/v1/setup/assist" {
			next.ServeHTTP(w, r)
			return
		}
		rec := &statusCapture{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		route := r.Pattern // set by ServeMux on match (Go ≥1.23)
		if route == "" && m.mux != nil {
			_, route = m.mux.Handler(r)
		}
		if !isAPIRoute(route) {
			return // matched no API route: not an API call
		}
		raw := clientIP(r)
		if m.isSelf(raw) {
			m.recordSelf(route, rec.status)
			return
		}
		ip := truncateIP(raw)
		m.record(route, rec.status, ip, m.countryOf(ip))
	})
}

// SetRouting gives the counter the API mux (to name the route of a request a
// middleware answered or cloned before the mux set its pattern) and this
// server's own public addresses, whose calls are counted apart.
func (m *APIMetrics) SetRouting(mux *http.ServeMux, selfIPs ...string) {
	m.mux = mux
	m.selfIPs = map[string]bool{}
	for _, s := range selfIPs {
		if ip := net.ParseIP(strings.TrimSpace(s)); ip != nil {
			m.selfIPs[ip.String()] = true
		}
	}
}

// isSelf reports a caller that is this server: loopback, or one of its own
// public addresses. Private networks are not assumed to be this server: a
// deployment behind an internal load balancer sees its real callers there.
func (m *APIMetrics) isSelf(s string) bool {
	ip := net.ParseIP(s)
	return ip != nil && (ip.IsLoopback() || m.selfIPs[ip.String()])
}

// isAPIRoute reports whether a mux pattern ("METHOD [host]/path") is an API
// route: under /v1/, or the connector at /mcp. Every API route is registered
// with a method, so a pattern without one is not an API route: the mux's
// trailing-slash redirect reports the literal path as its pattern, which
// would mint a row per site name. The site catch-all ("GET /") and an empty
// pattern (no route matched, or not for this method) are not API routes either.
func isAPIRoute(pattern string) bool {
	i := strings.IndexByte(pattern, ' ')
	if i < 0 {
		return false
	}
	p := pattern[i+1:]
	if i := strings.IndexByte(p, '/'); i > 0 {
		p = p[i:] // drop a host
	}
	return strings.HasPrefix(p, "/v1/") || p == "/mcp" || strings.HasPrefix(p, "/mcp/")
}

// truncateIP keeps the network part only: IPv4 → a.b.c.0, IPv6 → its /48.
// Loopback stays as-is (it is this box, and ::1/48 would stop reading as
// local). Anything unparseable is returned unchanged.
func truncateIP(s string) string {
	ip := net.ParseIP(s)
	if ip == nil || ip.IsLoopback() {
		return s
	}
	if v4 := ip.To4(); v4 != nil {
		return v4.Mask(net.CIDRMask(24, 32)).String()
	}
	return ip.Mask(net.CIDRMask(48, 128)).String()
}

type statusCapture struct {
	http.ResponseWriter
	status int
}

func (s *statusCapture) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (m *APIMetrics) record(route string, status int, ip, country string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.routes[routeKey{route, status}]++
	if m.growth == nil {
		m.growth = make(map[growthKey]int64)
	}
	m.growth[growthKey{growthDimGroup, apiRouteGroup(route)}]++
	m.growth[growthKey{growthDimCountry, country}]++
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

// recordSelf counts a call from this server itself.
func (m *APIMetrics) recordSelf(route string, status int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.self == nil {
		m.self = make(map[routeKey]int64)
	}
	m.self[routeKey{route, status}]++
	if m.growth == nil {
		m.growth = make(map[growthKey]int64)
	}
	m.growth[growthKey{growthDimSource, growthKeySelf}]++
}

func (m *APIMetrics) flushLoop() {
	// Prune once at startup too: the service can restart more often than every
	// 6 hours, and a ticker alone would then never fire, keeping shortened IPs
	// past the 30 days the privacy page promises.
	m.pruneOld()
	// Fill the growth counts from the traffic tables (a no-op once done).
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	if n, err := m.BackfillGrowth(ctx); err != nil {
		log.Printf("api growth backfill: %v", err)
	} else if n > 0 {
		log.Printf("api growth backfill: added %d calls from the API traffic tables", n)
	}
	cancel()
	tick := time.NewTicker(config.Active().APIMetricsFlush)
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
	m.flushMu.Lock()
	defer m.flushMu.Unlock()
	m.mu.Lock()
	routes := m.routes
	self := m.self
	ips := m.ips
	growth := m.growth
	m.routes = make(map[routeKey]int64)
	m.self = make(map[routeKey]int64)
	m.ips = make(map[string]*ipAgg)
	m.growth = make(map[growthKey]int64)
	m.mu.Unlock()
	if len(routes) == 0 && len(self) == 0 && len(ips) == 0 && len(growth) == 0 {
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
	for k, n := range self {
		if _, err := m.db.ExecContext(ctx, `
			INSERT INTO api_self_daily (day, route, status, calls)
			VALUES (CURRENT_DATE, $1, $2, $3)
			ON CONFLICT (day, route, status) DO UPDATE SET calls = api_self_daily.calls + EXCLUDED.calls`,
			k.route, k.status, n); err != nil {
			log.Printf("api metrics flush (self): %v", err)
			return
		}
	}
	// Growth next, so a DB that fails mid-flush loses the traffic detail
	// rather than the long-kept counts; the backfill repairs a gap either way.
	if err := flushGrowth(ctx, m.db, "", growth); err != nil {
		log.Printf("api metrics flush (growth): %v", err)
		return
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
		`DELETE FROM api_self_daily WHERE day < CURRENT_DATE - $1::int`,
	} {
		if _, err := m.db.ExecContext(ctx, q, metricsRetentionDays()); err != nil {
			log.Printf("api metrics prune: %v", err)
		}
	}
	if _, err := m.db.ExecContext(ctx, `DELETE FROM api_growth_daily WHERE day < CURRENT_DATE - $1::int`, config.Active().APIGrowthRetention); err != nil {
		log.Printf("api growth prune: %v", err)
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
	CallsToday  int64 `json:"calls_today"`
	CallsWeek   int64 `json:"calls_week"`
	IPsToday    int64 `json:"ips_today"`
	ErrorsToday int64 `json:"errors_today"`
	// Calls from this server itself (health checks, canaries, local tools):
	// counted apart and left out of every number above.
	SelfToday       int64               `json:"self_calls_today"`
	SelfErrorsToday int64               `json:"self_errors_today"`
	AIToday         int64               `json:"ai_builds_today"`
	AIWeek          int64               `json:"ai_builds_week"`
	Routes          []apiAnalyticsRoute `json:"routes"`
	IPs             []apiAnalyticsIP    `json:"ips"`
	Retention       int                 `json:"retention_days"`
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
	out := apiAnalyticsResponse{Retention: metricsRetentionDays()}

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
		SELECT COALESCE(SUM(calls), 0), COALESCE(SUM(calls) FILTER (WHERE status >= 400), 0)
		FROM api_self_daily WHERE day = CURRENT_DATE`).Scan(&out.SelfToday, &out.SelfErrorsToday)
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
