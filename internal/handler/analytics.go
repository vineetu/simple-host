package handler

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"

	"github.com/vsriram/simple-host/internal/analytics"
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

	days, ok := analyticsDays(w, r)
	if !ok {
		return
	}

	stats, err := db.GetSiteAnalytics(r.Context(), h.database, site.ID, days)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	writeJSON(w, http.StatusOK, stats)
}

// countryStat is one row of the geo endpoint. The display name is resolved on
// the way out rather than stored, so refreshing the IP dataset never means
// rewriting aggregates.
type countryStat struct {
	Country     string `json:"country"`
	CountryName string `json:"country_name"`
	db.Split
}

// getSiteGeoAnalytics serves GET /v1/sites/{sitename}/analytics/geo?days=30
// Owner-scoped like getSiteAnalytics. Countries come back ordered by real
// people descending; "XX" is traffic whose address the local IP-to-country
// table could not place, kept in the list so the numbers still add up.
func (h *SiteHandler) getSiteGeoAnalytics(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
		return
	}

	site, err := db.GetSiteByUser(r.Context(), h.database, user.ID, r.PathValue("sitename"))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	days, ok := analyticsDays(w, r)
	if !ok {
		return
	}

	rows, err := db.GetSiteGeo(r.Context(), h.database, site.ID, days)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	countries := make([]countryStat, 0, len(rows))
	for _, c := range rows {
		countries = append(countries, countryStat{
			Country:     c.Country,
			CountryName: analytics.CountryName(c.Country),
			Split:       c.Split,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"range_days": days,
		"countries":  countries,
	})
}

// analyticsDays reads ?days=: default 30, clamped to 365, anything that is not
// a positive integer is a 400 (already written when ok is false).
func analyticsDays(w http.ResponseWriter, r *http.Request) (int, bool) {
	v := r.URL.Query().Get("days")
	if v == "" {
		return 30, true
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "days must be an integer 1..365"})
		return 0, false
	}
	return min(n, 365), true
}

// getAnalyticsSummary serves GET /v1/analytics/sites?days=30[&all=1]
// Per-site totals for every site the caller owns, ordered by real people
// descending. The dashboard needs all sites' numbers before it can render them
// in traffic order, which one-request-per-site cannot provide.
// `all=1` widens the scope to every site on the instance, admins only.
func (h *SiteHandler) getAnalyticsSummary(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
		return
	}

	days, ok := analyticsDays(w, r)
	if !ok {
		return
	}

	// Silently narrowing a non-admin's all=1 to their own sites would be a lie
	// about what was returned; refuse it instead.
	allSites := r.URL.Query().Get("all") == "1"
	if allSites && !user.IsAdmin {
		writeJSON(w, http.StatusForbidden, errorResponse{Error: "admin only"})
		return
	}

	summary, err := db.GetAnalyticsSummary(r.Context(), h.database, user.ID, days, allSites)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"range_days": days,
		"sites":      summary,
	})
}
