package sites

import (
	"io"

	"github.com/vsriram/simple-host/internal/tarball"
)

// Archive operations apply the site's resolved extraction budget.
var SanitizeFilePatchWithLimit = tarball.SanitizeFilePatchWithLimit
var SanitizeFilesWithLimit = tarball.SanitizeFilesWithLimit
var ValidateExtensions = tarball.ValidateExtensions
var ErrArchiveTooLarge = tarball.ErrTooLarge

func ExtractWithLimit(src io.Reader, filename string, budget int64, ceiling int) (map[string][]byte, error) {
	return tarball.ExtractWithLimit(src, filename, budget, ceiling)
}
func SetArchiveSiteLimit(bytes int64) { tarball.SetSiteLimit(bytes) }
func SnapshotArchiveLimits() func()   { return tarball.SnapshotLimits() }
