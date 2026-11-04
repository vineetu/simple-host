package handler

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/vsriram/simple-host/internal/app/sites"
	"github.com/vsriram/simple-host/internal/auth"
	db "github.com/vsriram/simple-host/internal/db"
)

// Recently deleted (completeness plan, 2026-09-27).
//
// DELETE /v1/sites/{site} takes a site offline at once and keeps it — files,
// every version, saved state, collections, private-list settings, claimed
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

func toDeletedSiteResponse(d db.DeletedSite, retention time.Duration) deletedSiteResponse {
	return deletedSiteResponse{Name: d.Name, DeletedAt: d.DeletedAt.UTC(), PurgeAt: d.PurgeAt(retention).UTC(), CustomDomain: d.CustomDomain.String}
}

// deletedNameMessage is the answer when a name is held by a site in Recently
// deleted: the owner can restore it, or wait until it is gone.
func deletedNameMessage(d db.DeletedSite, retention time.Duration) string {
	return "you deleted a site named " + d.Name + " recently; it is in Recently deleted until " +
		d.PurgeAt(retention).UTC().Format("2 Jan 2006") + ". Restore it, or pick another name"
}

// deletedNameConflict reports whether the account's name is held by a site in
// Recently deleted, with the message to show.
func (h *SiteHandler) deletedNameConflict(ctx context.Context, userID, name string) (string, bool) {
	d, err := db.GetDeletedSiteByUser(ctx, h.database, userID, name)
	if err != nil {
		return "", false
	}
	return deletedNameMessage(d, h.limits.DeletedRetention), true
}

// deleteSite moves a site to Recently deleted: marked in the database, and its
// files moved out of every served path, in that order with the commit last so
// a failure leaves the site live and whole.
func (h *SiteHandler) deleteSite(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
		return
	}
	siteName := strings.TrimSpace(r.PathValue("sitename"))
	if siteName == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "site name is required"})
		return
	}
	if err := sites.ValidateShape(siteName); err != nil {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
		return
	}
	expected, check := r.Context().Value(adminSiteKey{}).(string)
	if err := h.lifecycleSvc().Delete(r.Context(), sites.DeleteInput{UserID: user.ID, Name: siteName, ExpectedSiteID: expected, CheckSiteID: check}); err != nil {
		var e *sites.Error
		if errors.As(err, &e) {
			switch e.Code {
			case "delete_not_found":
				writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
				return
			case "site_suspended":
				writeSiteSuspended(w, e.Message)
				return
			}
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *SiteHandler) trashSite(ctx context.Context, site db.Site, also func(tx *sql.Tx) error) error {
	return h.lifecycleSvcWithTransaction(also).Trash(ctx, sites.TrashInput{Site: site})
}

// listDeletedSites: GET /v1/me/deleted-sites.
func (h *SiteHandler) listDeletedSites(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
		return
	}
	list, err := db.ListDeletedSitesByUser(r.Context(), h.database, user.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	out := make([]deletedSiteResponse, 0, len(list))
	for _, d := range list {
		out = append(out, toDeletedSiteResponse(d, h.limits.DeletedRetention))
	}
	writeJSON(w, http.StatusOK, map[string]any{"sites": out, "retention_days": int(h.limits.DeletedRetention / (24 * time.Hour))})
}

// restoreSite: POST /v1/sites/{sitename}/restore. The site comes back under
// the same name, with the same versions, data, claimed names and domain.
func (h *SiteHandler) restoreSite(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
		return
	}
	siteName := strings.TrimSpace(r.PathValue("sitename"))
	if err := sites.ValidateShape(siteName); err != nil {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "no site with that name in Recently deleted"})
		return
	}
	result, err := h.lifecycleSvc().Restore(r.Context(), sites.RestoreInput{UserID: user.ID, Name: siteName, Handle: user.Handle.String})
	if err != nil {
		var e *sites.Error
		if errors.As(err, &e) {
			switch e.Code {
			case "site_not_deleted":
				writeJSON(w, http.StatusConflict, errorResponse{Error: "that site is not deleted", Code: "site_not_deleted"})
				return
			case "deleted_not_found":
				writeJSON(w, http.StatusNotFound, errorResponse{Error: "no site with that name in Recently deleted"})
				return
			case "restore_files":
				writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "could not restore the site's files"})
				return
			}
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	writeJSON(w, http.StatusOK, h.toSiteResponseState(result.Site, "Restored with all its versions and saved data.", result.ResponseState.(siteResponseState)))
}

var errRestoreFiles = sites.ErrRestoreFiles

func (h *SiteHandler) wakeImageScans(ctx context.Context, siteID string) {
	h.lifecycleSvc().WakeImageScans(ctx, siteID)
}

func (h *SiteHandler) restoreTrashedSite(ctx context.Context, d db.DeletedSite, handle string, also func(tx *sql.Tx) error) (db.Site, error) {
	return h.lifecycleSvcWithTransaction(also).RestoreTrashed(ctx, sites.RestoreTrashedInput{Site: d, Handle: handle})
}

func (h *SiteHandler) purgeDeletedSites(ctx context.Context) { h.lifecycleSvc().PurgeDeleted(ctx) }

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
	o.periodicTicker(ctx, 0, every, func(ctx context.Context) { h.purgeDeletedSites(ctx) })
}
