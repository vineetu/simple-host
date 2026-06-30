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

func (d *DiskStorage) WriteFiles(ctx context.Context, siteName string, versionNum int, files map[string][]byte) error {
	siteDir := filepath.Join(d.dataDir, siteName)
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

func (d *DiskStorage) UpdateCurrent(siteName string, versionNum int) error {
	siteDir := filepath.Join(d.dataDir, siteName)
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

	return nil
}

func (d *DiskStorage) DeleteSite(siteName string) error {
	if siteName == "" || siteName == "." || !filepath.IsLocal(siteName) || filepath.Clean(siteName) != siteName || strings.ContainsAny(siteName, `/\`) {
		return fmt.Errorf("invalid site name %q", siteName)
	}

	if err := os.RemoveAll(filepath.Join(d.dataDir, siteName)); err != nil {
		return fmt.Errorf("delete site dir: %w", err)
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
