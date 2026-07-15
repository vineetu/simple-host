package storage

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type DiskStorage struct {
	dataDir string
}

func NewDiskStorage(dataDir string) (*DiskStorage, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}

	return &DiskStorage{dataDir: dataDir}, nil
}

func (d *DiskStorage) DataDir() string {
	return d.dataDir
}

// SiteDir returns the real on-disk directory for a site under the by-id layout:
// <dataDir>/by-id/<userID>/<siteName>.
func (d *DiskStorage) SiteDir(userID, siteName string) string {
	return filepath.Join(d.dataDir, "by-id", userID, siteName)
}

// validPathKey rejects empty / "." / ".." / absolute / multi-segment path keys
// used as userID or siteName so they cannot escape the data dir.
func validPathKey(key string) bool {
	if key == "" || key == "." || key == ".." {
		return false
	}
	if strings.Contains(key, "..") {
		return false
	}
	if filepath.IsAbs(key) || strings.ContainsAny(key, `/\`) {
		return false
	}
	if !filepath.IsLocal(key) || filepath.Clean(key) != key {
		return false
	}
	return true
}

// validDomainKey is like validPathKey but allows '.' (DNS labels). Rejects
// empty, "..", absolute paths, slashes, leading dots, and non-local names so a
// custom domain cannot escape <dataDir>/domains/.
func validDomainKey(domain string) bool {
	if domain == "" || domain == "." || domain == ".." {
		return false
	}
	if strings.Contains(domain, "..") {
		return false
	}
	if strings.HasPrefix(domain, ".") {
		return false
	}
	if filepath.IsAbs(domain) || strings.ContainsAny(domain, `/\`) {
		return false
	}
	// filepath.IsLocal rejects ".." and absolute; Clean must be identity so we
	// don't accept names that collapse to something else.
	if !filepath.IsLocal(domain) || filepath.Clean(domain) != domain {
		return false
	}
	return true
}

// BindDomain creates <dataDir>/domains/<domain> -> ../by-id/<userID>/<siteName>
// (relative symlink) so Caddy can serve the bound site at the domain root.
// Idempotent when the symlink already points at the same target; errors if it
// points elsewhere (domains are globally unique).
func (d *DiskStorage) BindDomain(userID, siteName, domain string) error {
	if !validPathKey(userID) {
		return fmt.Errorf("invalid user id %q", userID)
	}
	if !validPathKey(siteName) {
		return fmt.Errorf("invalid site name %q", siteName)
	}
	if !validDomainKey(domain) {
		return fmt.Errorf("invalid domain %q", domain)
	}

	domainsDir := filepath.Join(d.dataDir, "domains")
	if err := os.MkdirAll(domainsDir, 0o755); err != nil {
		return fmt.Errorf("create domains dir: %w", err)
	}

	linkPath := filepath.Join(domainsDir, domain)
	// Relative from domains/<domain> -> by-id/<userID>/<siteName>
	target := filepath.Join("..", "by-id", userID, siteName)

	info, err := os.Lstat(linkPath)
	if err == nil {
		if info.Mode()&os.ModeSymlink == 0 {
			return fmt.Errorf("domain path %q exists and is not a symlink; refusing to clobber", linkPath)
		}
		cur, rerr := os.Readlink(linkPath)
		if rerr != nil {
			return fmt.Errorf("readlink domain path: %w", rerr)
		}
		if cur == target {
			return nil // already bound to this site
		}
		return fmt.Errorf("domain %q already bound to a different site", domain)
	}
	if !os.IsNotExist(err) {
		return fmt.Errorf("lstat domain path: %w", err)
	}

	if err := os.Symlink(target, linkPath); err != nil {
		return fmt.Errorf("create domain symlink: %w", err)
	}
	return nil
}

// UnbindDomain removes <dataDir>/domains/<domain> only if it is a symlink.
// Absent path is a no-op. Refuses to remove a real file/directory.
func (d *DiskStorage) UnbindDomain(domain string) error {
	if !validDomainKey(domain) {
		return fmt.Errorf("invalid domain %q", domain)
	}

	linkPath := filepath.Join(d.dataDir, "domains", domain)
	info, err := os.Lstat(linkPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("lstat domain path: %w", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return fmt.Errorf("domain path %q is not a symlink; refusing to remove", linkPath)
	}
	if err := os.Remove(linkPath); err != nil {
		return fmt.Errorf("remove domain symlink: %w", err)
	}
	return nil
}

func (d *DiskStorage) WriteFiles(ctx context.Context, userID, siteName string, versionNum int, files map[string][]byte) error {
	if !validPathKey(userID) {
		return fmt.Errorf("invalid user id %q", userID)
	}
	if !validPathKey(siteName) {
		return fmt.Errorf("invalid site name %q", siteName)
	}

	siteDir := d.SiteDir(userID, siteName)
	tmpDir := filepath.Join(siteDir, fmt.Sprintf(".v%d.tmp", versionNum))
	versionDir := filepath.Join(siteDir, fmt.Sprintf("v%d", versionNum))

	if err := os.MkdirAll(siteDir, 0o755); err != nil {
		return fmt.Errorf("create site dir: %w", err)
	}

	if err := os.RemoveAll(tmpDir); err != nil {
		return fmt.Errorf("remove temp dir: %w", err)
	}
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}

	for relativePath, content := range files {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		cleanPath := filepath.Clean(relativePath)
		if cleanPath == "." || cleanPath == "" || cleanPath == ".." || strings.HasPrefix(cleanPath, ".."+string(filepath.Separator)) || filepath.IsAbs(cleanPath) {
			return fmt.Errorf("invalid relative path %q", relativePath)
		}

		dstPath := filepath.Join(tmpDir, cleanPath)
		if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
			return fmt.Errorf("create parent dir for %q: %w", relativePath, err)
		}
		if err := os.WriteFile(dstPath, content, 0o644); err != nil {
			return fmt.Errorf("write %q: %w", relativePath, err)
		}
	}

	if err := os.RemoveAll(versionDir); err != nil {
		return fmt.Errorf("remove existing version dir: %w", err)
	}
	if err := os.Rename(tmpDir, versionDir); err != nil {
		return fmt.Errorf("promote temp dir: %w", err)
	}

	return nil
}

func (d *DiskStorage) UpdateCurrent(userID, siteName string, versionNum int) error {
	if !validPathKey(userID) {
		return fmt.Errorf("invalid user id %q", userID)
	}
	if !validPathKey(siteName) {
		return fmt.Errorf("invalid site name %q", siteName)
	}

	siteDir := d.SiteDir(userID, siteName)
	versionDir := filepath.Join(siteDir, fmt.Sprintf("v%d", versionNum))
	currentDir := filepath.Join(siteDir, "current")
	tmpDir := filepath.Join(siteDir, ".current.tmp")

	// Build the new tree off to the side. While this (slow) copy runs, the live
	// `current` directory keeps serving the previous version untouched.
	if err := os.RemoveAll(tmpDir); err != nil {
		return fmt.Errorf("remove temp current dir: %w", err)
	}
	if err := copyDir(versionDir, tmpDir); err != nil {
		return fmt.Errorf("copy version %d to temp current: %w", versionNum, err)
	}

	// Swap into place. The window where `current` is absent is now just a
	// remove + rename (fast metadata ops) rather than the full copy duration.
	// `current` lands as a fresh directory at the same path each deploy — the
	// same semantics the proxy already serves today.
	if err := os.RemoveAll(currentDir); err != nil {
		return fmt.Errorf("remove current dir: %w", err)
	}
	if err := os.Rename(tmpDir, currentDir); err != nil {
		return fmt.Errorf("promote temp current dir: %w", err)
	}

	// Legacy per-name subdomain (<siteName>.<domain>) is deprecated: we no longer
	// create the flat back-compat symlink at <dataDir>/<siteName>. Sites are
	// reachable only by path (sites.<domain>/<handle>/<siteName>/). DeleteSite
	// still cleans up any pre-existing back-compat symlink.

	return nil
}

// EnsureHandleLink makes handles/<handle> -> by-id/<userID> so the content host
// (sites.<domain>/<handle>/<site>/) resolves for this user's sites. Idempotent;
// never clobbers a real dir. Called on every deploy so a brand-new user's first
// upload is immediately reachable by path (no reconciler needed).
func (d *DiskStorage) EnsureHandleLink(handle, userID string) error {
	if !validPathKey(handle) {
		return fmt.Errorf("invalid handle %q", handle)
	}
	if !validPathKey(userID) {
		return fmt.Errorf("invalid user id %q", userID)
	}
	handlesDir := filepath.Join(d.dataDir, "handles")
	if err := os.MkdirAll(handlesDir, 0o755); err != nil {
		return fmt.Errorf("create handles dir: %w", err)
	}
	linkPath := filepath.Join(handlesDir, handle)
	target := filepath.Join("..", "by-id", userID) // relative from handles/<handle>

	info, err := os.Lstat(linkPath)
	if err == nil {
		if info.Mode()&os.ModeSymlink == 0 {
			return fmt.Errorf("handle path %q exists and is not a symlink; refusing to clobber", linkPath)
		}
		cur, rerr := os.Readlink(linkPath)
		if rerr != nil {
			return fmt.Errorf("readlink handle path: %w", rerr)
		}
		if cur == target {
			return nil
		}
		// Handles are unique per user_id; if it points elsewhere the handle was
		// reassigned — repoint it to the current owner.
		if err := os.Remove(linkPath); err != nil {
			return fmt.Errorf("remove stale handle link: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("lstat handle path: %w", err)
	}
	if err := os.Symlink(target, linkPath); err != nil {
		return fmt.Errorf("create handle link: %w", err)
	}
	return nil
}

// ensureBackCompatSymlink was removed: legacy per-name subdomains
// (<siteName>.<domain>) are deprecated and are no longer created on deploy. Sites
// are reachable by path only (sites.<domain>/<handle>/<siteName>/). DeleteSite
// still removes any pre-existing back-compat symlink so the tree self-cleans.

func (d *DiskStorage) DeleteSite(userID, siteName string) error {
	if !validPathKey(userID) {
		return fmt.Errorf("invalid user id %q", userID)
	}
	if !validPathKey(siteName) {
		return fmt.Errorf("invalid site name %q", siteName)
	}

	if err := os.RemoveAll(d.SiteDir(userID, siteName)); err != nil {
		return fmt.Errorf("delete site dir: %w", err)
	}

	// Remove the back-compat symlink only when it is a symlink that currently
	// points at THIS site's by-id dir. If another user's same-named site owns
	// the legacy name, leave their symlink alone. Never RemoveAll a real
	// directory/file that happens to share the site name.
	compatPath := filepath.Join(d.dataDir, siteName)
	compatTarget := filepath.Join("by-id", userID, siteName)
	info, err := os.Lstat(compatPath)
	if err == nil && info.Mode()&os.ModeSymlink != 0 {
		cur, rerr := os.Readlink(compatPath)
		if rerr != nil {
			return fmt.Errorf("readlink back-compat path: %w", rerr)
		}
		if cur == compatTarget {
			if err := os.Remove(compatPath); err != nil {
				return fmt.Errorf("remove back-compat symlink: %w", err)
			}
		}
		// else: points at another user's dir — leave it
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("lstat back-compat path: %w", err)
	}

	return nil
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}

		targetPath := filepath.Join(dst, relPath)
		info, err := entry.Info()
		if err != nil {
			return err
		}

		if entry.IsDir() {
			return os.MkdirAll(targetPath, info.Mode().Perm())
		}

		if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
			return err
		}

		srcFile, err := os.Open(path)
		if err != nil {
			return err
		}
		defer srcFile.Close()

		dstFile, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
		if err != nil {
			return err
		}

		if _, err := io.Copy(dstFile, srcFile); err != nil {
			dstFile.Close()
			return err
		}

		if err := dstFile.Close(); err != nil {
			return err
		}

		return nil
	})
}
