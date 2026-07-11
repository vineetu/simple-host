package handler

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"

	"github.com/vsriram/simple-host/internal/auth"
	db "github.com/vsriram/simple-host/internal/db"
)

// getSiteAnalytics serves GET /v1/sites/{sitename}/analytics?days=30
// Owner-scoped: resolves the site via the caller's user_id (not global name).
func (h *SiteHandler) getSiteAnalytics(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
		return
	}

	siteName := r.PathValue("sitename")
	site, err := db.GetSiteByUser(r.Context(), h.database, user.ID, siteName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	days := 30
	if v := r.URL.Query().Get("days"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "days must be an integer 1..365"})
			return
		}
		days = n
	}
	if days > 365 {
		days = 365
	}

	totalViews, totalVisitors, daily, err := db.GetSiteAnalytics(r.Context(), h.database, site.ID, days)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	type dayOut struct {
		Day      string `json:"day"`
		Views    int64  `json:"views"`
		Visitors int64  `json:"visitors"`
	}
	outDaily := make([]dayOut, len(daily))
	for i, d := range daily {
		outDaily[i] = dayOut{Day: d.Day, Views: d.Views, Visitors: d.Visitors}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"range_days": days,
		"totals": map[string]int64{
			"views":    totalViews,
			"visitors": totalVisitors,
		},
		"daily": outDaily,
	})
}
