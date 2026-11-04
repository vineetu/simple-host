package storage

import (
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// DetectContentType identifies the content type of file data.
func DetectContentType(data []byte) string {
	return http.DetectContentType(data)
}

// ValidObjectPath provides the existing runtime storage helper.
func ValidObjectPath(p string) bool {
	if p == "" || strings.HasPrefix(p, "/") || strings.Contains(p, "\\") || strings.ContainsRune(p, 0) || len(p) > 1024 || path.Clean(p) != p || len(strings.Split(p, "/")) > 16 {
		return false
	}
	for _, s := range strings.Split(p, "/") {
		if s == "" || s == "." || s == ".." || strings.HasPrefix(s, ".") {
			return false
		}
	}
	return true
}

// RuntimeFilesDir returns the directory for a files resource.
func RuntimeFilesDir(disk interface{ SiteDir(string, string) string }, ownerID, siteName, resourceName string) string {
	return filepath.Join(RuntimeDir(disk, ownerID, siteName), "files", resourceName)
}

// RuntimeFilePath returns the path of a runtime file.
func RuntimeFilePath(disk interface{ SiteDir(string, string) string }, ownerID, siteName, resourceName, p string) string {
	return filepath.Join(RuntimeFilesDir(disk, ownerID, siteName, resourceName), filepath.FromSlash(p))
}

// RuntimeDir returns a site's runtime directory.
func RuntimeDir(disk interface{ SiteDir(string, string) string }, ownerID, siteName string) string {
	return filepath.Join(disk.SiteDir(ownerID, siteName), "runtime")
}

// RuntimeSQLitePath returns a resource's SQLite path.
func RuntimeSQLitePath(disk interface{ SiteDir(string, string) string }, ownerID, siteName, resourceName string) string {
	return filepath.Join(RuntimeDir(disk, ownerID, siteName), "sqlite", resourceName+".sqlite")
}

// StageRuntime moves a resource aside until its declaration is deleted.
func StageRuntime(disk interface{ SiteDir(string, string) string }, ownerID, siteName, resourceName, kind string) (staged string, restore func(), err error) {
	if kind == "kv" {
		return "", func() {}, nil
	}
	root := RuntimeDir(disk, ownerID, siteName)
	if err = os.MkdirAll(root, 0700); err != nil {
		return "", nil, err
	}
	staged, err = os.MkdirTemp(root, ".deleting-")
	if err != nil {
		return "", nil, err
	}
	type move struct{ from, to string }
	moved := []move{}
	restore = func() {
		for i := len(moved) - 1; i >= 0; i-- {
			_ = os.Rename(moved[i].to, moved[i].from)
		}
		_ = os.RemoveAll(staged)
	}
	var paths []string
	if kind == "sqlite" {
		p := RuntimeSQLitePath(disk, ownerID, siteName, resourceName)
		paths = []string{p, p + "-wal", p + "-shm"}
	} else {
		paths = []string{RuntimeFilesDir(disk, ownerID, siteName, resourceName)}
	}
	for i, p := range paths {
		if _, e := os.Lstat(p); os.IsNotExist(e) {
			continue
		} else if e != nil {
			restore()
			return "", nil, e
		}
		dst := filepath.Join(staged, fmt.Sprintf("%d", i))
		if e := os.Rename(p, dst); e != nil {
			restore()
			return "", nil, e
		}
		moved = append(moved, move{p, dst})
	}
	return staged, restore, nil
}

// StageFile writes and synchronizes a temporary upload beside its destination.
// The caller removes the returned path after publishing or on failure.
func StageFile(dest string, body []byte) (string, error) {
	if err := os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(filepath.Dir(dest), ".upload-")
	if err != nil {
		return "", err
	}
	name := f.Name()
	if _, err = f.Write(body); err == nil {
		err = f.Sync()
	}
	if e := f.Close(); err == nil {
		err = e
	}
	if err != nil {
		_ = os.Remove(name)
		return "", err
	}
	return name, nil
}
