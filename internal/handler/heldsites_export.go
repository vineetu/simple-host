package handler

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"

	"github.com/vsriram/simple-host/internal/model"
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
		writeHackErr(w, 404, "no_project", "your team has not published a project")
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
		// Walk an actual version directory: WalkDir does not follow a root symlink.
		current := h.disk.VersionDir(site.UserID, site.Name, site.ActiveVersion)
		if err := h.writeSiteArchive(r.Context(), tarPut(tw), site.Name, site.ID, current); err != nil {
			panic(http.ErrAbortHandler)
		}
	}
	finishSiteTar(tw, gz)
}

func (h *SiteHandler) EventFileBytes(ctx context.Context, accountID string) (int64, error) {
	var total int64
	for _, area := range []string{"by-id", "deleted"} {
		root := filepath.Join(h.disk.DataDir(), area, accountID)
		err := filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			if err != nil {
				return err
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if d.Type().IsRegular() {
				info, e := d.Info()
				if e != nil {
					return e
				}
				total += info.Size()
			}
			return nil
		})
		if err != nil {
			return 0, err
		}
	}
	return total, nil
}
