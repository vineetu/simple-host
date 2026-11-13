package handler

import (
	"encoding/csv"
	"fmt"
	"net/http"

	"strconv"
	"strings"
	"time"

	"github.com/vsriram/simple-host/internal/app/operations"
	"github.com/vsriram/simple-host/internal/auth"
	"github.com/vsriram/simple-host/internal/model"
)

// Sign-ups by source (admin only, INTENT 2026-10-15): every account is put in
// one group from its signup_source, the Overview counts them, and a group
// opens to the accounts in it with what each did after joining.

func groupLabel(groups []signupGroup, key string) string {
	for _, g := range groups {
		if g.Key == key {
			return g.Label
		}
	}
	return key
}

// requireAdmin answers 404 to anyone but the admin, like /v1/admin/users: the
// route does not confirm it exists to someone who may not use it.
func requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	user := auth.GetUser(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
		return false
	}
	if !user.IsAdmin || user.Team != nil {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "not found"})
		return false
	}
	return true
}

// adminSignups is GET /v1/admin/signups?group=<key>: the accounts in one
// sign-up source group, what each did after joining, and the group's funnel.
func (h *SiteHandler) adminSignups(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	group := r.URL.Query().Get("group")
	if group == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "group is required"})
		return
	}
	accounts, funnel, groups, err := h.cohort(r, group)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"group": group, "label": groupLabel(groups, group), "funnel": funnel, "accounts": accounts})
}

// adminSignupsCSV is GET /v1/admin/signups.csv?group=<key>: the same accounts
// as a spreadsheet, every cell through csvSafe like the other exports.
func (h *SiteHandler) adminSignupsCSV(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	group := r.URL.Query().Get("group")
	if group == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "group is required"})
		return
	}
	accounts, _, groups, err := h.cohort(r, group)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	rows := [][]string{{"email", "handle", "signed_up_at", "source", "signup_method", "from", "utm_source", "utm_medium", "utm_campaign", "utm_term", "utm_content", "referrer", "landed_at",
		"connected_ai_apps", "first_connect_at", "sites_published", "first_publish_at", "visits", "last_active_at"}}
	label := groupLabel(groups, group)
	for _, a := range accounts {
		d := a.Detail
		if d == nil {
			d = &model.SignupDetail{}
		}
		names := make([]string, len(a.Apps))
		for i, app := range a.Apps {
			names[i] = app.Name
		}
		landed := ""
		if !d.LandedAt.IsZero() {
			landed = formatRFC3339(d.LandedAt)
		}
		rows = append(rows, []string{a.Email, a.Handle, formatRFC3339(a.CreatedAt), label, a.Method, d.From, d.UTMSource, d.UTMMedium, d.UTMCampaign, d.UTMTerm, d.UTMContent, d.Referrer, landed,
			strings.Join(names, "; "), optTime(a.FirstConnectAt), strconv.Itoa(a.SitesPublished), optTime(a.FirstPublishAt), strconv.FormatInt(a.Visits, 10), optTime(a.LastActiveAt)})
	}
	slug := strings.Map(func(c rune) rune {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' {
			return c
		}
		return '-'
	}, group)
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="signups-%s-%s.csv"`, slug, time.Now().UTC().Format("2006-01-02")))
	cw := csv.NewWriter(w)
	defer cw.Flush()
	for _, row := range rows {
		for i := range row {
			row[i] = csvSafe(row[i])
		}
		if err := cw.Write(row); err != nil {
			return
		}
	}
}

func optTime(t *time.Time) string {
	if t == nil {
		return ""
	}
	return formatRFC3339(*t)
}

type signupGroup = operations.SignupGroup

func cohortSources() map[string]operations.SignupGroup {
	out := make(map[string]operations.SignupGroup, len(arrivalSources))
	for k, v := range arrivalSources {
		out[k] = operations.SignupGroup{Key: k, Label: v.Label, Where: v.Where}
	}
	return out
}
func signupGroupOf(source string) string { return operations.SignupGroupOf(source, cohortSources()) }
func signupGroups(present map[string]bool) []signupGroup {
	return operations.SignupGroups(present, cohortSources())
}
func (h *SiteHandler) cohort(r *http.Request, group string) ([]operations.CohortAccount, operations.CohortFunnel, []signupGroup, error) {
	return h.reporting.Cohort(r.Context(), group, cohortSources())
}

// formatRFC3339 is hack.go's rfc3339Time, kept here for cohorts.go's signup
// export, which is not Hack-specific. Same formatting (UTC, RFC 3339).
func formatRFC3339(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}
