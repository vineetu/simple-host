package handler

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"
	"time"
)

// The traffic figures: today, 7 and 30 days, the calendar month with its
// pages/API split, and the top sites with their owners. Dated in 2031 so no
// other test's rows fall in the windows.
func TestServedTraffic(t *testing.T) {
	a := newPrivateApp(t)
	db := a.database
	ctx := context.Background()
	owner := "tr" + strconv.FormatInt(time.Now().UnixNano(), 36)
	var userID string
	if err := db.QueryRow(`INSERT INTO users (username, handle) VALUES ($1, $1) RETURNING id`, owner).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	var big, small string
	db.QueryRow(`INSERT INTO sites (user_id, name) VALUES ($1, 'tr-big') RETURNING id`, userID).Scan(&big)
	db.QueryRow(`INSERT INTO sites (user_id, name) VALUES ($1, 'tr-small') RETURNING id`, userID).Scan(&small)
	t.Cleanup(func() {
		db.Exec(`DELETE FROM traffic_daily WHERE day >= '2031-01-01' AND day < '2032-01-01'`)
		db.Exec(`DELETE FROM sites WHERE id IN ($1, $2)`, big, small)
		db.Exec(`DELETE FROM users WHERE id = $1`, userID)
	})
	ins := func(day, kind string, bytes, reqs int64) {
		t.Helper()
		if _, err := db.Exec(`INSERT INTO traffic_daily (day, kind, bytes, requests) VALUES ($1, $2, $3, $4)
			ON CONFLICT (day, kind) DO UPDATE SET bytes = EXCLUDED.bytes, requests = EXCLUDED.requests`, day, kind, bytes, reqs); err != nil {
			t.Fatal(err)
		}
	}
	ins("2031-03-10", "pages", 1000, 10) // today
	ins("2031-03-10", "api", 100, 5)
	ins("2031-03-04", "pages", 2000, 20) // 7 days (today is the 7th)
	ins("2031-03-01", "pages", 4000, 40) // the month's first day, 10 days back
	ins("2031-02-20", "pages", 8000, 80) // 30 days, last month
	ins("2031-02-01", "pages", 0, 99)    // over 30 days back, and no bytes
	site := func(id, day, kind string, bytes, reqs int64) {
		t.Helper()
		if _, err := db.Exec(`INSERT INTO site_traffic_daily (site_id, day, kind, bytes, requests) VALUES ($1, $2, $3, $4, $5)`, id, day, kind, bytes, reqs); err != nil {
			t.Fatal(err)
		}
	}
	site(big, "2031-03-10", "pages", 900, 9)
	site(big, "2031-03-02", "api", 90, 3)
	site(small, "2031-03-05", "pages", 50, 1)
	site(small, "2031-02-28", "pages", 99999, 1) // last month

	a.sites.SetTrafficLog(true)
	got := a.sites.networkUsage(ctx, time.Date(2031, 3, 10, 18, 0, 0, 0, time.UTC))
	raw, _ := json.Marshal(got)
	var out struct {
		Served struct {
			Today      trafficFigure `json:"today"`
			Last7      trafficFigure `json:"last_7_days"`
			Last30     trafficFigure `json:"last_30_days"`
			Month      trafficFigure `json:"month"`
			MonthPages trafficFigure `json:"month_pages"`
			MonthAPI   trafficFigure `json:"month_api"`
			BytesSince string        `json:"bytes_since"`
			Top        []trafficSite `json:"top_sites"`
		} `json:"served"`
		Box         any `json:"box"`
		AllowanceGB int `json:"allowance_gb"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	s := out.Served
	last7 := s.Last7
	if s.Today.Bytes != 1100 || s.Today.Requests != 15 {
		t.Errorf("today = %+v", s.Today)
	}
	if last7.Bytes != 3100 {
		t.Errorf("7 days = %d, want 3100", last7.Bytes)
	}
	if s.Last30.Bytes != 15100 {
		t.Errorf("30 days = %d, want 15100", s.Last30.Bytes)
	}
	if s.Month.Bytes != 7100 || s.MonthPages.Bytes != 7000 || s.MonthAPI.Bytes != 100 {
		t.Errorf("month = %d (pages %d, api %d), want 7100 (7000, 100)", s.Month.Bytes, s.MonthPages.Bytes, s.MonthAPI.Bytes)
	}
	if len(s.Top) < 2 || s.Top[0].Name != "tr-big" || s.Top[0].Bytes != 990 || s.Top[0].APIBytes != 90 || s.Top[0].Owner != owner {
		t.Fatalf("top = %+v", s.Top)
	}
	if s.Top[1].Name != "tr-small" || s.Top[1].Bytes != 50 {
		t.Errorf("second = %+v (last month's bytes must not count)", s.Top[1])
	}
	if s.BytesSince != "2031-02-20" {
		t.Errorf("bytes_since = %q, want 2031-02-20", s.BytesSince)
	}
	if out.Box != nil {
		t.Errorf("box = %v with the counters off, want null", out.Box)
	}
	if out.AllowanceGB != 10240 {
		t.Errorf("allowance = %d, want the default 10240", out.AllowanceGB)
	}
}
