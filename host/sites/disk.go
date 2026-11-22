package sites

import (
	"strings"

	"github.com/vsriram/simple-host/host/storage"
)

// Disk combines deploy contents and the local serving/lifecycle capabilities.
// Content operations use SiteFiles; local paths are only for serving and runtime.
type Disk interface {
	storage.SiteFiles
	storage.LocalServing
}

// Re-export the deploy contracts at the application boundary. HTTP transports
// depend on app/sites, not directly on the storage mechanism package.
type SiteFiles = storage.SiteFiles
type LocalServing = storage.LocalServing
type FileVersion = storage.FileVersion
type SiteFile = storage.SiteFile

var ErrInvalidFilePath = storage.ErrInvalidFilePath

// ValidVersionFilePath: path names a file inside a version (no empty, . or
// .. segment, no backslash or NUL).
func ValidVersionFilePath(path string) bool {
	if strings.ContainsAny(path, "\\\x00") {
		return false
	}
	for _, segment := range strings.Split(path, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}
