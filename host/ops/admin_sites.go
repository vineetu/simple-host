package ops

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sites"
)

// AdminSiteQuery is the operator's site list request: search words, a filter
// ("", "all", "down", "domain", "unlisted"), a sort key ("created",
// "updated", "size", "live", "views", "name", "owner") and a page.
type AdminSiteQuery struct {
	Search, Filter, Sort string
	Ascending            bool
	Page, Limit          int
}

// AdminSiteRow is one site on the operator's list with its measured footprint
// and its page views over AdminSiteViewDays.
type AdminSiteRow struct {
	Site  model.Site
	Usage SiteUsage
	Views int64
}

// AdminSiteViewDays is the window the operator's list counts views over.
const AdminSiteViewDays = 30

// Which read failed, so the operator sees what to retry.
var (
	ErrAdminSitesList    = errors.New("could not list sites")
	ErrAdminSitesMeasure = errors.New("could not measure sites")
	ErrAdminSitesViews   = errors.New("could not read views")
)

// AdminSites is the operator's site list: every site with its footprint (from
// usage, a cached disk walk), its views and its family address, filtered,
// searched, sorted, then cut to the page. Total counts the matches.
func (s *Reporting) AdminSites(ctx context.Context, hosts *sites.Hosts, disk sites.Disk, usage *UsageCache, q AdminSiteQuery) (rows []AdminSiteRow, total int, measured time.Time, err error) {
	all, err := s.SiteListing(ctx)
	if err != nil {
		return nil, 0, time.Time{}, ErrAdminSitesList
	}
	measuredUsage, measured, err := usage.Get(disk.DataDir(), disk.Changes())
	if err != nil {
		return nil, 0, time.Time{}, ErrAdminSitesMeasure
	}
	sizes := map[string]SiteUsage{}
	for _, u := range measuredUsage.All {
		sizes[u.UserID+"/"+u.Name] = u
	}
	views, err := s.AdminSiteViews(ctx, time.Now().UTC().Add(-AdminSiteViewDays*24*time.Hour))
	if err != nil {
		return nil, 0, time.Time{}, ErrAdminSitesViews
	}
	rows = make([]AdminSiteRow, 0, len(all))
	for _, site := range all {
		family, _ := hosts.SiteFamilyAddress(site.UserID, site.Name)
		if q.Filter == "down" && !site.Suspended() || q.Filter == "domain" && site.CustomDomain.String == "" && family == "" || q.Filter == "unlisted" && site.Visibility != "unlisted" {
			continue
		}
		if !adminSearchMatches(q.Search, site.Name+" "+site.OwnerHandle+" "+site.OwnerUsername+" "+site.CustomDomain.String+" "+family) {
			continue
		}
		rows = append(rows, AdminSiteRow{site, sizes[site.UserID+"/"+site.Name], views[site.ID]})
	}
	sortAdminSites(rows, q.Sort, q.Ascending)
	total = len(rows)
	start := min(q.Page*q.Limit, total)
	end := min(start+q.Limit, total)
	return rows[start:end], total, measured, nil
}

func adminSiteTime(s model.Site) time.Time {
	if s.LastDeployedAt.Valid {
		return s.LastDeployedAt.Time
	}
	return s.CreatedAt
}

func sortAdminSites(rows []AdminSiteRow, key string, ascending bool) {
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		cmp := 0
		switch key {
		case "created":
			cmp = a.Site.CreatedAt.Compare(b.Site.CreatedAt)
		case "updated":
			cmp = adminSiteTime(a.Site).Compare(adminSiteTime(b.Site))
		case "size":
			cmp = intCompare(a.Usage.Bytes, b.Usage.Bytes)
		case "live":
			cmp = intCompare(a.Usage.LiveBytes, b.Usage.LiveBytes)
		case "views":
			cmp = intCompare(a.Views, b.Views)
		case "name":
			cmp = strings.Compare(a.Site.Name, b.Site.Name)
		case "owner":
			cmp = strings.Compare(a.Site.OwnerHandle+a.Site.OwnerUsername, b.Site.OwnerHandle+b.Site.OwnerUsername)
		}
		if cmp == 0 {
			return a.Site.ID < b.Site.ID
		}
		if ascending {
			return cmp < 0
		}
		return cmp > 0
	})
}

// adminSearchMatches applies an admin search box query to text: every plain
// word must appear, and no word written as -word may. Case is ignored; a lone
// "-" is skipped, and an empty query matches everything.
func adminSearchMatches(query, text string) bool {
	text = strings.ToLower(text)
	for _, word := range strings.Fields(strings.ToLower(query)) {
		if exclude, ok := strings.CutPrefix(word, "-"); ok {
			if exclude != "" && strings.Contains(text, exclude) {
				return false
			}
		} else if !strings.Contains(text, word) {
			return false
		}
	}
	return true
}

func intCompare(a, b int64) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}
