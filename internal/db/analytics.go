package db

import (
	"context"
	"database/sql"
	"time"
)

// DayStat is one calendar day's view + visitor counts for a site.
type DayStat struct {
	Day      string // YYYY-MM-DD (UTC)
	Views    int64
	Visitors int64
}

// GetSiteAnalytics returns totals and a zero-filled dense daily series for the
// last `days` UTC days (inclusive of today), oldest → newest.
//
// totalVisitors is COUNT(DISTINCT ip_hash) over the range. Because the ingest
// salt rotates every UTC day, the same real-world visitor produces a different
// ip_hash on different days — so a returning-next-day visitor is counted once
// per day in the range total. Dashboard copy must not imply monthly uniques.
func GetSiteAnalytics(ctx context.Context, database *sql.DB, siteID string, days int) (totalViews, totalVisitors int64, daily []DayStat, err error) {
	if days < 1 {
		days = 1
	}
	if days > 365 {
		days = 365
	}

	today := time.Now().UTC().Truncate(24 * time.Hour)
	// Inclusive window: [today-(days-1), today]
	start := today.AddDate(0, 0, -(days - 1))
	// Exclusive end for SQL convenience: tomorrow.
	end := today.AddDate(0, 0, 1)

	startStr := start.Format("2006-01-02")
	endStr := end.Format("2006-01-02")

	// Daily views.
	viewRows, err := database.QueryContext(ctx, `
		SELECT day::text, views
		FROM site_view_daily
		WHERE site_id = $1 AND day >= $2::date AND day < $3::date
	`, siteID, startStr, endStr)
	if err != nil {
		return 0, 0, nil, err
	}
	viewsByDay := map[string]int64{}
	for viewRows.Next() {
		var day string
		var n int64
		if err := viewRows.Scan(&day, &n); err != nil {
			viewRows.Close()
			return 0, 0, nil, err
		}
		// day::text may be "2006-01-02" or "2006-01-02T00:00:00Z" depending on driver;
		// keep the date prefix.
		if len(day) >= 10 {
			day = day[:10]
		}
		viewsByDay[day] = n
		totalViews += n
	}
	if err := viewRows.Err(); err != nil {
		viewRows.Close()
		return 0, 0, nil, err
	}
	viewRows.Close()

	// Daily distinct visitors (count of ip_hash rows per day).
	visRows, err := database.QueryContext(ctx, `
		SELECT day::text, COUNT(*)
		FROM site_visitor_daily
		WHERE site_id = $1 AND day >= $2::date AND day < $3::date
		GROUP BY day
	`, siteID, startStr, endStr)
	if err != nil {
		return 0, 0, nil, err
	}
	visByDay := map[string]int64{}
	for visRows.Next() {
		var day string
		var n int64
		if err := visRows.Scan(&day, &n); err != nil {
			visRows.Close()
			return 0, 0, nil, err
		}
		if len(day) >= 10 {
			day = day[:10]
		}
		visByDay[day] = n
	}
	if err := visRows.Err(); err != nil {
		visRows.Close()
		return 0, 0, nil, err
	}
	visRows.Close()

	// Range total: distinct ip_hash over the window.
	// Note: salt rotates per UTC day, so cross-day uniqueness is per-day only
	// (a returning-next-day visitor contributes once per day to this count).
	err = database.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT ip_hash)
		FROM site_visitor_daily
		WHERE site_id = $1 AND day >= $2::date AND day < $3::date
	`, siteID, startStr, endStr).Scan(&totalVisitors)
	if err != nil {
		return 0, 0, nil, err
	}

	// Zero-filled dense series, oldest → newest.
	daily = make([]DayStat, 0, days)
	for d := start; !d.After(today); d = d.AddDate(0, 0, 1) {
		key := d.Format("2006-01-02")
		daily = append(daily, DayStat{
			Day:      key,
			Views:    viewsByDay[key],
			Visitors: visByDay[key],
		})
	}
	return totalViews, totalVisitors, daily, nil
}
