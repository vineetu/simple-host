package handler

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/vsriram/simple-host/internal/auth"
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
// Geo lookups go to ip-api.com (free, keyless, batch endpoint) from a slow
// background worker with results cached forever in ip_geo. Only the IP is
// sent — no request data — and lookups are best-effort: an outage just leaves
// the "where" column blank until the next tick.
type APIMetrics struct {
	db *sql.DB

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

func NewAPIMetrics(db *sql.DB) *APIMetrics {
	m := &APIMetrics{
		db:     db,
		routes: make(map[routeKey]int64),
		ips:    make(map[string]*ipAgg),
	}
	go m.flushLoop()
	go m.geoLoop()
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
	// /v1/u/{handle}/sites/{name}/..., /v1/templates/{id}, /v1/skills/{name}/...
	for i := range seg {
		prev := ""
		if i > 0 {
			prev = seg[i-1]
		}
		switch prev {
		case "sites", "templates", "skills", "u", "collections", "oauth":
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
// Geo resolution (ip-api.com batch, keyless, cached in ip_geo)
// ---------------------------------------------------------------------------

func (m *APIMetrics) geoLoop() {
	tick := time.NewTicker(90 * time.Second)
	for range tick.C {
		m.resolvePendingGeo()
	}
}

func (m *APIMetrics) resolvePendingGeo() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	rows, err := m.db.QueryContext(ctx, `
		SELECT DISTINCT d.ip FROM api_ip_daily d
		LEFT JOIN ip_geo g ON g.ip = d.ip
		WHERE g.ip IS NULL
		LIMIT 50`)
	if err != nil {
		log.Printf("api metrics geo (select): %v", err)
		return
	}
	var pending []string
	for rows.Next() {
		var ip string
		if rows.Scan(&ip) == nil {
			pending = append(pending, ip)
		}
	}
	rows.Close()
	if len(pending) == 0 {
		return
	}

	var lookup []string
	for _, ip := range pending {
		if isPrivateIP(ip) {
			m.saveGeo(ctx, ip, "this box", "", "local")
			continue
		}
		lookup = append(lookup, ip)
	}
	if len(lookup) == 0 {
		return
	}

	body, _ := json.Marshal(lookup)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"http://ip-api.com/batch?fields=status,country,city,org,query", bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		log.Printf("api metrics geo (lookup): %v", err)
		return
	}
	defer resp.Body.Close()
	var results []struct {
		Status  string `json:"status"`
		Country string `json:"country"`
		City    string `json:"city"`
		Org     string `json:"org"`
		Query   string `json:"query"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		log.Printf("api metrics geo (decode): %v", err)
		return
	}
	for _, g := range results {
		if g.Status != "success" {
			m.saveGeo(ctx, g.Query, "unknown", "", "")
			continue
		}
		m.saveGeo(ctx, g.Query, g.Country, g.City, g.Org)
	}
}

func (m *APIMetrics) saveGeo(ctx context.Context, ip, country, city, org string) {
	if _, err := m.db.ExecContext(ctx, `
		INSERT INTO ip_geo (ip, country, city, org) VALUES ($1, $2, $3, $4)
		ON CONFLICT (ip) DO NOTHING`, ip, country, city, org); err != nil {
		log.Printf("api metrics geo (save): %v", err)
	}
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
			COALESCE(g.city, ''), COALESCE(g.country, ''), COALESCE(g.org, ''),
			COALESCE(SUM(d.calls) FILTER (WHERE d.day = CURRENT_DATE), 0) AS today,
			SUM(d.calls) AS week,
			MAX(d.last_seen) AS last_seen,
			(ARRAY_AGG(d.last_route ORDER BY d.last_seen DESC))[1] AS last_route
		FROM api_ip_daily d LEFT JOIN ip_geo g ON g.ip = d.ip
		WHERE d.day > CURRENT_DATE - 7
		GROUP BY d.ip, g.city, g.country, g.org
		ORDER BY week DESC LIMIT 20`)
	if err == nil {
		for rows.Next() {
			var ip apiAnalyticsIP
			var city, country string
			var lastSeen time.Time
			if rows.Scan(&ip.IP, &city, &country, &ip.Org, &ip.Today, &ip.Week, &lastSeen, &ip.LastPath) == nil {
				var loc []string
				if city != "" {
					loc = append(loc, city)
				}
				if country != "" {
					loc = append(loc, country)
				}
				ip.Where = strings.Join(loc, ", ")
				ip.LastSeen = lastSeen.UTC().Format(time.RFC3339)
				out.IPs = append(out.IPs, ip)
			}
		}
		rows.Close()
	}

	writeJSON(w, http.StatusOK, out)
}
