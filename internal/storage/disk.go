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

	// Back-compat symlink for the legacy edge that serves
	// <dataDir>/<siteName>/current. Relative target keeps the tree relocatable.
	if err := d.ensureBackCompatSymlink(userID, siteName); err != nil {
		return err
	}

	return nil
}

// ensureBackCompatSymlink creates <dataDir>/<siteName> -> by-id/<userID>/<siteName>
// only if the path does not already exist (first-owner-wins). A later same-named
// site from a different user must never hijack the legacy flat symlink.
// Never clobbers a real directory or file at that path.
func (d *DiskStorage) ensureBackCompatSymlink(userID, siteName string) error {
	compatPath := filepath.Join(d.dataDir, siteName)
	// Relative target from dataDir so the whole tree stays relocatable.
	compatTarget := filepath.Join("by-id", userID, siteName)

	info, err := os.Lstat(compatPath)
	if err == nil {
		if info.Mode()&os.ModeSymlink == 0 {
			return fmt.Errorf("back-compat path %q exists and is not a symlink; refusing to clobber", compatPath)
		}
		// Existing symlink: leave it. Idempotent no-op if it already points at
		// us; if it points elsewhere another user owns the legacy name.
		cur, rerr := os.Readlink(compatPath)
		if rerr != nil {
			return fmt.Errorf("readlink back-compat path: %w", rerr)
		}
		if cur == compatTarget {
			return nil
		}
		// Different owner — do not repoint / hijack.
		return nil
	}
	if !os.IsNotExist(err) {
		return fmt.Errorf("lstat back-compat path: %w", err)
	}

	// Not exist: this owner is first for the name.
	if err := os.Symlink(compatTarget, compatPath); err != nil {
		return fmt.Errorf("create back-compat symlink: %w", err)
	}
	return nil
}

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
