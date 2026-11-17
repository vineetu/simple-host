package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// ErrInvalidFilePath identifies invalid or escaping deploy-content paths.
var ErrInvalidFilePath = errors.New("invalid path")

func (d *DiskStorage) fileVersionDir(v FileVersion) (string, error) {
	if !validPathKey(v.UserID) || !validPathKey(v.SiteName) || v.Version < 0 || (v.TrashID != "" && !validPathKey(v.TrashID)) {
		return "", os.ErrNotExist
	}
	dir := d.SiteDir(v.UserID, v.SiteName)
	if v.TrashID != "" {
		dir = d.TrashDir(v.UserID, v.TrashID)
	}
	name := "current"
	if v.Version > 0 {
		name = fmt.Sprintf("v%d", v.Version)
	}
	dir = filepath.Join(dir, name)
	st, err := os.Lstat(dir)
	if err != nil {
		return "", err
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return "", os.ErrNotExist
	}
	return dir, nil
}

// filePathWithin preserves retained-version reads: internal symlinks are allowed,
// but a path resolving outside its version is refused.
func filePathWithin(dir, path string) (string, error) {
	if strings.HasPrefix(path, "/") || strings.ContainsRune(path, '\x00') {
		return "", ErrInvalidFilePath
	}
	for _, segment := range strings.Split(path, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return "", ErrInvalidFilePath
		}
	}
	root, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	target := filepath.Join(root, filepath.Clean(path))
	inside := func(root, target string) bool {
		rel, err := filepath.Rel(root, target)
		return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
	}
	if !inside(root, target) {
		return "", ErrInvalidFilePath
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(target)
	if err != nil {
		return "", err
	}
	if !inside(root, resolved) {
		return "", ErrInvalidFilePath
	}
	return resolved, nil
}

// openValidatedFile confines the final lookup even if a validated file or its
// parent is replaced with a symlink. Opening the resolved target relative to
// root also preserves absolute symlinks whose targets are inside the version.
func openValidatedFile(root *os.File, path string) (*os.File, error) {
	dir, err := filepath.Abs(root.Name())
	if err != nil {
		return nil, err
	}
	dir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		return nil, err
	}
	name, err := filepath.Rel(dir, path)
	if err != nil {
		return nil, err
	}
	if !filepath.IsLocal(name) {
		return nil, ErrInvalidFilePath
	}
	// Validation has already resolved symlinks. Refuse replacements while
	// walking from the held directory, with search-only handles for parents.
	// No lookup can escape through a symlink swapped in after validation.
	parent := root
	defer func() {
		if parent != root {
			parent.Close()
		}
	}()
	parts := strings.Split(name, string(filepath.Separator))
	for i, part := range parts {
		flags := unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NOFOLLOW
		if i < len(parts)-1 {
			flags = searchDirectoryFlags | unix.O_CLOEXEC | unix.O_NOFOLLOW | unix.O_DIRECTORY
		}
		fd, err := unix.Openat(int(parent.Fd()), part, flags, 0)
		if err != nil {
			return nil, &os.PathError{Op: "openat", Path: name, Err: err}
		}
		f := os.NewFile(uintptr(fd), part)
		if i == len(parts)-1 {
			return f, nil
		}
		if parent != root {
			parent.Close()
		}
		parent = f
	}
	panic("empty validated path")
}

func openFileRoot(dir string) (*os.File, error) {
	fd, err := unix.Open(dir, searchDirectoryFlags|unix.O_CLOEXEC|unix.O_DIRECTORY, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: dir, Err: err}
	}
	return os.NewFile(uintptr(fd), dir), nil
}

func (d *DiskStorage) OpenFile(ctx context.Context, v FileVersion, name string) (io.ReadCloser, fs.FileInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	dir, err := d.fileVersionDir(v)
	if err != nil {
		return nil, nil, err
	}
	var f *os.File
	if v.Version == 0 {
		// Live page readers historically used Root.Open on the original name.
		// Keep component resolution (including symlink/.. ordering) unchanged.
		root, openErr := os.OpenRoot(dir)
		if openErr != nil {
			return nil, nil, openErr
		}
		defer root.Close()
		f, err = root.Open(name)
	} else {
		f, err = openRetainedFile(dir, name)
	}
	if err != nil {
		return nil, nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	return f, info, nil
}

func openRetainedFile(dir, name string) (*os.File, error) {
	root, err := openFileRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	path, err := filePathWithin(dir, name)
	if err != nil {
		return nil, err
	}
	return openValidatedFile(root, path)
}

func (d *DiskStorage) ReadFile(ctx context.Context, v FileVersion, name string) ([]byte, error) {
	f, _, err := d.OpenFile(ctx, v, name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

func (d *DiskStorage) StatVersion(ctx context.Context, v FileVersion) (fs.FileInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	dir, err := d.fileVersionDir(v)
	if err != nil {
		return nil, err
	}
	return os.Lstat(dir)
}

// StatFile resolves safe internal symlinks without opening file contents.
func (d *DiskStorage) StatFile(ctx context.Context, v FileVersion, name string) (fs.FileInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	dir, err := d.fileVersionDir(v)
	if err != nil {
		return nil, err
	}
	path, err := filePathWithin(dir, name)
	if err != nil {
		return nil, err
	}
	return os.Stat(path)
}

type lazyFileReader struct {
	path string
	file *os.File
}

func (r *lazyFileReader) Read(p []byte) (int, error) {
	if r.file == nil {
		f, err := os.Open(r.path)
		if err != nil {
			return 0, err
		}
		r.file = f
	}
	return r.file.Read(p)
}

func (r *lazyFileReader) close() {
	if r.file != nil {
		r.file.Close()
	}
}

func (d *DiskStorage) WalkFiles(ctx context.Context, v FileVersion, visit func(SiteFile, error) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	dir, err := d.fileVersionDir(v)
	if err != nil {
		return visit(SiteFile{}, err)
	}
	return filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err != nil {
			return visit(SiteFile{}, err)
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return visit(SiteFile{}, err)
		}
		file := SiteFile{Name: filepath.ToSlash(rel), Mode: entry.Type()}
		info, err := entry.Info()
		if err != nil {
			return visit(file, err)
		}
		file.Size, file.ModTime, file.Mode = info.Size(), info.ModTime(), info.Mode()
		if !info.Mode().IsRegular() {
			return visit(file, nil)
		}
		reader := &lazyFileReader{path: path}
		defer reader.close()
		file.Reader = reader
		return visit(file, nil)
	})
}

func (d *DiskStorage) ListVersions(userID, siteName string) ([]int, error) {
	if !validPathKey(userID) || !validPathKey(siteName) {
		return nil, os.ErrNotExist
	}
	versions := make([]int, 0)
	entries, err := os.ReadDir(d.SiteDir(userID, siteName))
	if os.IsNotExist(err) {
		return versions, nil
	}
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() || len(name) < 2 || name[0] != 'v' {
			continue
		}
		n, err := strconv.Atoi(name[1:])
		if err == nil && n > 0 {
			versions = append(versions, n)
		}
	}
	sort.Ints(versions)
	return versions, nil
}

func (d *DiskStorage) ReadTrashedRedirects(userID, siteID string) ([]byte, error) {
	return os.ReadFile(filepath.Join(d.TrashDir(userID, siteID), "redirects"))
}

// AccountAuxiliaryBytes keeps the product's physical-byte counter unchanged:
// unlike deploy quotas, that counter includes runtime resources and markers.
// Exclude deploy subtrees so callers can add their SiteFiles total.
func (d *DiskStorage) AccountAuxiliaryBytes(ctx context.Context, userID string) (int64, error) {
	var total int64
	for _, area := range []string{"by-id", "deleted"} {
		root := filepath.Join(d.dataDir, area, userID)
		err := filepath.WalkDir(root, func(path string, e fs.DirEntry, err error) error {
			if os.IsNotExist(err) {
				return nil
			}
			if err != nil {
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			parts := strings.Split(rel, string(filepath.Separator))
			if e.IsDir() && len(parts) == 2 {
				name := parts[1]
				if name == "current" {
					return filepath.SkipDir
				}
				if len(name) > 1 && name[0] == 'v' {
					if n, err := strconv.Atoi(name[1:]); err == nil && n > 0 {
						return filepath.SkipDir
					}
				}
			}
			if e.Type().IsRegular() {
				info, err := e.Info()
				if err != nil {
					return err
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
