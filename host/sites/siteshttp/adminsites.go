package siteshttp

import (
	"net/http"
	"strconv"

	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/httpx"
	operations "github.com/vsriram/simple-host/host/ops"
)

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
	rows, total, measured, err := h.siteReporting().AdminSites(r.Context(), h.Hosts, h.Disk, &h.usage, operations.AdminSiteQuery{
		Search: q.Get("q"), Filter: filter, Sort: key, Ascending: q.Get("dir") == "asc", Page: page, Limit: limit,
	})
	if err != nil {
		httpx.WriteJSON(w, 500, httpx.ErrorResponse{Error: err.Error()})
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, entry := range rows {
		m := h.adminSiteRow(entry.Site)
		m["user_id"] = entry.Site.UserID
		m["size"] = entry.Usage.LiveBytes
		m["disk"] = entry.Usage.Bytes
		m["views"] = entry.Views
		out = append(out, m)
	}
	httpx.WriteJSON(w, 200, map[string]any{"sites": out, "total": total, "page": page, "limit": limit, "views_days": operations.AdminSiteViewDays, "measured_at": measured})
}
