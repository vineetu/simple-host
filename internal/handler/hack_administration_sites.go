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

	"github.com/vsriram/simple-host/internal/db"
)

// Site-side of Hack administration exports (cleanup 3.2 slice 1c): these are
// *SiteHandler methods, split out of hack_administration.go so that file's
// *HackHandler methods can move into internal/hackhttp as one unit. They
// stay in handler because they walk the shared disk layout that SiteHandler
// already owns. hackAdministrationSites (hack_administration.go) is the
// narrow interface the moving code calls these through.

// ExportEventProjects is called only after event/own-team authorization. It
// selects this event's teams and holding account; no instance-wide export is used.
func (h *SiteHandler) ExportEventProjects(w http.ResponseWriter, r *http.Request, ev db.Event, ownSlug string) {
	teams, err := db.ListEventTeams(r.Context(), h.database, ev.ID)
	if err != nil {
		writeInternal(w)
		return
	}
	var sites []db.Site
	for _, team := range teams {
		if ownSlug != "" && team.Slug != ownSlug {
			continue
		}
		site, err := db.GetSiteByUser(r.Context(), h.database, ev.AccountID, team.Slug)
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
	name := ev.Slug + "-projects"
	if ownSlug != "" {
		name = ev.Slug + "-" + ownSlug
	}
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.tar.gz"`, name))
	gz := gzip.NewWriter(w)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()
	for _, site := range sites {
		// Walk an actual version directory: WalkDir does not follow a root symlink.
		current := h.disk.VersionDir(site.UserID, site.Name, site.ActiveVersion)
		if err := h.writeSiteArchive(r.Context(), tarPut(tw), site.Name, site.ID, current); err != nil {
			return
		}
	}
}

func (h *SiteHandler) EventFileBytes(ctx context.Context, ev db.Event) (int64, error) {
	var total int64
	for _, area := range []string{"by-id", "deleted"} {
		root := filepath.Join(h.disk.DataDir(), area, ev.AccountID)
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
