// Package holds is the operator's hand on bad sites: take one site down
// ("taken down" on every address, files kept), suspend an account (all its
// sites taken down, every sign-in refused), and restore either as it was.
// Anyone may report a page (route 64); the operator reads the reports.
package holds

import (
	"net/http"
	"net/url"
	"strings"

	"simplehost/app"
	"simplehost/core/web"
	sites "simplehost/sites/core"
)

type handler struct{ a *app.App }

// Mount adds route 64 (report a page) and route 66 (suspend or restore an
// account).
func Mount(a *app.App) {
	h := &handler{a}
	a.Routes.Platform.Handle("POST /v1/reports", web.Func(h.report))
	a.Routes.Platform.Handle("PATCH /v1/admin/people/{id}", web.Func(h.suspend))
}

var errOperator = web.Refuse(http.StatusForbidden, "operator_only", "Only the operator can do this.")

// TakeDown takes the site down (on) or restores it. Operator only. The
// reason is shown to the owner.
func TakeDown(r *http.Request, a *app.App, site *sites.Site, on bool, reason string) error {
	if !web.CallerOf(r).Operator {
		return errOperator
	}
	q := `UPDATE sites SET taken_down_at = NULL, taken_down_reason = '' WHERE id = $1`
	args := []any{site.ID}
	if on {
		q, args = `UPDATE sites SET taken_down_at = now(), taken_down_reason = $2 WHERE id = $1`, append(args, strings.TrimSpace(reason))
	}
	if _, err := a.DB.ExecContext(r.Context(), q, args...); err != nil {
		return err
	}
	if on {
		a.DB.ExecContext(r.Context(), `UPDATE reports SET handled_at = now() WHERE site_id = $1 AND handled_at IS NULL`, site.ID)
	}
	a.Sites.Forget()
	return nil
}

// suspend is route 66: {"suspended": true, "reason": "..."} or false to restore.
func (h *handler) suspend(w http.ResponseWriter, r *http.Request) error {
	if !web.CallerOf(r).Operator {
		return errOperator
	}
	var in struct {
		Suspended bool   `json:"suspended"`
		Reason    string `json:"reason"`
	}
	if err := web.Read(r, &in); err != nil {
		return err
	}
	q := `UPDATE people SET suspended_at = NULL, suspended_reason = '' WHERE id::text = $1 AND NOT operator`
	args := []any{r.PathValue("id")}
	if in.Suspended {
		q, args = `UPDATE people SET suspended_at = now(), suspended_reason = $2 WHERE id::text = $1 AND NOT operator`, append(args, strings.TrimSpace(in.Reason))
	}
	res, err := h.a.DB.ExecContext(r.Context(), q, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return web.NotFound
	}
	h.a.Sites.Forget()
	web.JSON(w, http.StatusOK, map[string]any{"id": r.PathValue("id"), "suspended": in.Suspended})
	return nil
}

// report is route 64: anyone reports a page by its address, with a reason.
func (h *handler) report(w http.ResponseWriter, r *http.Request) error {
	var in struct{ URL, Reason string }
	if err := web.Read(r, &in); err != nil {
		return err
	}
	u, err := url.Parse(strings.TrimSpace(in.URL))
	in.Reason = strings.TrimSpace(in.Reason)
	if err != nil || u.Host == "" || in.Reason == "" || len(in.Reason) > 2000 || len(in.URL) > 2000 {
		return web.Refuse(http.StatusBadRequest, "bad_report", "Give the page's full address and a reason of at most 2000 characters.")
	}
	var siteID any
	if p, err := h.a.Sites.Lookup(r.Context(), u.Hostname()); err == nil && p.Site != nil {
		siteID = p.Site.ID
	}
	if _, err := h.a.DB.ExecContext(r.Context(), `INSERT INTO reports (url, reason, site_id) VALUES ($1, $2, $3)`, u.String(), in.Reason, siteID); err != nil {
		return err
	}
	web.JSON(w, http.StatusOK, map[string]string{"reported": "Thank you. The operator will look at it."})
	return nil
}
