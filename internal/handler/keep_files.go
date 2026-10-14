package handler

import (
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"

	"github.com/vsriram/simple-host/internal/auth"
	"github.com/vsriram/simple-host/internal/tarball"
)

func (h *SiteHandler) keepSiteFiles(r *http.Request, siteName string, files map[string][]byte, remove []string, budget int64) error {
	totalLimit, fileLimit, entryLimit := tarball.FileLimits(budget)
	removed, err := normalizeFilePatch(files, remove)
	if err != nil {
		return err
	}
	site, err := h.siteForCaller(r, auth.GetUser(r.Context()).ID, siteName)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if site.ActiveVersion < 1 {
		return nil
	}
	dir := h.disk.VersionDir(site.UserID, site.Name, site.ActiveVersion)
	var total int64
	for _, b := range files {
		total += int64(len(b))
	}
	err = filepath.WalkDir(dir, func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return errors.New("live version contains a non-regular file")
		}
		rel, err := filepath.Rel(dir, name)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if removed[rel] {
			return nil
		}
		if _, ok := files[rel]; ok {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		if total > totalLimit || info.Size() > fileLimit || len(files)+1 > entryLimit {
			return fmt.Errorf("%w: merged files exceed the site limit", tarball.ErrTooLarge)
		}
		body, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		files[rel] = body
		return nil
	})
	if err != nil {
		return err
	}
	for p := range removed {
		delete(files, p)
	}
	return nil
}

func normalizeFilePatch(files map[string][]byte, remove []string) (map[string]bool, error) {
	removed := map[string]bool{}
	for _, raw := range remove {
		clean, err := importPath(raw)
		if err != nil || clean == "" {
			return nil, errors.New("unsafe path in remove_files")
		}
		removed[clean] = true
	}
	// Normalize the patch before merging so ./about.html replaces about.html.
	patch := map[string][]byte{}
	for raw, b := range files {
		clean, err := importPath(raw)
		if err != nil || clean == "" {
			return nil, errors.New("unsafe file path")
		}
		if _, ok := patch[clean]; ok {
			return nil, errors.New("duplicate file path")
		}
		patch[clean] = b
		delete(files, raw)
	}
	for p, b := range patch {
		files[p] = b
	}
	return removed, nil
}
