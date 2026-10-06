package handler

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/vsriram/simple-host/internal/capacity"
	"github.com/vsriram/simple-host/internal/db"
)

type adminSiteEntry struct {
	site  db.Site
	size  capacity.SiteUsage
	views int64
}

func adminSiteTime(s db.Site) time.Time {
	if s.LastDeployedAt.Valid {
		return s.LastDeployedAt.Time
	}
	return s.CreatedAt
}

// Sort before pagination, with a stable id tie-breaker across requests.
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
	if !accountAdmin(w, r) {
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
		writeJSON(w, 400, errorResponse{Error: "invalid sort"})
		return
	}
	page, limit := 0, 50
	for name, target := range map[string]*int{"page": &page, "limit": &limit} {
		if value := q.Get(name); value != "" {
			n, err := strconv.Atoi(value)
			if err != nil || n < 0 || n > 1000000 {
				writeJSON(w, 400, errorResponse{Error: "invalid pagination"})
				return
			}
			*target = n
		}
	}
	if limit < 1 || limit > 200 {
		writeJSON(w, 400, errorResponse{Error: "limit must be 1 to 200"})
		return
	}
	filter := q.Get("filter")
	switch filter {
	case "", "all", "down", "domain", "unlisted":
	default:
		writeJSON(w, 400, errorResponse{Error: "invalid filter"})
		return
	}
	sites, err := db.ListAllSites(r.Context(), h.database)
	if err != nil {
		writeJSON(w, 500, errorResponse{Error: "could not list sites"})
		return
	}
	usage, measured, err := h.usage.get(h.disk.DataDir(), h.disk.Changes())
	if err != nil {
		writeJSON(w, 500, errorResponse{Error: "could not measure sites"})
		return
	}
	sizes := map[string]capacity.SiteUsage{}
	for _, s := range usage.All {
		sizes[s.UserID+"/"+s.Name] = s
	}
	views, err := db.AdminSiteViews(r.Context(), h.database, time.Now().UTC().Add(-30*24*time.Hour))
	if err != nil {
		writeJSON(w, 500, errorResponse{Error: "could not read views"})
		return
	}
	rows := make([]adminSiteEntry, 0, len(sites))
	search := strings.ToLower(strings.TrimSpace(q.Get("q")))
	for _, s := range sites {
		family, _ := h.siteFamilyAddress(s.UserID, s.Name)
		if filter == "down" && !s.Suspended() || filter == "domain" && s.CustomDomain.String == "" && family == "" || filter == "unlisted" && s.Visibility != "unlisted" {
			continue
		}
		if search != "" && !strings.Contains(strings.ToLower(s.Name+" "+s.OwnerHandle+" "+s.OwnerUsername+" "+s.CustomDomain.String+" "+family), search) {
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
	writeJSON(w, 200, map[string]any{"sites": out, "total": total, "page": page, "limit": limit, "views_days": 30, "measured_at": measured})
}
