package serve

import (
	"context"
	"crypto/sha256"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"sync"
	"time"

	"simplehost/app"
	"simplehost/core/web"
	sites "simplehost/sites/core"
)

// Visits are counted in memory as pages are served and written to
// stats_daily and stats_keys by Flush, so serving an open site's page needs
// no database write. A visit counts once: a page view is one view; a person
// is one address and browser per site per day. The owner's own visits are
// not counted (they still keep the site from looking abandoned); bots are
// counted apart.
type tally struct {
	views, people, bots         int
	pages, referrers, countries map[string]int
}

var visits = struct {
	sync.Mutex
	day     string
	seen    map[[32]byte]bool
	tallies map[[2]string]*tally // {site id, day}
	visited map[string]bool      // site ids with a human visit, for the idle clock
}{seen: map[[32]byte]bool{}, tallies: map[[2]string]*tally{}, visited: map[string]bool{}}

var bot = regexp.MustCompile(`(?i)bot|crawl|spider|slurp|preview|fetch|curl|wget|python|go-http|headless|lighthouse|facebookexternalhit|^$`)

func (h *handler) count(r *http.Request, site *sites.Site, page string) {
	owner, _ := h.a.People.ActsFor(r.Context(), web.CallerOf(r), site.OwnerID)
	isBot := bot.MatchString(r.UserAgent())
	day := time.Now().UTC().Format("2006-01-02")
	visits.Lock()
	defer visits.Unlock()
	if !isBot {
		visits.visited[site.ID] = true
	}
	if owner {
		return
	}
	if visits.day != day {
		visits.day, visits.seen = day, map[[32]byte]bool{}
	}
	t := visits.tallies[[2]string{site.ID, day}]
	if t == nil {
		t = &tally{pages: map[string]int{}, referrers: map[string]int{}, countries: map[string]int{}}
		visits.tallies[[2]string{site.ID, day}] = t
	}
	if isBot {
		t.bots++
		return
	}
	t.views++
	t.pages[page]++
	who := sha256.Sum256([]byte(site.ID + "\x00" + web.ClientIP(r) + "\x00" + r.UserAgent()))
	if !visits.seen[who] {
		visits.seen[who] = true
		t.people++
	}
	if c := h.a.Countries.Of(web.ClientIP(r)); c != "" {
		t.countries[c]++
	}
	if ref, err := url.Parse(r.Referer()); err == nil && ref.Host != "" && ref.Hostname() != web.Host(r) {
		t.referrers[ref.Hostname()]++
	}
}

// Flush writes the counted visits to the database.
func Flush(ctx context.Context, a *app.App) {
	visits.Lock()
	tallies, visited := visits.tallies, visits.visited
	visits.tallies, visits.visited = map[[2]string]*tally{}, map[string]bool{}
	visits.Unlock()
	for key, t := range tallies {
		_, err := a.DB.ExecContext(ctx, `INSERT INTO stats_daily (site_id, day, views, people, bots) VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (site_id, day) DO UPDATE SET views = stats_daily.views + $3, people = stats_daily.people + $4, bots = stats_daily.bots + $5`,
			key[0], key[1], t.views, t.people, t.bots)
		for kind, m := range map[string]map[string]int{"page": t.pages, "referrer": t.referrers, "country": t.countries} {
			for k, n := range m {
				if err == nil {
					_, err = a.DB.ExecContext(ctx, `INSERT INTO stats_keys (site_id, day, kind, key, n) VALUES ($1, $2, $3, $4, $5)
						ON CONFLICT (site_id, day, kind, key) DO UPDATE SET n = stats_keys.n + $5`, key[0], key[1], kind, k, n)
				}
			}
		}
		if err != nil {
			log.Printf("visit numbers for site %s: %v", key[0], err) // a site deleted meanwhile
		}
	}
	for id := range visited {
		if _, err := a.DB.ExecContext(ctx, `UPDATE sites SET last_visit_at = now() WHERE id = $1`, id); err != nil {
			log.Printf("last visit for site %s: %v", id, err)
		}
	}
}

// Work flushes the visit numbers every 30 seconds, and once more on the way out.
func Work(ctx context.Context, a *app.App) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			Flush(context.Background(), a)
			return
		case <-t.C:
			Flush(ctx, a)
		}
	}
}
