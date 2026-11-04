package operations

import (
	"context"
	"database/sql"
	repo "github.com/vsriram/simple-host/internal/repository/operations"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/vsriram/simple-host/internal/edition"
	"github.com/vsriram/simple-host/internal/geoip"
	"github.com/vsriram/simple-host/internal/workers"
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
	startOnce  sync.Once
	workerOnce sync.Once
	runner     *workers.Runner
	tracker    *workers.Tracker
	ed         *edition.Edition
	db         *sql.DB
	geo        *geoip.DB // nil = no geo at all (blank columns)

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

func NewAPIMetrics(db *sql.DB, geo *geoip.DB, editions ...*edition.Edition) *APIMetrics {
	var ed *edition.Edition
	if len(editions) > 0 {
		ed = editions[0]
	}
	t := workers.NewTracker("operations.api_metrics")
	m := &APIMetrics{runner: workers.NewRunner(t), tracker: t,
		ed:     ed,
		db:     db,
		geo:    geo,
		routes: make(map[routeKey]int64),
		self:   make(map[routeKey]int64),
		ips:    make(map[string]*ipAgg),
		growth: make(map[growthKey]int64),
	}
	return m
}

// SetSelfIPs sets the server's own public addresses for separate accounting.
func (m *APIMetrics) SetSelfIPs(selfIPs ...string) {
	m.selfIPs = map[string]bool{}
	for _, s := range selfIPs {
		if ip := net.ParseIP(strings.TrimSpace(s)); ip != nil {
			m.selfIPs[ip.String()] = true
		}
	}
}

// Observe records a resolved API route and status after the request finishes.
func (m *APIMetrics) Observe(route string, status int, raw string) {
	if m.isSelf(raw) {
		m.recordSelf(route, status)
		return
	}
	ip := truncateIP(raw)
	m.record(route, status, ip, m.countryOf(ip))
}

// isSelf reports a caller that is this server: loopback, or one of its own
// public addresses. Private networks are not assumed to be this server: a
// deployment behind an internal load balancer sees its real callers there.
func (m *APIMetrics) isSelf(s string) bool {
	ip := net.ParseIP(s)
	return ip != nil && (ip.IsLoopback() || m.selfIPs[ip.String()])
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

// Growth backfill tools also construct APIMetrics without starting a worker.
// Initialize their tracker lazily so explicit flushes share the same reporting.
func (m *APIMetrics) initWorker() {
	m.workerOnce.Do(func() {
		if m.tracker == nil {
			m.tracker = workers.NewTracker("operations.api_metrics")
		}
		if m.runner == nil {
			m.runner = workers.NewRunner(m.tracker)
		}
	})
}
func (m *APIMetrics) Start(ctx context.Context) {
	m.startOnce.Do(func() {
		m.initWorker()
		m.runner.Go(ctx, m.flushLoop)
	})
}
func (m *APIMetrics) Stop()                    { m.initWorker(); m.runner.Stop() }
func (m *APIMetrics) Wait()                    { m.initWorker(); m.runner.Wait() }
func (m *APIMetrics) Status() []workers.Status { m.initWorker(); return m.runner.Status() }

func (m *APIMetrics) flushLoop(ctx context.Context) {
	m.tracker.Attempt()
	// Prune once at startup too: the service can restart more often than every
	// 6 hours, and a ticker alone would then never fire, keeping shortened IPs
	// past the 30 days the privacy page promises.
	m.pruneOldContext(ctx)
	// Fill the growth counts from the traffic tables (a no-op once done).
	backfillCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	if n, err := m.BackfillGrowth(backfillCtx); err != nil {
		m.tracker.Fail("growth backfill", err)
	} else if n > 0 {
		m.tracker.Progress()
		log.Printf("api growth backfill: added %d calls from the API traffic tables", n)
	}
	cancel()
	tick := time.NewTicker(m.ed.Limits().APIMetricsFlush)
	prune := time.NewTicker(6 * time.Hour)
	defer tick.Stop()
	defer prune.Stop()
	for {
		select {
		case <-ctx.Done():
			m.flushContext(context.Background()) // HTTP is drained; persist the final buffer before Wait returns.
			return
		case <-tick.C:
			// Finish a bounded buffer write even if Stop cancels the scan context.
			m.flushContext(context.WithoutCancel(ctx))
		case <-prune.C:
			m.pruneOldContext(ctx)
		}
	}
}

func (m *APIMetrics) flush() { m.flushContext(context.Background()) }
func (m *APIMetrics) flushContext(ctx context.Context) {
	m.initWorker()
	m.tracker.Attempt()
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

	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	for k, n := range routes {
		if _, err := repo.FlushAPIRoute(ctx, m.db, k.route, k.status, n); err != nil {
			m.tracker.Fail("flush route", err)
			return // DB down: drop this batch rather than queue forever
		}
	}
	for k, n := range self {
		if _, err := repo.FlushAPISelf(ctx, m.db, k.route, k.status, n); err != nil {
			m.tracker.Fail("flush self", err)
			return
		}
	}
	// Growth next, so a DB that fails mid-flush loses the traffic detail
	// rather than the long-kept counts; the backfill repairs a gap either way.
	if err := flushGrowth(ctx, m.db, "", growth); err != nil {
		m.tracker.Fail("flush growth", err)
		return
	}
	for ip, a := range ips {
		if _, err := repo.FlushAPIIP(ctx, m.db, ip, a.calls, a.lastRoute); err != nil {
			m.tracker.Fail("flush ip", err)
			return
		}
	}
	m.tracker.Progress()
}

func (m *APIMetrics) pruneOld() { m.pruneOldContext(context.Background()) }
func (m *APIMetrics) pruneOldContext(ctx context.Context) {
	m.initWorker()
	m.tracker.Attempt()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for _, q := range []string{"requests", "ips", "self"} {
		if result, err := repo.PruneAPI(ctx, m.db, q, m.ed.Limits().APIMetricsRetention); err != nil {
			m.tracker.Fail("prune", err)
		} else if n, _ := result.RowsAffected(); n > 0 {
			m.tracker.Progress()
		}
	}
	if result, err := repo.PruneAPI(ctx, m.db, "growth", m.ed.Limits().APIGrowthRetention); err != nil {
		m.tracker.Fail("growth prune", err)
	} else if n, _ := result.RowsAffected(); n > 0 {
		m.tracker.Progress()
	}
}

// ---------------------------------------------------------------------------
// Geo resolution — local only (see internal/geoip)
// ---------------------------------------------------------------------------

// locate answers "where / whose network" for one caller IP. Private and
// loopback addresses are this server talking to itself (health checks, the
// local tooling) and are labelled as such without a lookup.
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

type apiAnalyticsResponse = repo.APIAnalyticsResponse

// Summary folds the buffer into the report, then resolves each caller locally.
func (m *APIMetrics) Summary(ctx context.Context) (apiAnalyticsResponse, error) {
	m.flush()
	out, err := repo.APISummary(ctx, m.db, m.ed.Limits().APIMetricsRetention)
	if err != nil {
		return out, err
	}
	for i := range out.IPs {
		out.IPs[i].Where, out.IPs[i].Org = m.locate(out.IPs[i].IP)
	}
	return out, nil
}
