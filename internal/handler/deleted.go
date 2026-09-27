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
	if err := validateSiteShape(siteName); err != nil {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
		return
	}
	// Look the site up before taking its lock, so a request for a name the
	// account does not have never adds a lock; read it again under the lock.
	if _, err := db.GetSiteByUser(r.Context(), h.database, user.ID, siteName); errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
		return
	}
	unlock := h.lockSite(user.ID, siteName)
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
	// A site the operator took down stays as it is: its owner cannot delete it.
	if refuseSuspendedSite(w, site) {
		return
	}
	if err := h.trashSite(r.Context(), site, nil); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// trashSite moves a live site to Recently deleted: marked in the database
// (plus whatever also records in the same transaction), and its files moved
// out of every served path, with the commit last so a failure leaves the site
// live and whole. The caller holds the site's lock. sql.ErrNoRows when the
// site is no longer live.
func (h *SiteHandler) trashSite(ctx context.Context, site db.Site, also func(tx *sql.Tx) error) error {
	tx, err := h.database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := db.MarkSiteDeleted(ctx, tx, site.ID); err != nil {
		return err
	}
	if also != nil {
		if err := also(tx); err != nil {
			return err
		}
	}
	if err := h.disk.TrashSite(site.UserID, site.Name, site.ID); err != nil {
		log.Printf("delete site %s: %v", site.ID, err)
		return err
	}
	if err := tx.Commit(); err != nil {
		if rerr := h.disk.RestoreSite(site.UserID, site.Name, site.ID); rerr != nil {
			log.Printf("delete site %s: commit failed and files could not be moved back: %v", site.ID, rerr)
		}
		return err
	}
	return nil
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
	if err := validateSiteShape(siteName); err != nil {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "no site with that name in Recently deleted"})
		return
	}
	// Lock only a name the account really has in Recently deleted (checked
	// again under the lock below); the answers for other names need no lock.
	unlock := func() {}
	if _, err := db.GetDeletedSiteByUser(r.Context(), h.database, user.ID, siteName); err == nil {
		unlock = h.lockSite(user.ID, siteName)
	}
	defer func() { unlock() }()

	d, err := db.GetDeletedSiteByUser(r.Context(), h.database, user.ID, siteName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			if _, lerr := db.GetSiteByUser(r.Context(), h.database, user.ID, siteName); lerr == nil {
				writeJSON(w, http.StatusConflict, errorResponse{Error: "that site is not deleted", Code: "site_not_deleted"})
				return
			}
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "no site with that name in Recently deleted"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	// No quota check: a site in Recently deleted already counts toward it.
	site, err := h.restoreTrashedSite(r.Context(), d, user.Handle.String, nil)
	if err != nil {
		switch {
		case errors.Is(err, sql.ErrNoRows):
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "no site with that name in Recently deleted"})
		case errors.Is(err, errRestoreFiles):
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "could not restore the site's files"})
		default:
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		}
		return
	}
	writeJSON(w, http.StatusOK, h.toSiteResponse(site, "Restored with all its versions and saved data."))
}

var errRestoreFiles = errors.New("could not restore the site's files")

// restoreTrashedSite brings a site in Recently deleted back exactly as it
// was (plus whatever also records in the same transaction). handle is the
// owner's current handle ("" if none). The caller holds the site's lock.
// sql.ErrNoRows when it is not (or no longer) in Recently deleted.
func (h *SiteHandler) restoreTrashedSite(ctx context.Context, d db.DeletedSite, handle string, also func(tx *sql.Tx) error) (db.Site, error) {
	// It comes back as it was, taken down included (the operator may have
	// taken it down while it was deleted): the marker goes into its folder
	// before the folder is served again, so it is never live without it.
	full, err := db.GetSiteByID(ctx, h.database, d.ID)
	if err != nil {
		return db.Site{}, err
	}
	if err := h.disk.SetTrashedSuspended(d.UserID, d.ID, full.Suspended()); err != nil {
		log.Printf("restore site %s: suspend marker: %v", d.ID, err)
		return db.Site{}, errRestoreFiles
	}
	tx, err := h.database.BeginTx(ctx, nil)
	if err != nil {
		return db.Site{}, err
	}
	defer tx.Rollback()
	if err := db.RestoreDeletedSite(ctx, tx, d.ID); err != nil {
		return db.Site{}, err
	}
	if also != nil {
		if err := also(tx); err != nil {
			return db.Site{}, err
		}
	}
	// The name is this site's again; an old name another site left there
	// stops redirecting.
	if err := db.DropOldSiteName(ctx, tx, d.UserID, d.Name); err != nil {
		return db.Site{}, err
	}
	if err := h.disk.RestoreSite(d.UserID, d.Name, d.ID); err != nil {
		log.Printf("restore site %s: %v", d.ID, err)
		return db.Site{}, errRestoreFiles
	}
	if err := tx.Commit(); err != nil {
		if terr := h.disk.TrashSite(d.UserID, d.Name, d.ID); terr != nil {
			log.Printf("restore site %s: commit failed and files could not be moved back: %v", d.ID, terr)
		}
		return db.Site{}, err
	}
	// The handle may have changed while the site was deleted.
	if handle != "" {
		if err := h.disk.EnsureHandleLink(handle, d.UserID); err != nil {
			log.Printf("restore site: ensure handle link %s: %v", handle, err)
		}
		h.RequestSiteCert(handle)
	}
	site, err := db.GetSiteByUser(ctx, h.database, d.UserID, d.Name)
	if err != nil {
		return db.Site{}, err
	}
	// The operator may have acted since: make the marker follow the record.
	if err := h.syncSiteMarker(site); err != nil {
		log.Printf("restore site %s: suspend marker: %v", site.ID, err)
	}
	return site, nil
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
		unlock := h.lockSite(d.UserID, d.Name)
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
			// Only while the link is still this site's: the domain may have
			// been bound to another site since this one was deleted.
			mine, err := h.disk.UnbindDomainOf(dom, d.UserID, d.Name)
			if err != nil {
				log.Printf("deleted-site purge: unbind %s: %v", dom, err)
				continue
			}
			if mine {
				h.cancelDomainCert(dom)
			}
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
