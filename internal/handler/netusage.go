package handler

import (
	"context"
	"database/sql"
	"log"
	"os"
	"time"

	"github.com/vsriram/simple-host/internal/config"
	"github.com/vsriram/simple-host/internal/netusage"
)

// Network use on the admin page's Overview: what the web server sent for
// sites (the analytics log's bytes, per UTC day, from traffic_daily and
// site_traffic_daily) and the box's own bytes in and out this month
// (internal/netusage), against NETWORK_MONTHLY_ALLOWANCE_GB.

// trafficTopSites is how many sites the "most traffic" table names.
const trafficTopSites = 10

// SetNetworkUsage turns on the box's network counters. iface is
// NETWORK_INTERFACE. Inside a container the default route is the container's
// own interface, not the box's, so there it stays off unless iface is set.
func (h *SiteHandler) SetNetworkUsage(iface string) {
	if iface == "" && inContainer() {
		log.Printf("network usage: off (in a container; set NETWORK_INTERFACE to the host's interface to count it)")
		return
	}
	h.netUsage = netusage.New(h.database, iface)
}

// SetTrafficLog records whether the analytics log (and so traffic figures)
// is being read.
func (h *SiteHandler) SetTrafficLog(on bool) { h.trafficOn = on }

func inContainer() bool {
	_, err := os.Stat("/.dockerenv")
	return err == nil
}

// StartNetworkUsage samples the counters every NETWORK_SAMPLE_MINUTES.
func (h *SiteHandler) StartNetworkUsage(ctx context.Context) {
	if h.netUsage == nil {
		return
	}
	every := config.Active().NetworkSample
	iface, err := h.netUsage.Interface()
	if err != nil {
		log.Printf("network usage: %v", err)
		return
	}
	log.Printf("network usage: counting %s every %s", iface, every)
	h.netUsage.Start(ctx, every)
}

type trafficFigure struct {
	Bytes    int64 `json:"bytes"`
	Requests int64 `json:"requests"`
}

type trafficSite struct {
	SiteID     string `json:"site_id"`
	UserID     string `json:"user_id"`
	Name       string `json:"name"`
	Owner      string `json:"owner"`
	Bytes      int64  `json:"bytes"`
	PagesBytes int64  `json:"pages_bytes"`
	APIBytes   int64  `json:"api_bytes"`
	Requests   int64  `json:"requests"`
}

// networkUsage is the "network" object of GET /v1/admin/usage. Each half is
// null when it is off or could not be read, and the rest still answers.
func (h *SiteHandler) networkUsage(ctx context.Context, now time.Time) map[string]any {
	lim := config.Active()
	out := map[string]any{
		"allowance_gb": lim.NetworkAllowanceGB,
		"alert_pct":    lim.NetworkAlertPct,
		"box":          nil,
		"served":       nil,
	}
	if h.netUsage != nil {
		if m, err := h.netUsage.ThisMonth(ctx); err != nil {
			log.Printf("admin usage: network counters: %v", err)
		} else {
			out["box"] = m
		}
	}
	if h.trafficOn {
		if s, err := h.servedTraffic(ctx, now); err != nil {
			log.Printf("admin usage: traffic: %v", err)
		} else {
			out["served"] = s
		}
	}
	return out
}

// servedTraffic sums traffic_daily over today, the last 7 and 30 days (today
// included) and this calendar month, all UTC, and names the sites that took
// the most this month.
func (h *SiteHandler) servedTraffic(ctx context.Context, now time.Time) (map[string]any, error) {
	now = now.UTC()
	day := func(t time.Time) string { return t.Format("2006-01-02") }
	today := now.Truncate(24 * time.Hour)
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	var t, d7, d30, m, mPages, mAPI trafficFigure
	var bytesSince sql.NullTime
	err := h.database.QueryRowContext(ctx, `
		SELECT
		  COALESCE(SUM(bytes) FILTER (WHERE day = $1::date), 0),
		  COALESCE(SUM(requests) FILTER (WHERE day = $1::date), 0),
		  COALESCE(SUM(bytes) FILTER (WHERE day > $1::date - 7), 0),
		  COALESCE(SUM(requests) FILTER (WHERE day > $1::date - 7), 0),
		  COALESCE(SUM(bytes) FILTER (WHERE day > $1::date - 30), 0),
		  COALESCE(SUM(requests) FILTER (WHERE day > $1::date - 30), 0),
		  COALESCE(SUM(bytes) FILTER (WHERE day >= $2::date), 0),
		  COALESCE(SUM(requests) FILTER (WHERE day >= $2::date), 0),
		  COALESCE(SUM(bytes) FILTER (WHERE day >= $2::date AND kind = 'pages'), 0),
		  COALESCE(SUM(requests) FILTER (WHERE day >= $2::date AND kind = 'pages'), 0),
		  COALESCE(SUM(bytes) FILTER (WHERE day >= $2::date AND kind = 'api'), 0),
		  COALESCE(SUM(requests) FILTER (WHERE day >= $2::date AND kind = 'api'), 0),
		  MIN(day) FILTER (WHERE bytes > 0)
		FROM traffic_daily
		WHERE day > LEAST($1::date - 30, $2::date - 1)
	`, day(today), day(month)).Scan(&t.Bytes, &t.Requests, &d7.Bytes, &d7.Requests, &d30.Bytes, &d30.Requests,
		&m.Bytes, &m.Requests, &mPages.Bytes, &mPages.Requests, &mAPI.Bytes, &mAPI.Requests, &bytesSince)
	if err != nil {
		return nil, err
	}
	rows, err := h.database.QueryContext(ctx, `
		SELECT st.site_id, s.user_id, s.name, COALESCE(NULLIF(u.handle, ''), u.username),
		       SUM(st.bytes),
		       COALESCE(SUM(st.bytes) FILTER (WHERE st.kind = 'pages'), 0),
		       COALESCE(SUM(st.bytes) FILTER (WHERE st.kind = 'api'), 0),
		       SUM(st.requests)
		FROM site_traffic_daily st
		JOIN sites s ON s.id = st.site_id
		JOIN users u ON u.id = s.user_id
		WHERE st.day >= $1::date
		GROUP BY st.site_id, s.user_id, s.name, u.handle, u.username
		ORDER BY SUM(st.bytes) DESC, SUM(st.requests) DESC, s.name
		LIMIT $2
	`, day(month), trafficTopSites)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	top := []trafficSite{}
	for rows.Next() {
		var s trafficSite
		if err := rows.Scan(&s.SiteID, &s.UserID, &s.Name, &s.Owner, &s.Bytes, &s.PagesBytes, &s.APIBytes, &s.Requests); err != nil {
			return nil, err
		}
		top = append(top, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := map[string]any{
		"today":        t,
		"last_7_days":  d7,
		"last_30_days": d30,
		"month":        m,
		"month_pages":  mPages,
		"month_api":    mAPI,
		"top_sites":    top,
		// The first day any bytes were logged; the windows that start
		// before it count only from then.
		"bytes_since": nil,
	}
	if bytesSince.Valid {
		out["bytes_since"] = bytesSince.Time.Format("2006-01-02")
	}
	return out, nil
}
