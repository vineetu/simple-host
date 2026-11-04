package handler

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"

	"github.com/vsriram/simple-host/internal/app/operations"
	"github.com/vsriram/simple-host/internal/auth"
	"github.com/vsriram/simple-host/internal/model"
)

// getSiteAnalytics serves GET /v1/sites/{sitename}/analytics?days=30[&owner=<handle>]
// Owner-scoped: resolves the site via the caller's user_id (not global name).
// `owner` lets the platform admin read another account's site (see analyticsSite).
func (h *SiteHandler) getSiteAnalytics(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
		return
	}
	h.noteOwnerBrowser(r, user)

	site, ok := h.analyticsSite(w, r, user)
	if !ok {
		return
	}

	days, ok := analyticsDays(w, r)
	if !ok {
		return
	}

	stats, err := h.reporting.SiteAnalytics(r.Context(), site.ID, days)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	writeJSON(w, http.StatusOK, stats)
}

// countryStat is one row of the geo endpoint. The display name is resolved on
// the way out rather than stored, so refreshing the IP dataset never means
// rewriting aggregates.
type countryStat = operations.CountryStat

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
	h.noteOwnerBrowser(r, user)

	site, ok := h.analyticsSite(w, r, user)
	if !ok {
		return
	}

	days, ok := analyticsDays(w, r)
	if !ok {
		return
	}

	rows, err := h.reporting.SiteGeo(r.Context(), site.ID, days)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	countries := rows

	writeJSON(w, http.StatusOK, map[string]any{
		"range_days": days,
		"countries":  countries,
	})
}

// getSiteTopAnalytics serves GET /v1/sites/{sitename}/analytics/top?days=30:
// the site's most viewed pages and top referring domains for the range,
// people only. Owner-scoped like getSiteAnalytics. A referrer is only ever a
// domain: the log keeps nothing more of it.
func (h *SiteHandler) getSiteTopAnalytics(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
		return
	}
	h.noteOwnerBrowser(r, user)
	site, ok := h.analyticsSite(w, r, user)
	if !ok {
		return
	}
	days, ok := analyticsDays(w, r)
	if !ok {
		return
	}
	pages, refs, err := h.reporting.SiteTop(r.Context(), site.ID, days)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"range_days": days,
		"pages":      pages,
		"referrers":  refs,
	})
}

// analyticsSite resolves the site the analytics endpoints report on, or writes
// the answer and returns ok=false. Without ?owner= (or with the caller's own
// handle) it is the caller's own site by name, exactly as before. Any other
// owner is honoured for the platform admin only; for everyone else it is the
// same 404 as a site that does not exist, so the parameter cannot be used to
// learn which handles or sites exist.
func (h *SiteHandler) analyticsSite(w http.ResponseWriter, r *http.Request, user *model.User) (model.Site, bool) {
	site, err := h.reporting.AnalyticsSite(r.Context(), user, r.URL.Query().Get("owner"), r.PathValue("sitename"))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
			return model.Site{}, false
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return model.Site{}, false
	}
	return site, true
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
	h.noteOwnerBrowser(r, user)

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

	summary, err := h.reporting.AnalyticsSummary(r.Context(), user.ID, days, allSites)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"range_days": days,
		"sites":      summary,
	})
}

func (h *SiteHandler) SetOperationsReporting(s *operations.Reporting) { h.reporting = s }
