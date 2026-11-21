package analytics

import (
	"context"
	"database/sql"
	"log"
	"sort"
	"time"

	"github.com/lib/pq"
	sitesrepo "github.com/vsriram/simple-host/host/sites/repo"
)

// ClassUnknown is never written by the ingester. It carries the pre-v2
// aggregates in site_view_daily, which were recorded before traffic was
// classified and cannot be re-derived: the raw log they came from has
// scrolled away. Those days are real traffic and belong on the chart, but
// splitting them into person/bot/infra now would be invention, so they are
// reported as their own series instead.
const ClassUnknown = "unknown"

// Counts is one class's traffic in one bucket.
type Counts struct {
	Views    int64 `json:"views"`
	Visitors int64 `json:"visitors"`
}

// Split is one bucket of traffic with the classes side by side.
type Split struct {
	Person Counts `json:"person"`
	Bot    Counts `json:"bot"`
	Infra  Counts `json:"infra"`
	// Owner is the owner's own visits (string(ClassOwner)); not people.
	Owner Counts `json:"owner"`
	// Unknown is non-zero only for days predating the classifier.
	Unknown Counts `json:"unknown"`
}

// add folds a (class, views, visitors) row into the right slot.
//
// An unrecognised class is logged rather than dropped. Silently discarding it
// would undercount by exactly the new class while every field stayed a valid
// number -- the server-side twin of the bug that made the dashboard render
// zeros, and just as invisible.
func (s *Split) add(class string, views, visitors int64) {
	switch class {
	case string(ClassPerson):
		s.Person.Views += views
		s.Person.Visitors += visitors
	case string(ClassBot):
		s.Bot.Views += views
		s.Bot.Visitors += visitors
	case string(ClassInfra):
		s.Infra.Views += views
		s.Infra.Visitors += visitors
	case string(ClassOwner):
		s.Owner.Views += views
		s.Owner.Visitors += visitors
	case ClassUnknown:
		s.Unknown.Views += views
		s.Unknown.Visitors += visitors
	default:
		log.Printf("analytics: unknown traffic class %q dropped (%d views, %d visitors) — add it to Split", class, views, visitors)
	}
}

// DayStat is one UTC calendar day, split by class.
type DayStat struct {
	Day string `json:"day"` // YYYY-MM-DD (UTC)
	Split
}

// HourStat is one UTC hour, split by class.
type HourStat struct {
	Hour string `json:"hour"` // RFC3339 UTC, on the hour
	Split
}

// SiteAnalytics is everything the dashboard needs for one site in one call.
type SiteAnalytics struct {
	RangeDays int   `json:"range_days"`
	Totals    Split `json:"totals"`
	// ClassifiedFrom is the first UTC day (YYYY-MM-DD) for which the
	// person/bot/infra split exists. Days before it report under `unknown`.
	// Empty when there is no classified data at all.
	ClassifiedFrom string     `json:"classified_from,omitempty"`
	Daily          []DayStat  `json:"daily"`
	Last24h        Split      `json:"last_24h"`
	Hourly         []HourStat `json:"hourly"`
}

// GetSiteAnalytics returns, for one site: class-split totals and a zero-filled
// dense daily series over the last `days` UTC days (inclusive of today), plus a
// zero-filled 24-bucket hourly series covering the last 24 hours.
//
// Visitor counts are COUNT(DISTINCT ip_hash) over whatever window is being
// asked for. The ingest salt is stable, so a hash identifies the same visitor
// across days: `totals.*.visitors` is genuine unique visitors for the range and
// is NOT the sum of the daily figures (a person who came on five days is one
// unique here, five in `daily`).
//
// The usual IP caveats apply and always will: a shared office NAT reads as one
// visitor, and a phone moving between wifi and cellular reads as two.
func GetSiteAnalytics(ctx context.Context, database *sql.DB, siteID string, days int) (*SiteAnalytics, error) {
	if days < 1 {
		days = 1
	}
	if days > 365 {
		days = 365
	}

	now := time.Now().UTC()
	today := now.Truncate(24 * time.Hour)
	start := today.AddDate(0, 0, -(days - 1))
	end := today.AddDate(0, 0, 1) // exclusive

	out := &SiteAnalytics{RangeDays: days}

	winStart, winEnd := start.Format(time.RFC3339), end.Format(time.RFC3339)

	// ── Daily views and visitors, by class ─────────────────────────────────
	byDay := buckets{}
	if err := eachSplitRow(ctx, database, viewsBy(dayBucket, "site_id = $1"),
		[]any{siteID, winStart, winEnd},
		func(day, class string, n int64) {
			byDay.get(dayKey(day)).add(class, n, 0)
			out.Totals.add(class, n, 0)
		}); err != nil {
		return nil, err
	}
	if err := eachSplitRow(ctx, database, visitorsBy(dayBucket, "site_id = $1"),
		[]any{siteID, winStart, winEnd},
		func(day, class string, n int64) {
			byDay.get(dayKey(day)).add(class, 0, n)
		}); err != nil {
		return nil, err
	}

	rangeVisitors, err := visitorsByClass(ctx, database, siteID, start, end)
	if err != nil {
		return nil, err
	}
	for class, n := range rangeVisitors {
		out.Totals.add(class, 0, n)
	}

	// ── Pre-classifier history ─────────────────────────────────────────────
	// Everything before the classifier went live only exists as unclassified
	// daily totals. Fold those days in so the trend line keeps its full length
	// instead of starting abruptly at the cutover.
	cutover, err := classifierCutover(ctx, database)
	if err != nil {
		return nil, err
	}
	out.ClassifiedFrom = cutover
	if cutover != "" && cutover > start.Format("2006-01-02") {
		legacy, err := legacyDaily(ctx, database, siteID, startStr(start), cutover)
		if err != nil {
			return nil, err
		}
		for day, c := range legacy {
			byDay.get(day).add(ClassUnknown, c.Views, c.Visitors)
			out.Totals.add(ClassUnknown, c.Views, c.Visitors)
		}
	}

	out.Daily = make([]DayStat, 0, days)
	for d := start; !d.After(today); d = d.AddDate(0, 0, 1) {
		key := d.Format("2006-01-02")
		stat := DayStat{Day: key}
		if s := byDay[key]; s != nil {
			stat.Split = *s
		}
		out.Daily = append(out.Daily, stat)
	}

	// ── Last 24 hours, hour by hour ────────────────────────────────────────
	// 24 buckets ending with the hour in progress.
	hourEnd := now.Truncate(time.Hour)
	hourStart := hourEnd.Add(-23 * time.Hour)
	hourEndExcl := hourEnd.Add(time.Hour)

	hourFrom, hourTo := hourStart.Format(time.RFC3339), hourEndExcl.Format(time.RFC3339)

	byHour := buckets{}
	if err := eachSplitRow(ctx, database, viewsBy(hourBucket, "site_id = $1"),
		[]any{siteID, hourFrom, hourTo},
		func(hour, class string, n int64) {
			byHour.get(hour).add(class, n, 0)
			out.Last24h.add(class, n, 0)
		}); err != nil {
		return nil, err
	}
	if err := eachSplitRow(ctx, database, visitorsBy(hourBucket, "site_id = $1"),
		[]any{siteID, hourFrom, hourTo},
		func(hour, class string, n int64) {
			byHour.get(hour).add(class, 0, n)
		}); err != nil {
		return nil, err
	}

	last24Visitors, err := visitorsByClass(ctx, database, siteID, hourStart, hourEndExcl)
	if err != nil {
		return nil, err
	}
	for class, n := range last24Visitors {
		out.Last24h.add(class, 0, n)
	}

	out.Hourly = make([]HourStat, 0, 24)
	for h := hourStart; !h.After(hourEnd); h = h.Add(time.Hour) {
		key := h.Format("2006-01-02T15:00:00Z")
		stat := HourStat{Hour: key}
		if s := byHour[key]; s != nil {
			stat.Split = *s
		}
		out.Hourly = append(out.Hourly, stat)
	}

	return out, nil
}

func startStr(t time.Time) string { return t.Format("2006-01-02") }

// Every analytics read buckets rows by time, groups by class, and counts.
const (
	// dayBucket collapses each row to its UTC calendar day.
	dayBucket = `(hour AT TIME ZONE 'UTC')::date::text`
	// hourBucket renders the exact RFC3339 UTC hour the client keys off.
	hourBucket = `to_char(hour AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:00:00"Z"')`
)

// The window is always $2/$3; scope owns $1 and is always a package constant.
func viewsBy(bucket, scope string) string {
	return `SELECT ` + bucket + `, class, SUM(views)
		FROM site_view_hourly
		WHERE ` + scope + ` AND hour >= $2::timestamptz AND hour < $3::timestamptz
		GROUP BY 1, 2`
}

func visitorsBy(bucket, scope string) string {
	return `SELECT ` + bucket + `, class, COUNT(DISTINCT ip_hash)
		FROM site_visitor_hourly
		WHERE ` + scope + ` AND hour >= $2::timestamptz AND hour < $3::timestamptz
		GROUP BY 1, 2`
}

// eachSplitRow runs a (bucket, class, count) query and hands each row to fn,
// replacing the query/defer-close/scan/err-check/close dance at every call site.
func eachSplitRow(ctx context.Context, database *sql.DB, query string, args []any, fn func(bucket, class string, n int64)) error {
	rows, err := database.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var bucket, class string
		var n int64
		if err := rows.Scan(&bucket, &class, &n); err != nil {
			return err
		}
		fn(bucket, class, n)
	}
	return rows.Err()
}

// buckets is a keyed set of Splits that materialises entries on first touch, so
// callers stop repeating the nil check before every add.
type buckets map[string]*Split

func (b buckets) get(key string) *Split {
	if b[key] == nil {
		b[key] = &Split{}
	}
	return b[key]
}

// dayKey normalises a driver's date rendering ("2006-01-02" or a full timestamp)
// to a bare YYYY-MM-DD.
func dayKey(s string) string {
	if len(s) >= 10 {
		return s[:10]
	}
	return s
}

// CountryStat is one country's traffic over a range, split by class.
type CountryStat struct {
	Country string `json:"country"` // ISO-3166 alpha-2; "XX" = unresolved
	Split
}

// GetSiteGeo returns per-country traffic for one site over the last `days` UTC
// days, ordered by person views descending then country code ascending.
//
// Views come from site_geo_daily. Visitors are COUNT(DISTINCT ip_hash) from
// site_visitor_hourly over the whole range, which is what the rest of analytics
// counts: a country's `visitors` is genuine uniques for the range, not the sum
// of the per-day figures. site_geo_daily.visitors holds the daily uniques for
// anyone reading the table directly — summing that column instead would
// double-count every returning visitor.
//
// A visitor whose address changes country mid-range counts once in each, so the
// country columns can add to slightly more than the site total.
func GetSiteGeo(ctx context.Context, database *sql.DB, siteID string, days int) ([]CountryStat, error) {
	if days < 1 {
		days = 1
	}
	if days > 365 {
		days = 365
	}

	today := time.Now().UTC().Truncate(24 * time.Hour)
	start := today.AddDate(0, 0, -(days - 1))
	end := today.AddDate(0, 0, 1)

	byCountry := buckets{}

	if err := eachSplitRow(ctx, database, `
		SELECT country, class, SUM(views)
		FROM site_geo_daily
		WHERE site_id = $1 AND day >= $2::date AND day < $3::date
		GROUP BY 1, 2
	`, []any{siteID, startStr(start), startStr(end)},
		func(country, class string, n int64) {
			byCountry.get(country).add(class, n, 0)
		}); err != nil {
		return nil, err
	}

	if err := eachSplitRow(ctx, database, `
		SELECT country, class, COUNT(DISTINCT ip_hash)
		FROM site_visitor_hourly
		WHERE site_id = $1 AND hour >= $2::timestamptz AND hour < $3::timestamptz
		GROUP BY 1, 2
	`, []any{siteID, start.Format(time.RFC3339), end.Format(time.RFC3339)},
		func(country, class string, n int64) {
			byCountry.get(country).add(class, 0, n)
		}); err != nil {
		return nil, err
	}

	out := make([]CountryStat, 0, len(byCountry))
	for country, s := range byCountry {
		out = append(out, CountryStat{Country: country, Split: *s})
	}
	sort.Slice(out, func(a, b int) bool {
		if out[a].Person.Views != out[b].Person.Views {
			return out[a].Person.Views > out[b].Person.Views
		}
		return out[a].Country < out[b].Country
	})
	return out, nil
}

// SiteAnalyticsSummary is one site's totals, for ordering a list of sites by traffic.
type SiteAnalyticsSummary struct {
	Name string `json:"name"`
	Split
}

// GetAnalyticsSummary returns per-site totals over the last `days` UTC days for
// every site owned by userID (or every site on the instance when allSites is
// set, for the admin view).
//
// This exists so the dashboard can sort by traffic: sorting needs every site's
// numbers before the first card renders, which the per-site endpoint cannot do
// without one request per site.
func GetAnalyticsSummary(ctx context.Context, database *sql.DB, userID string, days int, allSites bool) ([]SiteAnalyticsSummary, error) {
	if days < 1 {
		days = 1
	}
	if days > 365 {
		days = 365
	}

	today := time.Now().UTC().Truncate(24 * time.Hour)
	start := today.AddDate(0, 0, -(days - 1))
	end := today.AddDate(0, 0, 1)

	// Owner scoping is in SQL, not a filter applied afterwards: the account's
	// site ids are a bind parameter, so each read is one query.
	owned, err := accountSiteIDs(ctx, database, userID, allSites)
	if err != nil {
		return nil, err
	}
	args := []any{pq.Array(owned), start.Format(time.RFC3339), end.Format(time.RFC3339), allSites}
	const scope = `($4 OR v.site_id = ANY($1::uuid[]))`

	// Rows are read per site id, then folded by the site's name.
	type siteRow struct {
		id, class       string
		views, visitors int64
	}
	var got []siteRow

	if err := eachSplitRow(ctx, database, `
		SELECT v.site_id::text, v.class, SUM(v.views)
		FROM site_view_hourly v
		WHERE `+scope+` AND v.hour >= $2::timestamptz AND v.hour < $3::timestamptz
		GROUP BY v.site_id, v.class
	`, args, func(id, class string, n int64) {
		got = append(got, siteRow{id, class, n, 0})
	}); err != nil {
		return nil, err
	}

	// Unique visitors are counted once per site name across same-named sites
	// (a deleted twin, or other accounts in the operator's view), so this one
	// count keeps its join (named exception, table-owner-exceptions.txt).
	byName := buckets{}
	if err := eachSplitRow(ctx, database, `
		SELECT s.name, v.class, COUNT(DISTINCT v.ip_hash)
		FROM site_visitor_hourly v JOIN sites s ON s.id = v.site_id
		WHERE ($4 OR s.user_id = $1) AND v.hour >= $2::timestamptz AND v.hour < $3::timestamptz
		GROUP BY s.name, v.class
	`, []any{userID, start.Format(time.RFC3339), end.Format(time.RFC3339), allSites}, func(name, class string, n int64) {
		byName.get(name).add(class, 0, n)
	}); err != nil {
		return nil, err
	}

	cutover, err := classifierCutover(ctx, database)
	if err != nil {
		return nil, err
	}
	if cutover != "" && cutover > startStr(start) {
		if err := eachSplitRow(ctx, database, `
			SELECT v.site_id::text, '`+ClassUnknown+`', SUM(v.views)
			FROM site_view_daily v
			WHERE `+scope+` AND v.day >= $2::date AND v.day < $3::date
			GROUP BY v.site_id
		`, []any{pq.Array(owned), startStr(start), cutover, allSites},
			func(id, class string, n int64) {
				got = append(got, siteRow{id, class, n, 0})
			}); err != nil {
			return nil, err
		}
	}

	ids := make([]string, 0, len(got))
	for _, r := range got {
		ids = append(ids, r.id)
	}
	sites, err := sitesrepo.SitesByIDs(ctx, database, ids)
	if err != nil {
		return nil, err
	}
	for _, r := range got {
		if site, ok := sites[r.id]; ok {
			byName.get(site.Name).add(r.class, r.views, r.visitors)
		}
	}

	out := make([]SiteAnalyticsSummary, 0, len(byName))
	for name, s := range byName {
		out = append(out, SiteAnalyticsSummary{Name: name, Split: *s})
	}
	// Descending by real people: "who actually got read".
	sort.Slice(out, func(a, b int) bool {
		if out[a].Person.Views != out[b].Person.Views {
			return out[a].Person.Views > out[b].Person.Views
		}
		return out[a].Name < out[b].Name
	})
	return out, nil
}

// classifierCutover is the first UTC day covered by the classified tables.
// Returns "" when nothing has been classified yet (fresh install, or an
// instance that has never run the ingester).
func classifierCutover(ctx context.Context, database *sql.DB) (string, error) {
	var day sql.NullString
	err := database.QueryRowContext(ctx,
		`SELECT MIN(hour AT TIME ZONE 'UTC')::date::text FROM site_view_hourly`).Scan(&day)
	if err != nil {
		return "", err
	}
	if !day.Valid || len(day.String) < 10 {
		return "", nil
	}
	return day.String[:10], nil
}

// legacyDaily reads the pre-v2 aggregates for [from, to) -- `to` being the day
// the classifier took over, so the two sources never overlap and no day is
// counted twice.
func legacyDaily(ctx context.Context, database *sql.DB, siteID, from, to string) (map[string]Counts, error) {
	out := map[string]Counts{}
	if err := addLegacyDays(ctx, database, out, func(c *Counts, n int64) { c.Views = n }, `
		SELECT day::text, views
		FROM site_view_daily
		WHERE site_id = $1 AND day >= $2::date AND day < $3::date
	`, siteID, from, to); err != nil {
		return nil, err
	}
	if err := addLegacyDays(ctx, database, out, func(c *Counts, n int64) { c.Visitors = n }, `
		SELECT day::text, COUNT(*)
		FROM site_visitor_daily
		WHERE site_id = $1 AND day >= $2::date AND day < $3::date
		GROUP BY day
	`, siteID, from, to); err != nil {
		return nil, err
	}
	return out, nil
}

// addLegacyDays runs a (day, count) query and sets each day's count in out.
func addLegacyDays(ctx context.Context, database *sql.DB, out map[string]Counts, set func(*Counts, int64), query string, args ...any) error {
	rows, err := database.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var day string
		var n int64
		if err := rows.Scan(&day, &n); err != nil {
			return err
		}
		if len(day) >= 10 {
			day = day[:10]
		}
		c := out[day]
		set(&c, n)
		out[day] = c
	}
	return rows.Err()
}

// visitorsByClass counts distinct ip_hash per class over an arbitrary window.
func visitorsByClass(ctx context.Context, database *sql.DB, siteID string, from, to time.Time) (map[string]int64, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT class, COUNT(DISTINCT ip_hash)
		FROM site_visitor_hourly
		WHERE site_id = $1 AND hour >= $2::timestamptz AND hour < $3::timestamptz
		GROUP BY class
	`, siteID, from.Format(time.RFC3339), to.Format(time.RFC3339))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]int64{}
	for rows.Next() {
		var class string
		var n int64
		if err := rows.Scan(&class, &n); err != nil {
			return nil, err
		}
		out[class] = n
	}
	return out, rows.Err()
}

// PageStat is one page's views by people over a range.
type PageStat struct {
	Path  string `json:"path"`
	Views int64  `json:"views"`
}

// ReferrerStat is one referring domain's views by people over a range.
type ReferrerStat struct {
	Domain string `json:"domain"`
	Views  int64  `json:"views"`
}

// TopListLimit is how many pages and referring domains GetSiteTop returns.
const TopListLimit = 20

// GetSiteTop returns one site's most viewed pages and top referring domains
// over the last `days` UTC days, people only, most views first.
func GetSiteTop(ctx context.Context, database *sql.DB, siteID string, days int) ([]PageStat, []ReferrerStat, error) {
	days = max(1, min(days, 365))
	today := time.Now().UTC().Truncate(24 * time.Hour)
	start, end := startStr(today.AddDate(0, 0, -(days-1))), startStr(today.AddDate(0, 0, 1))

	pages := []PageStat{}
	rows, err := database.QueryContext(ctx, `
		SELECT path, SUM(views) FROM site_page_daily
		 WHERE site_id = $1 AND day >= $2::date AND day < $3::date
		 GROUP BY path ORDER BY 2 DESC, 1 LIMIT $4`, siteID, start, end, TopListLimit)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var p PageStat
		if err := rows.Scan(&p.Path, &p.Views); err != nil {
			rows.Close()
			return nil, nil, err
		}
		pages = append(pages, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	refs := []ReferrerStat{}
	rows, err = database.QueryContext(ctx, `
		SELECT domain, SUM(views) FROM site_referrer_daily
		 WHERE site_id = $1 AND day >= $2::date AND day < $3::date
		 GROUP BY domain ORDER BY 2 DESC, 1 LIMIT $4`, siteID, start, end, TopListLimit)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var r ReferrerStat
		if err := rows.Scan(&r.Domain, &r.Views); err != nil {
			return nil, nil, err
		}
		refs = append(refs, r)
	}
	return pages, refs, rows.Err()
}

// AdminSiteViews counts people, using the same classified hourly analytics as
// the site dashboard. Bots and infrastructure requests are excluded.
func AdminSiteViews(ctx context.Context, database *sql.DB, since time.Time) (map[string]int64, error) {
	rows, err := database.QueryContext(ctx, `SELECT site_id, SUM(views) FROM site_view_hourly WHERE class = 'person' AND hour >= $1 GROUP BY site_id`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var id string
		var n int64
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

// SiteViews is the light per-site count the site list carries: views by
// people (class 'person') only, over the last 7 x 24 and 30 x 24 hours. The
// owner's own visits (string(ClassOwner)), bots and infrastructure are excluded.
// Buckets are hourly, so each window starts at the top of its oldest hour.
type SiteViews struct {
	People7Days  int64 `json:"people_7_days"`
	People30Days int64 `json:"people_30_days"`
}

// PersonViewsBySite returns SiteViews keyed by site_id for every site owned
// by userID (or every site when allSites is set; userID may then be ""), in
// one grouped query over site_view_hourly. People only: class 'person';
// 'owner' is excluded. Sites with no people views in the last 30 days have
// no entry.
func PersonViewsBySite(ctx context.Context, database *sql.DB, userID string, allSites bool) (map[string]SiteViews, error) {
	owned, err := accountSiteIDs(ctx, database, userID, allSites)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	rows, err := database.QueryContext(ctx, `
		SELECT v.site_id,
		       COALESCE(SUM(v.views) FILTER (WHERE v.hour >= $2), 0),
		       SUM(v.views)
		FROM site_view_hourly v
		WHERE ($4 OR v.site_id = ANY($1::uuid[])) AND v.class = 'person' AND v.hour >= $3
		GROUP BY v.site_id
	`, pq.Array(owned), now.AddDate(0, 0, -7).Truncate(time.Hour), now.AddDate(0, 0, -30).Truncate(time.Hour), allSites)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]SiteViews{}
	for rows.Next() {
		var id string
		var v SiteViews
		if err := rows.Scan(&id, &v.People7Days, &v.People30Days); err != nil {
			return nil, err
		}
		out[id] = v
	}
	return out, rows.Err()
}

// accountSiteIDs is the account's sites (live and deleted) for scoping a
// report; nil when the report covers every site or names no account.
func accountSiteIDs(ctx context.Context, database *sql.DB, userID string, allSites bool) ([]string, error) {
	if allSites || userID == "" {
		return nil, nil
	}
	return sitesrepo.AccountSiteIDs(ctx, database, userID)
}
