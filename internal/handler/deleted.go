package handler

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/vsriram/simple-host/internal/auth"
	db "github.com/vsriram/simple-host/internal/db"
)

// Recently deleted (completeness plan, 2026-09-27).
//
// DELETE /v1/sites/{site} takes a site offline at once and keeps it — files,
// every version, saved state, collections, private-list settings, claimed
// names, custom domain — for db.DeletedSiteRetention with its name held.
// GET /v1/me/deleted-sites lists them, POST /v1/sites/{site}/restore brings one
// back exactly as it was, and an in-process sweep removes them for good after
// the window. Deleting a whole account (admin) stays immediate.

type deletedSiteResponse struct {
	Name         string    `json:"name"`
	DeletedAt    time.Time `json:"deleted_at"`
	PurgeAt      time.Time `json:"purge_at"`
	CustomDomain string    `json:"custom_domain,omitempty"`
}

func toDeletedSiteResponse(d db.DeletedSite) deletedSiteResponse {
	return deletedSiteResponse{Name: d.Name, DeletedAt: d.DeletedAt.UTC(), PurgeAt: d.PurgeAt().UTC(), CustomDomain: d.CustomDomain.String}
}

// deletedNameMessage is the answer when a name is held by a site in Recently
// deleted: the owner can restore it, or wait until it is gone.
func deletedNameMessage(d db.DeletedSite) string {
	return "you deleted a site named " + d.Name + " recently; it is in Recently deleted until " +
		d.PurgeAt().UTC().Format("2 Jan 2006") + ". Restore it, or pick another name"
}

// deletedNameConflict reports whether the account's name is held by a site in
// Recently deleted, with the message to show.
func (h *SiteHandler) deletedNameConflict(ctx context.Context, userID, name string) (string, bool) {
	d, err := db.GetDeletedSiteByUser(ctx, h.database, userID, name)
	if err != nil {
		return "", false
	}
	return deletedNameMessage(d), true
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
	unlock := h.lockSite(siteName)
	defer unlock()

	site, err := db.GetSiteByUser(r.Context(), h.database, user.ID, siteName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	tx, err := h.database.BeginTx(r.Context(), nil)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	defer tx.Rollback()
	if err := db.MarkSiteDeleted(r.Context(), tx, site.ID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	if err := h.disk.TrashSite(site.UserID, site.Name, site.ID); err != nil {
		log.Printf("delete site %s: %v", site.ID, err)
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	if err := tx.Commit(); err != nil {
		if rerr := h.disk.RestoreSite(site.UserID, site.Name, site.ID); rerr != nil {
			log.Printf("delete site %s: commit failed and files could not be moved back: %v", site.ID, rerr)
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
		out = append(out, toDeletedSiteResponse(d))
	}
	writeJSON(w, http.StatusOK, map[string]any{"sites": out, "retention_days": int(db.DeletedSiteRetention / (24 * time.Hour))})
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
	unlock := h.lockSite(siteName)
	defer unlock()

	d, err := db.GetDeletedSiteByUser(r.Context(), h.database, user.ID, siteName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			if _, lerr := db.GetSiteByUser(r.Context(), h.database, user.ID, siteName); lerr == nil {
				writeJSON(w, http.StatusConflict, errorResponse{Error: "that site is not deleted"})
				return
			}
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "no site with that name in Recently deleted"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	if !user.IsAdmin {
		existing, err := db.ListSitesByUser(r.Context(), h.database, user.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
			return
		}
		if len(existing) >= maxSitesPerUser {
			writeJSON(w, http.StatusForbidden, errorResponse{Error: "site quota reached; delete another site first"})
			return
		}
	}
	tx, err := h.database.BeginTx(r.Context(), nil)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	defer tx.Rollback()
	if err := db.RestoreDeletedSite(r.Context(), tx, d.ID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "no site with that name in Recently deleted"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	if err := h.disk.RestoreSite(d.UserID, d.Name, d.ID); err != nil {
		log.Printf("restore site %s: %v", d.ID, err)
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "could not restore the site's files"})
		return
	}
	if err := tx.Commit(); err != nil {
		if terr := h.disk.TrashSite(d.UserID, d.Name, d.ID); terr != nil {
			log.Printf("restore site %s: commit failed and files could not be moved back: %v", d.ID, terr)
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	// The handle may have changed while the site was deleted.
	if user.Handle.Valid && user.Handle.String != "" {
		if err := h.disk.EnsureHandleLink(user.Handle.String, user.ID); err != nil {
			log.Printf("restore site: ensure handle link %s: %v", user.Handle.String, err)
		}
		h.RequestSiteCert(user.Handle.String)
	}
	site, err := db.GetSiteByUser(r.Context(), h.database, user.ID, d.Name)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	writeJSON(w, http.StatusOK, h.toSiteResponse(site, "Restored with all its versions and saved data."))
}

// purgeDeletedSites removes sites whose restore window has passed: the row
// (versions, data and claimed names cascade) first, then the files.
func (h *SiteHandler) purgeDeletedSites(ctx context.Context) {
	list, err := db.ListPurgeableSites(ctx, h.database)
	if err != nil {
		log.Printf("deleted-site purge: list: %v", err)
		return
	}
	for _, d := range list {
		unlock := h.lockSite(d.Name)
		domains, err := db.PurgeDeletedSite(ctx, h.database, d.ID)
		unlock()
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				log.Printf("deleted-site purge: %s: %v", d.ID, err)
			}
			continue
		}
		if err := h.disk.PurgeTrashedSite(d.UserID, d.ID); err != nil {
			log.Printf("deleted-site purge: files of %s: %v", d.ID, err)
		}
		if err := h.disk.PurgeTrashedSiteLinks(d.UserID, d.Name, strings.ToLower(d.CustomDomain.String)); err != nil {
			log.Printf("deleted-site purge: links of %s: %v", d.ID, err)
		}
		for _, dom := range domains {
			if err := h.disk.UnbindDomain(dom); err != nil {
				log.Printf("deleted-site purge: unbind %s: %v", dom, err)
			}
			h.cancelDomainCert(dom)
		}
		log.Printf("deleted-site purge: removed %s (%s)", d.Name, d.ID)
	}
}

// StartDeletedSitePurge runs the purge now and then every interval until ctx
// ends.
func (h *SiteHandler) StartDeletedSitePurge(ctx context.Context, every time.Duration) {
	go func() {
		h.purgeDeletedSites(ctx)
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				h.purgeDeletedSites(ctx)
			}
		}
	}()
}
