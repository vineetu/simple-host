package storage

import (
	"fmt"
	"os"
	"path/filepath"
)

// Recently deleted sites (completeness plan, 2026-09-27).
//
// A deleted site's directory moves from by-id/<userID>/<site> to
// deleted/<userID>/<siteID>. Nothing serves from deleted/ (nginx and Caddy
// reach sites only through handles/ -> by-id/ and domains/ -> by-id/), so the
// move alone takes the site offline at once, and moving it back brings it back
// with every version intact. Keyed by site id so the folder never depends on
// the name.

// TrashDir is where a deleted site's files wait out the restore window.
func (d *DiskStorage) TrashDir(userID, siteID string) string {
	return filepath.Join(d.dataDir, "deleted", userID, siteID)
}

// TrashSite moves a site's files to the deleted area. A site with no files on
// disk (never promoted) is fine.
func (d *DiskStorage) TrashSite(userID, siteName, siteID string) error {
	if !validPathKey(userID) || !validPathKey(siteName) || !validPathKey(siteID) {
		return fmt.Errorf("invalid site path")
	}
	src := d.SiteDir(userID, siteName)
	if _, err := os.Lstat(src); os.IsNotExist(err) {
		return nil
	}
	dst := d.TrashDir(userID, siteID)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("create deleted dir: %w", err)
	}
	if err := os.RemoveAll(dst); err != nil {
		return fmt.Errorf("clear deleted dir: %w", err)
	}
	if err := os.Rename(src, dst); err != nil {
		return fmt.Errorf("move site to deleted: %w", err)
	}
	return nil
}

// RestoreSite moves a deleted site's files back to where they are served.
// Refuses to clobber an existing site directory.
func (d *DiskStorage) RestoreSite(userID, siteName, siteID string) error {
	if !validPathKey(userID) || !validPathKey(siteName) || !validPathKey(siteID) {
		return fmt.Errorf("invalid site path")
	}
	src := d.TrashDir(userID, siteID)
	if _, err := os.Lstat(src); os.IsNotExist(err) {
		return nil
	}
	dst := d.SiteDir(userID, siteName)
	if _, err := os.Lstat(dst); err == nil {
		return fmt.Errorf("site directory %q already exists", dst)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("create user dir: %w", err)
	}
	if err := os.Rename(src, dst); err != nil {
		return fmt.Errorf("move site back: %w", err)
	}
	return nil
}

// PurgeTrashedSite removes a deleted site's files for good. Missing is fine.
func (d *DiskStorage) PurgeTrashedSite(userID, siteID string) error {
	if !validPathKey(userID) || !validPathKey(siteID) {
		return fmt.Errorf("invalid site path")
	}
	return os.RemoveAll(d.TrashDir(userID, siteID))
}

// PurgeTrashedSiteLinks removes what still names a purged site outside its
// folder: the legacy <dataDir>/<siteName> link and, when given, the
// domains/<domain> link — each only while it points at this site.
func (d *DiskStorage) PurgeTrashedSiteLinks(userID, siteName, domain string) error {
	if !validPathKey(userID) || !validPathKey(siteName) {
		return fmt.Errorf("invalid site path")
	}
	if err := d.removeCompatLink(userID, siteName); err != nil {
		return err
	}
	if domain == "" || !validDomainKey(domain) {
		return nil
	}
	link := filepath.Join(d.dataDir, "domains", domain)
	if cur, err := os.Readlink(link); err == nil && cur == filepath.Join("..", "by-id", userID, siteName) {
		return os.Remove(link)
	}
	return nil
}
