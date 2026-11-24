// Package admin is the operator's page data (route 65): people, sites,
// sizes, new sign-ups, visitor reports, network use and the API growth tab;
// and, on a self-hosted install, making an account for someone. It counts
// API calls and network bytes as requests pass (Counted), written once a
// minute to api_daily and net_usage_daily.
package admin

import (
	"context"
	"database/sql"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"simplehost/app"
	"simplehost/core/web"
)

type handler struct{ a *app.App }

// Mount adds route 65 and the self-hosted "make an account".
func Mount(a *app.App) {
	h := &handler{a}
	a.Routes.Platform.Handle("GET /v1/admin/overview", web.Func(h.overview))
	a.Routes.Platform.Handle("POST /v1/admin/people", web.Func(h.makeAccount))
}

var errOperator = web.Refuse(http.StatusForbidden, "operator_only", "Only the operator can see this.")

// makeAccount makes a person by email on a self-hosted install. They sign
// in with a code sent to that email and pick a handle.
func (h *handler) makeAccount(w http.ResponseWriter, r *http.Request) error {
	if !web.CallerOf(r).Operator {
		return errOperator
	}
	if !h.a.Config.SelfHosted {
		return web.Refuse(http.StatusForbidden, "self_hosted_only", "Accounts are made by signing up on the hosted service.")
	}
	var in struct{ Email string }
	if err := web.Read(r, &in); err != nil {
		return err
	}
	p, err := h.a.People.Ensure(r.Context(), in.Email)
	if err != nil {
		return err
	}
	web.JSON(w, http.StatusOK, map[string]string{"id": p.ID, "email": p.Email,
		"says": "Made. They sign in at https://" + h.a.Config.AppDomain + "/signin with a code sent to this email."})
	return nil
}

// rows runs a query and returns each row as a map of column -> value.
func rows(ctx context.Context, db *sql.DB, q string, args ...any) ([]map[string]any, error) {
	rs, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	cols, _ := rs.Columns()
	out := []map[string]any{}
	for rs.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rs.Scan(ptrs...); err != nil {
			return nil, err
		}
		m := map[string]any{}
		for i, c := range cols {
			if b, ok := vals[i].([]byte); ok {
				vals[i] = string(b)
			}
			m[c] = vals[i]
		}
		out = append(out, m)
	}
	return out, rs.Err()
}

// liveBytes is each live site's published size.
const liveBytes = `(SELECT s.id, s.owner_id, s.name, COALESCE(v.bytes, 0) AS bytes FROM sites s
	LEFT JOIN versions v ON v.site_id = s.id AND v.n = s.live_version WHERE s.deleted_at IS NULL) live`

// overview is route 65. ?range= (7d, 14d, 30d, 6m) sets the growth tab's span.
func (h *handler) overview(w http.ResponseWriter, r *http.Request) error {
	if !web.CallerOf(r).Operator {
		return errOperator
	}
	ctx := r.Context()
	out := map[string]any{"self_hosted": h.a.Config.SelfHosted}
	queries := []struct{ key, q string }{
		{"people", `SELECT count(*) AS total, count(*) FILTER (WHERE created_at > now() - interval '7 days') AS new_7d,
			count(*) FILTER (WHERE created_at > now() - interval '30 days') AS new_30d FROM people WHERE NOT is_team`},
		{"sites", `SELECT count(*) AS total, COALESCE(sum(bytes), 0)::bigint AS bytes FROM ` + liveBytes},
		{"signups", `SELECT to_char(created_at, 'YYYY-MM-DD') AS day, count(*) AS count FROM people
			WHERE NOT is_team AND created_at > now() - interval '30 days' GROUP BY 1 ORDER BY 1`},
		{"people_list", `SELECT p.id, p.email, COALESCE(n.name, '') AS handle, p.created_at, count(live.id) AS sites,
			COALESCE(sum(live.bytes), 0)::bigint AS bytes, p.suspended_at IS NOT NULL AS suspended
			FROM people p LEFT JOIN names n ON n.person_id = p.id AND n.kind = 'handle' AND n.retired_at IS NULL
			LEFT JOIN ` + liveBytes + ` ON live.owner_id = p.id WHERE NOT p.is_team GROUP BY p.id, n.name ORDER BY p.created_at DESC LIMIT 200`},
		{"largest_sites", `SELECT COALESCE(n.name, '') AS owner, live.name, live.bytes FROM ` + liveBytes + `
			LEFT JOIN names n ON n.person_id = live.owner_id AND n.kind = 'handle' AND n.retired_at IS NULL ORDER BY live.bytes DESC LIMIT 20`},
		{"reports", `SELECT r.id, r.url, r.reason, r.created_at, COALESCE(n.name, '') AS owner, COALESCE(s.name, '') AS site FROM reports r
			LEFT JOIN sites s ON s.id = r.site_id LEFT JOIN names n ON n.person_id = s.owner_id AND n.kind = 'handle' AND n.retired_at IS NULL
			WHERE r.handled_at IS NULL ORDER BY r.id DESC LIMIT 100`},
		{"network", `SELECT to_char(day, 'YYYY-MM-DD') AS day, bytes_in, bytes_out FROM net_usage_daily WHERE day > current_date - 30 ORDER BY day`},
	}
	for _, q := range queries {
		list, err := rows(ctx, h.a.DB, q.q)
		if err != nil {
			return err
		}
		if q.key == "people" || q.key == "sites" {
			out[q.key] = list[0]
		} else {
			out[q.key] = list
		}
	}
	for _, s := range out["largest_sites"].([]map[string]any) {
		if p, err := h.a.People.ByHandle(ctx, s["owner"].(string)); err == nil {
			if site, err := h.a.Sites.Get(ctx, p.ID, s["name"].(string)); err == nil {
				s["url"] = h.a.Sites.URL(site)
			}
		}
	}
	g, err := growth(ctx, h.a.DB, r.URL.Query().Get("range"))
	if err != nil {
		return err
	}
	out["growth"] = g
	web.JSON(w, http.StatusOK, out)
	return nil
}

// growth is the API growth tab: calls, new accounts and new sites per day
// over the range, against the period before, and calls by country.
func growth(ctx context.Context, db *sql.DB, span string) (map[string]any, error) {
	days := map[string]int{"7d": 7, "14d": 14, "30d": 30, "6m": 182}[span]
	if days == 0 {
		span, days = "30d", 30
	}
	to := time.Now().UTC().Truncate(24 * time.Hour)
	from := to.AddDate(0, 0, 1-days)
	dates := make([]string, days)
	for i := range dates {
		dates[i] = from.AddDate(0, 0, i).Format("2006-01-02")
	}
	series := func(q string) (map[string]any, error) {
		list, err := rows(ctx, db, `SELECT to_char(d, 'YYYY-MM-DD') AS day, COALESCE(n, 0)::bigint AS n FROM generate_series($1::date - $2::int, $1::date, '1 day') d
			LEFT JOIN (`+q+`) x ON x.day = d`, to, 2*days-1)
		if err != nil {
			return nil, err
		}
		daily, prev, total := []int64{}, int64(0), int64(0)
		for i, x := range list {
			if i < days {
				prev += x["n"].(int64)
			} else {
				total += x["n"].(int64)
				daily = append(daily, x["n"].(int64))
			}
		}
		s := map[string]any{"total": total, "previous": prev, "daily": daily, "change_pct": nil}
		if prev > 0 {
			s["change_pct"] = (total - prev) * 100 / prev
		}
		return s, nil
	}
	api, err := series(`SELECT day, sum(calls) AS n FROM api_daily GROUP BY day`)
	if err != nil {
		return nil, err
	}
	api["groups"] = map[string]any{"calls": api["daily"]}
	users, err := series(`SELECT created_at::date AS day, count(*) AS n FROM people WHERE NOT is_team GROUP BY 1`)
	if err != nil {
		return nil, err
	}
	sitesNew, err := series(`SELECT created_at::date AS day, count(*) AS n FROM sites GROUP BY 1`)
	if err != nil {
		return nil, err
	}
	countries, err := rows(ctx, db, `SELECT country AS code, country AS name, sum(calls)::bigint AS calls FROM api_daily
		WHERE day >= $1 AND country <> '' GROUP BY country ORDER BY 3 DESC`, from)
	if err != nil {
		return nil, err
	}
	var since sql.NullString
	var unknown int64
	db.QueryRowContext(ctx, `SELECT to_char(min(day), 'YYYY-MM-DD'), COALESCE((SELECT sum(calls) FROM api_daily WHERE day >= $1 AND country = ''), 0) FROM api_daily`, from).Scan(&since, &unknown)
	return map[string]any{"range": span, "days": days, "from": dates[0], "to": dates[days-1], "dates": dates,
		"tracking_since": since.String, "self_calls": 0, "group_order": []string{"calls"}, "api": api, "users": users,
		"sites": sitesNew, "countries": countries, "unknown_country_calls": unknown}, nil
}

// counts are the API calls (by country) and network bytes of today, kept in
// memory until Flush.
var counts = struct {
	sync.Mutex
	calls   map[[2]string]int // {day, country}
	in, out map[string]int64  // day
}{calls: map[[2]string]int{}, in: map[string]int64{}, out: map[string]int64{}}

type counter struct {
	http.ResponseWriter
	n int64
}

func (c *counter) Write(b []byte) (int, error) {
	n, err := c.ResponseWriter.Write(b)
	c.n += int64(n)
	return n, err
}

func (c *counter) Flush() {
	if f, ok := c.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Counted wraps the service: every request's bytes count as network use,
// and every API call (on the platform: /v1/ and /mcp) counts for the growth
// tab, by the country nginx names in X-Country (empty when unknown).
func Counted(a *app.App) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := &counter{ResponseWriter: w}
		a.ServeHTTP(c, r)
		day := time.Now().UTC().Format("2006-01-02")
		api := web.Host(r) == a.Config.AppDomain && (strings.HasPrefix(r.URL.Path, "/v1/") || r.URL.Path == "/mcp")
		counts.Lock()
		counts.in[day] += max(r.ContentLength, 0)
		counts.out[day] += c.n
		if api {
			counts.calls[[2]string{day, strings.ToUpper(r.Header.Get("X-Country"))}]++
		}
		counts.Unlock()
	})
}

// Work writes the counts every minute until ctx ends.
func Work(ctx context.Context, a *app.App) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			flush(context.Background(), a)
			return
		case <-t.C:
			flush(ctx, a)
		}
	}
}

func flush(ctx context.Context, a *app.App) {
	counts.Lock()
	calls, in, out := counts.calls, counts.in, counts.out
	counts.calls, counts.in, counts.out = map[[2]string]int{}, map[string]int64{}, map[string]int64{}
	counts.Unlock()
	for k, n := range calls {
		if _, err := a.DB.ExecContext(ctx, `INSERT INTO api_daily (day, country, calls) VALUES ($1, $2, $3)
			ON CONFLICT (day, country) DO UPDATE SET calls = api_daily.calls + $3`, k[0], k[1], n); err != nil {
			log.Printf("api counts: %v", err)
		}
	}
	for day := range in {
		if _, err := a.DB.ExecContext(ctx, `INSERT INTO net_usage_daily (day, bytes_in, bytes_out) VALUES ($1, $2, $3)
			ON CONFLICT (day) DO UPDATE SET bytes_in = net_usage_daily.bytes_in + $2, bytes_out = net_usage_daily.bytes_out + $3`, day, in[day], out[day]); err != nil {
			log.Printf("network counts: %v", err)
		}
	}
}
