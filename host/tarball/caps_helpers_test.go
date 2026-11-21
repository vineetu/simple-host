package tarball

import "io"

// Extract unpacks an archive under the instance's caps at the built-in
// entry-count ceiling; production goes through ExtractWithLimit.
func Extract(r io.Reader, filename string) (map[string][]byte, error) {
	return extract(r, filename, current(maxEntriesCeiling))
}

// SanitizeFiles is SanitizeFilesWithLimit under the instance's caps.
func SanitizeFiles(in map[string][]byte) (map[string][]byte, error) {
	return sanitize(in, current(maxEntriesCeiling), false)
}

// SiteLimitIs reports whether both extraction caps sit at bytes.
func SiteLimitIs(bytes int64) bool {
	return maxTotalUncompressedSize == bytes && maxFileSize == bytes
}
