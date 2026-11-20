package handler

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"

	"github.com/vsriram/simple-host/host/model"
	appsites "github.com/vsriram/simple-host/host/sites"
)

// ExportEventProjects is called only after event/own-team authorization. It
// exports only the supplied team names from the group's holding account.
func (h *SiteHandler) ExportEventProjects(w http.ResponseWriter, r *http.Request, g GroupRef, teamSlugs []string, ownSlug string) {
	var sites []model.Site
	for _, slug := range teamSlugs {
		if ownSlug != "" && slug != ownSlug {
			continue
		}
		site, err := h.sitesSvc().GetSiteByUser(r.Context(), g.AccountID, slug)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			writeInternal(w)
			return
		}
		sites = append(sites, site)
	}
	if ownSlug != "" && len(sites) == 0 {
		writeProductErr(w, 404, "no_project", "your team has not published a project")
		return
	}
	name := g.Slug + "-projects"
	if ownSlug != "" {
		name = g.Slug + "-" + ownSlug
	}
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.tar.gz"`, name))
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	for _, site := range sites {
		files := appsites.FileVersion{UserID: site.UserID, SiteName: site.Name, Version: site.ActiveVersion}
		if err := h.writeSiteArchive(r.Context(), tarPut(tw), site.Name, site.ID, files); err != nil {
			panic(http.ErrAbortHandler)
		}
	}
	finishSiteTar(tw, gz)
}

func (h *SiteHandler) EventFileBytes(ctx context.Context, accountID string) (int64, error) {
	files, err := h.disk.AccountFileBytes(accountID)
	if err != nil {
		return 0, err
	}
	local, err := h.disk.AccountAuxiliaryBytes(ctx, accountID)
	if err != nil {
		return 0, err
	}
	return files + local, nil
}
