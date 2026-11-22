package siteshttp

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/web"
)

// Recently deleted: DELETE /v1/sites/{site} takes a site offline at once and keeps it
// (files, versions, saved data, claimed names, custom domain) for
// db.DeletedSiteRetention() with its name held; restore brings it back as it was and an
// in-process sweep removes it after the window. Deleting a whole account (admin) is immediate.

type deletedSiteResponse struct {
	Name         string    `json:"name"`
	DeletedAt    time.Time `json:"deleted_at"`
	PurgeAt      time.Time `json:"purge_at"`
	CustomDomain string    `json:"custom_domain,omitempty"`
}

func toDeletedSiteResponse(d model.DeletedSite, retention time.Duration) deletedSiteResponse {
	return deletedSiteResponse{Name: d.Name, DeletedAt: d.DeletedAt.UTC(), PurgeAt: d.PurgeAt(retention).UTC(), CustomDomain: d.CustomDomain.String}
}

// deleteSite moves a site to Recently deleted: marked in the database, and its
// files moved out of every served path, in that order with the commit last so
// a failure leaves the site live and whole.
func (h *SiteHandler) deleteSite(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		httpx.WriteJSON(w, http.StatusUnauthorized, httpx.ErrorResponse{Error: "unauthorized"})
		return
	}
	siteName := strings.TrimSpace(r.PathValue("sitename"))
	if siteName == "" {
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: "site name is required"})
		return
	}
	expected, check := r.Context().Value(web.AdminSiteKey{}).(string)
	in := sites.DeleteInput{UserID: user.ID, Name: siteName, ExpectedSiteID: expected, CheckSiteID: check}
	if r.URL.Query().Has("confirm_name") {
		confirm := r.URL.Query().Get("confirm_name")
		in.ConfirmName = &confirm
	}
	if err := h.LifecycleSvc().Delete(r.Context(), in); err != nil {
		var e *sites.Error
		if errors.As(err, &e) {
			switch e.Code {
			case "confirm_name_mismatch":
				httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: e.Message, Code: e.Code})
				return
			case "delete_not_found":
				httpx.WriteJSON(w, http.StatusNotFound, httpx.ErrorResponse{Error: "site not found"})
				return
			case "site_suspended":
				web.WriteSiteSuspended(w, e.Message)
				return
			}
		}
		httpx.WriteInternal(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *SiteHandler) listDeletedSites(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		httpx.WriteJSON(w, http.StatusUnauthorized, httpx.ErrorResponse{Error: "unauthorized"})
		return
	}
	list, err := h.SitesSvc().ListDeletedSitesByUser(r.Context(), user.ID)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	out := make([]deletedSiteResponse, 0, len(list))
	for _, d := range list {
		out = append(out, toDeletedSiteResponse(d, h.Limits.DeletedRetention))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"sites": out, "retention_days": int(h.Limits.DeletedRetention / (24 * time.Hour))})
}

func (h *SiteHandler) restoreSite(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		httpx.WriteJSON(w, http.StatusUnauthorized, httpx.ErrorResponse{Error: "unauthorized"})
		return
	}
	siteName := strings.TrimSpace(r.PathValue("sitename"))
	result, err := h.LifecycleSvc().Restore(r.Context(), sites.RestoreInput{UserID: user.ID, Name: siteName, Handle: user.Handle.String})
	if err != nil {
		var e *sites.Error
		if errors.As(err, &e) {
			switch e.Code {
			case "site_not_deleted":
				httpx.WriteJSON(w, http.StatusConflict, httpx.ErrorResponse{Error: "that site is not deleted", Code: "site_not_deleted"})
				return
			case "deleted_not_found":
				httpx.WriteJSON(w, http.StatusNotFound, httpx.ErrorResponse{Error: "no site with that name in Recently deleted"})
				return
			case "restore_files":
				httpx.WriteJSON(w, http.StatusInternalServerError, httpx.ErrorResponse{Error: "could not restore the site's files"})
				return
			}
		}
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, h.ToSiteResponse(result.Site, "Restored with all its versions and saved data."))
}

func (h *SiteHandler) PurgeDeletedSites(ctx context.Context) { h.LifecycleSvc().PurgeDeleted(ctx) }

func (h *SiteHandler) startDeletedSitePurge(ctx context.Context, every time.Duration) {
	o := h.siteLoop("sites.trash_purge")
	o.periodicTicker(ctx, 0, every, func(ctx context.Context) { h.PurgeDeletedSites(ctx) })
}
