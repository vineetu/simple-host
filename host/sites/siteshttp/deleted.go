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

// Recently deleted (completeness plan, 2026-09-27).
//
// DELETE /v1/sites/{site} takes a site offline at once and keeps it — files,
// every version, saved data, claimed
// names, custom domain — for db.DeletedSiteRetention() with its name held.
// GET /v1/me/deleted-sites lists them, POST /v1/sites/{site}/restore brings one
// back exactly as it was, and an in-process sweep removes them for good after
// the window. Deleting a whole account (admin) stays immediate.

type deletedSiteResponse struct {
	Name         string    `json:"name"`
	DeletedAt    time.Time `json:"deleted_at"`
	PurgeAt      time.Time `json:"purge_at"`
	CustomDomain string    `json:"custom_domain,omitempty"`
}

func toDeletedSiteResponse(d model.DeletedSite, retention time.Duration) deletedSiteResponse {
	return deletedSiteResponse{Name: d.Name, DeletedAt: d.DeletedAt.UTC(), PurgeAt: d.PurgeAt(retention).UTC(), CustomDomain: d.CustomDomain.String}
}

// deletedNameMessage is the answer when a name is held by a site in Recently
// deleted: the owner can restore it, or wait until it is gone.
func deletedNameMessage(d model.DeletedSite, retention time.Duration) string {
	return "you deleted a site named " + d.Name + " recently; it is in Recently deleted until " +
		d.PurgeAt(retention).UTC().Format("2 Jan 2006") + ". Restore it, or pick another name"
}

// deletedNameConflict reports whether the account's name is held by a site in
// Recently deleted, with the message to show.
func (h *SiteHandler) deletedNameConflict(ctx context.Context, userID, name string) (string, bool) {
	d, err := h.SitesSvc().GetDeletedSiteByUser(ctx, userID, name)
	if err != nil {
		return "", false
	}
	return deletedNameMessage(d, h.Limits.DeletedRetention), true
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
	if err := h.LifecycleSvc().Delete(r.Context(), sites.DeleteInput{UserID: user.ID, Name: siteName, ExpectedSiteID: expected, CheckSiteID: check}); err != nil {
		var e *sites.Error
		if errors.As(err, &e) {
			switch e.Code {
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

// listDeletedSites: GET /v1/me/deleted-sites.
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

// restoreSite: POST /v1/sites/{sitename}/restore. The site comes back under
// the same name, with the same versions, data, claimed names and domain.
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
	httpx.WriteJSON(w, http.StatusOK, h.toSiteResponseState(result.Site, "Restored with all its versions and saved data.", result.ResponseState.(siteResponseState)))
}

func (h *SiteHandler) PurgeDeletedSites(ctx context.Context) { h.LifecycleSvc().PurgeDeleted(ctx) }

// StartDeletedSitePurge runs the purge now and then every interval until ctx
// ends.
func (h *SiteHandler) StartDeletedSitePurge(ctx context.Context, every time.Duration) {
	o := h.siteLoop("sites.trash_purge")
	o.once.Do(func() {
		if o.enabled() {
			h.startDeletedSitePurge(ctx, every)
		}
	})
}
func (h *SiteHandler) startDeletedSitePurge(ctx context.Context, every time.Duration) {
	o := h.siteLoop("sites.trash_purge")
	o.periodicTicker(ctx, 0, every, func(ctx context.Context) { h.PurgeDeletedSites(ctx) })
}
