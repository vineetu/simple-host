package siteshttp

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
	operations "github.com/vsriram/simple-host/host/ops"
)

type adminSiteEntry struct {
	site  model.Site
	size  operations.SiteUsage
	views int64
}

func adminSiteTime(s model.Site) time.Time {
	if s.LastDeployedAt.Valid {
		return s.LastDeployedAt.Time
	}
	return s.CreatedAt
}

func sortAdminSites(rows []adminSiteEntry, key string, ascending bool) {
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		cmp := 0
		switch key {
		case "created":
			cmp = a.site.CreatedAt.Compare(b.site.CreatedAt)
		case "updated":
			cmp = adminSiteTime(a.site).Compare(adminSiteTime(b.site))
		case "size":
			cmp = intCompare(a.size.Bytes, b.size.Bytes)
		case "live":
			cmp = intCompare(a.size.LiveBytes, b.size.LiveBytes)
		case "views":
			cmp = intCompare(a.views, b.views)
		case "name":
			cmp = strings.Compare(a.site.Name, b.site.Name)
		case "owner":
			cmp = strings.Compare(a.site.OwnerHandle+a.site.OwnerUsername, b.site.OwnerHandle+b.site.OwnerUsername)
		}
		if cmp == 0 {
			return a.site.ID < b.site.ID
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

func (h *SiteHandler) adminSites(w http.ResponseWriter, r *http.Request) {
	if !auth.Admin(w, r) {
		return
	}
	q := r.URL.Query()
	key := q.Get("sort")
	if key == "" {
		key = "updated"
	}
	switch key {
	case "created", "updated", "size", "views", "live", "name", "owner":
	default:
		httpx.WriteJSON(w, 400, httpx.ErrorResponse{Error: "invalid sort"})
		return
	}
	page, err := strconv.Atoi(q.Get("page"))
	if q.Get("page") == "" {
		page, err = 0, nil
	}
	limit, ok := httpx.Limit(q, 50, 200)
	if err != nil || page < 0 || page > 1000000 || !ok {
		httpx.WriteJSON(w, 400, httpx.ErrorResponse{Error: "invalid pagination: page from 0, limit 1 to 200"})
		return
	}
	filter := q.Get("filter")
	switch filter {
	case "", "all", "down", "domain", "unlisted":
	default:
		httpx.WriteJSON(w, 400, httpx.ErrorResponse{Error: "invalid filter"})
		return
	}
	sites, err := h.siteReporting().SiteListing(r.Context())
	if err != nil {
		httpx.WriteJSON(w, 500, httpx.ErrorResponse{Error: "could not list sites"})
		return
	}
	usage, measured, err := h.usage.Get(h.Disk.DataDir(), h.Disk.Changes())
	if err != nil {
		httpx.WriteJSON(w, 500, httpx.ErrorResponse{Error: "could not measure sites"})
		return
	}
	sizes := map[string]operations.SiteUsage{}
	for _, s := range usage.All {
		sizes[s.UserID+"/"+s.Name] = s
	}
	views, err := h.Reporting.AdminSiteViews(r.Context(), time.Now().UTC().Add(-30*24*time.Hour))
	if err != nil {
		httpx.WriteJSON(w, 500, httpx.ErrorResponse{Error: "could not read views"})
		return
	}
	rows := make([]adminSiteEntry, 0, len(sites))
	search := q.Get("q")
	for _, s := range sites {
		family, _ := h.Hosts.SiteFamilyAddress(s.UserID, s.Name)
		if filter == "down" && !s.Suspended() || filter == "domain" && s.CustomDomain.String == "" && family == "" || filter == "unlisted" && s.Visibility != "unlisted" {
			continue
		}
		if !adminSearchMatches(search, s.Name+" "+s.OwnerHandle+" "+s.OwnerUsername+" "+s.CustomDomain.String+" "+family) {
			continue
		}
		rows = append(rows, adminSiteEntry{s, sizes[s.UserID+"/"+s.Name], views[s.ID]})
	}
	sortAdminSites(rows, key, q.Get("dir") == "asc")
	total := len(rows)
	start := min(page*limit, total)
	end := min(start+limit, total)
	out := make([]map[string]any, 0, end-start)
	for _, entry := range rows[start:end] {
		m := h.adminSiteRow(entry.site)
		m["user_id"] = entry.site.UserID
		m["size"] = entry.size.LiveBytes
		m["disk"] = entry.size.Bytes
		m["views"] = entry.views
		out = append(out, m)
	}
	httpx.WriteJSON(w, 200, map[string]any{"sites": out, "total": total, "page": page, "limit": limit, "views_days": 30, "measured_at": measured})
}
