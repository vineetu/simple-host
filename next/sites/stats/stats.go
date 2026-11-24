// Package stats is route 30: a site's visit numbers per day, its top pages,
// referrers and countries, for its owner. sites/serve counts the visits.
package stats

import (
	"net/http"
	"strconv"

	"simplehost/app"
	"simplehost/core/web"
)

type handler struct{ a *app.App }

// Mount adds route 30.
func Mount(a *app.App) {
	h := &handler{a}
	a.Routes.Platform.Handle("GET /v1/sites/{site}/stats", web.Func(h.stats))
}

// Day is one day's numbers. People are distinct visitors; bots are counted apart.
type Day struct {
	Day    string `json:"day"`
	Views  int    `json:"views"`
	People int    `json:"people"`
	Bots   int    `json:"bots"`
}

// Count is one page, referrer or country and its views.
type Count struct {
	Key string `json:"key"`
	N   int    `json:"n"`
}

// stats answers for the last ?days= days (1 to 365, default 30).
func (h *handler) stats(w http.ResponseWriter, r *http.Request) error {
	site, err := h.a.Sites.Of(r)
	if err != nil {
		return err
	}
	if err := h.a.Sites.Manage(r, site); err != nil {
		return err
	}
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if days < 1 || days > 365 {
		days = 30
	}
	out := struct {
		Days      []Day   `json:"days"`
		Views     int     `json:"views"`
		People    int     `json:"people"`
		Bots      int     `json:"bots"`
		Pages     []Count `json:"pages"`
		Referrers []Count `json:"referrers"`
		Countries []Count `json:"countries"`
	}{Days: []Day{}, Pages: []Count{}, Referrers: []Count{}, Countries: []Count{}}
	rows, err := h.a.DB.QueryContext(r.Context(), `SELECT to_char(day, 'YYYY-MM-DD'), views, people, bots FROM stats_daily
		WHERE site_id = $1 AND day > current_date - $2::int ORDER BY day`, site.ID, days)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var d Day
		if err := rows.Scan(&d.Day, &d.Views, &d.People, &d.Bots); err != nil {
			return err
		}
		out.Days = append(out.Days, d)
		out.Views, out.People, out.Bots = out.Views+d.Views, out.People+d.People, out.Bots+d.Bots
	}
	if err := rows.Err(); err != nil {
		return err
	}
	keys, err := h.a.DB.QueryContext(r.Context(), `SELECT kind, key, n FROM (
		SELECT kind, key, SUM(n)::int AS n, row_number() OVER (PARTITION BY kind ORDER BY SUM(n) DESC, key) AS rank
		FROM stats_keys WHERE site_id = $1 AND day > current_date - $2::int GROUP BY kind, key) top WHERE rank <= 20 ORDER BY kind, rank`, site.ID, days)
	if err != nil {
		return err
	}
	defer keys.Close()
	for keys.Next() {
		var kind string
		var c Count
		if err := keys.Scan(&kind, &c.Key, &c.N); err != nil {
			return err
		}
		list := map[string]*[]Count{"page": &out.Pages, "referrer": &out.Referrers, "country": &out.Countries}[kind]
		*list = append(*list, c)
	}
	if err := keys.Err(); err != nil {
		return err
	}
	web.JSON(w, http.StatusOK, out)
	return nil
}
