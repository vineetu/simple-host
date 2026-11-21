package siteshttp

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"

	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
)

// getSiteAnalytics is owner-scoped; `owner` lets the platform admin read another account's site (see analyticsSite).
func (h *SiteHandler) getSiteAnalytics(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		httpx.WriteJSON(w, http.StatusUnauthorized, httpx.ErrorResponse{Error: "unauthorized"})
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

	stats, err := h.Reporting.SiteAnalytics(r.Context(), site.ID, days)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, stats)
}

// "XX" is traffic the local IP-to-country table could not place, kept so the numbers add up.
func (h *SiteHandler) getSiteGeoAnalytics(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		httpx.WriteJSON(w, http.StatusUnauthorized, httpx.ErrorResponse{Error: "unauthorized"})
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

	rows, err := h.Reporting.SiteGeo(r.Context(), site.ID, days)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}

	countries := rows

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"range_days": days,
		"countries":  countries,
	})
}

// A referrer is only ever a domain: the log keeps nothing more of it.
func (h *SiteHandler) getSiteTopAnalytics(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		httpx.WriteJSON(w, http.StatusUnauthorized, httpx.ErrorResponse{Error: "unauthorized"})
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
	pages, refs, err := h.Reporting.SiteTop(r.Context(), site.ID, days)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
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
	site, err := h.Reporting.AnalyticsSite(r.Context(), user, r.URL.Query().Get("owner"), r.PathValue("sitename"))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.WriteJSON(w, http.StatusNotFound, httpx.ErrorResponse{Error: "site not found"})
			return model.Site{}, false
		}
		httpx.WriteInternal(w)
		return model.Site{}, false
	}
	return site, true
}

func analyticsDays(w http.ResponseWriter, r *http.Request) (int, bool) {
	v := r.URL.Query().Get("days")
	if v == "" {
		return 30, true
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: "days must be an integer 1..365"})
		return 0, false
	}
	return min(n, 365), true
}

// getAnalyticsSummary returns per-site totals in traffic order, which one request per site
// cannot give the dashboard. `all=1` widens it to every site on the instance, admins only.
func (h *SiteHandler) getAnalyticsSummary(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		httpx.WriteJSON(w, http.StatusUnauthorized, httpx.ErrorResponse{Error: "unauthorized"})
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
		httpx.WriteJSON(w, http.StatusForbidden, httpx.ErrorResponse{Error: "admin only"})
		return
	}

	summary, err := h.Reporting.AnalyticsSummary(r.Context(), user.ID, days, allSites)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"range_days": days,
		"sites":      summary,
	})
}
