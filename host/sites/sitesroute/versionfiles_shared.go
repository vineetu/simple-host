package sitesroute

import (
	"context"
	"errors"
	"os"
	"sort"

	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sites"
)

// WalkVersionFiles lists a stored version's files (valid names, no links
// that escape), sorted by path.
func WalkVersionFiles(ctx context.Context, files sites.SiteFiles, ref sites.FileVersion) ([]VersionFile, error) {
	entries := make([]VersionFile, 0)
	err := files.WalkFiles(ctx, ref, func(file sites.SiteFile, err error) error {
		if err != nil {
			return err
		}
		// Retain the API's support for safe internal file symlinks and skip
		// invalid names and links that escape or no longer exist.
		if !sites.ValidVersionFilePath(file.Name) {
			return nil
		}
		info, err := files.StatFile(ctx, ref, file.Name)
		if errors.Is(err, sites.ErrInvalidFilePath) || os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if !info.IsDir() {
			entries = append(entries, VersionFile{Path: file.Name, Size: info.Size()})
		}
		return nil
	})
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return entries, err
}

// LiveVersionFiles lists the files of a site's live version, nil when it has
// none or they cannot be read (GET /v1/sites?files=<name>).
func LiveVersionFiles(ctx context.Context, files sites.SiteFiles, site model.Site) *[]VersionFile {
	if site.ActiveVersion < 1 {
		return nil
	}
	entries, err := WalkVersionFiles(ctx, files, sites.FileVersion{UserID: site.UserID, SiteName: site.Name, Version: site.ActiveVersion})
	if err != nil {
		return nil
	}
	return &entries
}
