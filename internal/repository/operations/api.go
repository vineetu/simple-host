package operations

import (
	"context"
	"database/sql"
	"github.com/lib/pq"
	"log"
	"time"
)

type APIAnalyticsRoute struct {
	Route  string `json:"route"`
	Today  int64  `json:"today"`
	Week   int64  `json:"week"`
	Errors int64  `json:"errors_week"`
}

type APIAnalyticsIP struct {
	IP       string `json:"ip"`
	Where    string `json:"where"`
	Org      string `json:"org"`
	Today    int64  `json:"today"`
	Week     int64  `json:"week"`
	LastSeen string `json:"last_seen"`
	LastPath string `json:"last_route"`
}

type APIAnalyticsResponse struct {
	CallsToday  int64 `json:"calls_today"`
	CallsWeek   int64 `json:"calls_week"`
	IPsToday    int64 `json:"ips_today"`
	ErrorsToday int64 `json:"errors_today"`
	// Calls from this server itself (health checks, canaries, local tools):
	// counted apart and left out of every number above.
	SelfToday       int64               `json:"self_calls_today"`
	SelfErrorsToday int64               `json:"self_errors_today"`
	Routes          []APIAnalyticsRoute `json:"routes"`
	IPs             []APIAnalyticsIP    `json:"ips"`
	Retention       int                 `json:"retention_days"`
}

func APISummary(ctx context.Context, database *sql.DB, retention int) (APIAnalyticsResponse, error) {
	out := APIAnalyticsResponse{Retention: retention}
	row := database.QueryRowContext(ctx, `
		SELECT
			COALESCE(SUM(calls) FILTER (WHERE day = CURRENT_DATE), 0),
			COALESCE(SUM(calls), 0),
			COALESCE(SUM(calls) FILTER (WHERE day = CURRENT_DATE AND status >= 400), 0)
		FROM api_request_daily WHERE day > CURRENT_DATE - 7`)
	if err := row.Scan(&out.CallsToday, &out.CallsWeek, &out.ErrorsToday); err != nil {
		log.Printf("api analytics (totals): %v", err)
		return APIAnalyticsResponse{}, err
	}
	_ = database.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT ip) FROM api_ip_daily WHERE day = CURRENT_DATE`).Scan(&out.IPsToday)
	_ = database.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(calls), 0), COALESCE(SUM(calls) FILTER (WHERE status >= 400), 0)
		FROM api_self_daily WHERE day = CURRENT_DATE`).Scan(&out.SelfToday, &out.SelfErrorsToday)

	rows, err := database.QueryContext(ctx, `
		SELECT route,
			COALESCE(SUM(calls) FILTER (WHERE day = CURRENT_DATE), 0) AS today,
			SUM(calls) AS week,
			COALESCE(SUM(calls) FILTER (WHERE status >= 400), 0) AS errs
		FROM api_request_daily WHERE day > CURRENT_DATE - 7
		GROUP BY route ORDER BY week DESC LIMIT 20`)
	if err == nil {
		for rows.Next() {
			var rt APIAnalyticsRoute
			if rows.Scan(&rt.Route, &rt.Today, &rt.Week, &rt.Errors) == nil {
				out.Routes = append(out.Routes, rt)
			}
		}
		rows.Close()
	}

	rows, err = database.QueryContext(ctx, `
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
			var ip APIAnalyticsIP
			var lastSeen time.Time
			if rows.Scan(&ip.IP, &ip.Today, &ip.Week, &lastSeen, &ip.LastPath) == nil {
				ip.LastSeen = lastSeen.UTC().Format(time.RFC3339)
				out.IPs = append(out.IPs, ip)
			}
		}
		rows.Close()
	}

	return out, nil
}

func FlushAPIIP(ctx context.Context, q interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, ip string, calls int64, lastRoute string) (sql.Result, error) {
	return q.ExecContext(ctx, `
			INSERT INTO api_ip_daily (day, ip, calls, last_route, last_seen)
			VALUES (CURRENT_DATE, $1, $2, $3, now())
			ON CONFLICT (day, ip) DO UPDATE SET
				calls = api_ip_daily.calls + EXCLUDED.calls,
				last_route = EXCLUDED.last_route,
				last_seen = now()`, ip, calls, lastRoute)
}

func FlushAPISelf(ctx context.Context, q interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, route string, status int, calls int64) (sql.Result, error) {
	return q.ExecContext(ctx, `
			INSERT INTO api_self_daily (day, route, status, calls)
			VALUES (CURRENT_DATE, $1, $2, $3)
			ON CONFLICT (day, route, status) DO UPDATE SET calls = api_self_daily.calls + EXCLUDED.calls`, route, status, calls)
}

func FlushAPIRoute(ctx context.Context, q interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, route string, status int, calls int64) (sql.Result, error) {
	return q.ExecContext(ctx, `
			INSERT INTO api_request_daily (day, route, status, calls)
			VALUES (CURRENT_DATE, $1, $2, $3)
			ON CONFLICT (day, route, status) DO UPDATE SET calls = api_request_daily.calls + EXCLUDED.calls`, route, status, calls)
}

func PruneAPI(ctx context.Context, q interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, kind string, days int) (sql.Result, error) {
	queries := map[string]string{"requests": `DELETE FROM api_request_daily WHERE day < CURRENT_DATE - $1::int`, "ips": `DELETE FROM api_ip_daily WHERE day < CURRENT_DATE - $1::int`, "self": `DELETE FROM api_self_daily WHERE day < CURRENT_DATE - $1::int`, "growth": `DELETE FROM api_growth_daily WHERE day < CURRENT_DATE - $1::int`}
	return q.ExecContext(ctx, queries[kind], days)
}

func FlushGrowth(ctx context.Context, q interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, day string, dims, keys []string, calls []int64) error {
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

type APICount struct {
	Day, Key string
	Calls    int64
}
type GrowthCount struct {
	Day, Dim, Key string
	Calls         int64
}

func apiCounts(rows *sql.Rows, err error) ([]APICount, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []APICount{}
	for rows.Next() {
		var c APICount
		if err := rows.Scan(&c.Day, &c.Key, &c.Calls); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func APIRequestCounts(ctx context.Context, db *sql.DB) ([]APICount, error) {
	return apiCounts(db.QueryContext(ctx, `SELECT day::text, route, SUM(calls) FROM api_request_daily GROUP BY day, route`))
}

func APIIPCounts(ctx context.Context, db *sql.DB) ([]APICount, error) {
	return apiCounts(db.QueryContext(ctx, `SELECT day::text, ip, SUM(calls) FROM api_ip_daily GROUP BY day, ip`))
}

func ExistingGrowth(ctx context.Context, db *sql.DB) ([]GrowthCount, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT day::text, dim, key, calls FROM api_growth_daily
		WHERE day >= (SELECT LEAST(
			(SELECT MIN(day) FROM api_request_daily),
			(SELECT MIN(day) FROM api_ip_daily)))`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []GrowthCount{}
	for rows.Next() {
		var c GrowthCount
		if err := rows.Scan(&c.Day, &c.Dim, &c.Key, &c.Calls); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func ConnectorDays(ctx context.Context, db *sql.DB) (map[string]bool, error) {
	covered := map[string]bool{}
	rows, err := db.QueryContext(ctx, `SELECT DISTINCT day::text FROM api_growth_daily WHERE dim=$1 AND key='connector' AND calls>0`, "group")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var d string
		if rows.Scan(&d) == nil {
			covered[d] = true
		}
	}
	rows.Close()
	return covered, nil
}

func APIToday(ctx context.Context, db *sql.DB) (time.Time, error) {
	var t time.Time
	err := db.QueryRowContext(ctx, `SELECT CURRENT_DATE`).Scan(&t)
	return t, err
}
func GrowthSince(ctx context.Context, db *sql.DB) (sql.NullString, error) {
	var s sql.NullString
	err := db.QueryRowContext(ctx, `SELECT MIN(day)::text FROM api_growth_daily`).Scan(&s)
	return s, err
}
func GrowthGroups(ctx context.Context, db *sql.DB, from, to string) ([]APICount, error) {
	return apiCounts(db.QueryContext(ctx, `SELECT day::text,key,calls FROM api_growth_daily WHERE dim=$1 AND day BETWEEN $2::date AND $3::date`, "group", from, to))
}
func GrowthCountries(ctx context.Context, db *sql.DB, from, to string) ([]APICount, error) {
	return apiCounts(db.QueryContext(ctx, `SELECT '',key,SUM(calls) FROM api_growth_daily WHERE dim=$1 AND day BETWEEN $2::date AND $3::date GROUP BY key ORDER BY SUM(calls) DESC,key`, "country", from, to))
}
func GrowthSelf(ctx context.Context, db *sql.DB, from, to string) (int64, error) {
	var n int64
	err := db.QueryRowContext(ctx, `SELECT COALESCE(SUM(calls),0) FROM api_growth_daily WHERE dim=$1 AND key=$2 AND day BETWEEN $3::date AND $4::date`, "source", "self", from, to).Scan(&n)
	return n, err
}
func GrowthCreated(ctx context.Context, db *sql.DB, table, from, to string) ([]APICount, error) {
	filter := ""
	if table == "users" {
		filter = " AND EXISTS (SELECT 1 FROM sites s WHERE s.user_id=users.id)"
	}
	return apiCounts(db.QueryContext(ctx, `SELECT created_at::date::text,'',COUNT(*) FROM `+table+` WHERE created_at >= $1::date AND created_at < $2::date + 1`+filter+` GROUP BY 1`, from, to))
}
