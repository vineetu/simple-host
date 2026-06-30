package tarball

import "fmt"

// SanitizeFiles validates and normalizes a caller-supplied set of files
// (relative path -> contents) using the SAME guards as archive extraction, so
// the JSON deploy path (POST/PUT /v1/sites/{name}/files) inherits every defense
// with no parallel logic to drift.
//
// Differences from extraction, by design:
//   - An unsafe path is a HARD error (the caller typed the path explicitly,
//     rather than it coming from an opaque archive), so we reject instead of
//     skipping — surfacing the mistake.
//   - Secret/credential files (.env, .git/*, id_rsa, …) and junk (.DS_Store,
//     ._*) are silently dropped, identical to extraction.
//
// It enforces the same per-file, total-size, and entry-count caps as extraction.
func SanitizeFiles(in map[string][]byte) (map[string][]byte, error) {
	out := make(map[string][]byte, len(in))
	var total int64
	var count int

	for rawPath, content := range in {
		clean, ok := safeRelPath(rawPath)
		if !ok {
			return nil, fmt.Errorf("unsafe path %q", rawPath)
		}
		if shouldSkip(clean) || isSecretPath(clean) {
			continue
		}
		if _, dup := out[clean]; dup {
			// Two distinct input keys normalized to the same path (e.g. "./a"
			// and "a") — reject rather than silently clobber one.
			return nil, fmt.Errorf("duplicate path %q after normalization", clean)
		}

		if int64(len(content)) > maxFileSize {
			return nil, fmt.Errorf("file %q exceeds %dMB limit", clean, maxFileSize/(1024*1024))
		}
		total += int64(len(content))
		if total > maxTotalUncompressedSize {
			return nil, fmt.Errorf("files exceed %dMB total limit", maxTotalUncompressedSize/(1024*1024))
		}
		count++
		if count > maxEntryCount {
			return nil, fmt.Errorf("exceeds %d file limit", maxEntryCount)
		}

		out[clean] = content
	}

	if len(out) == 0 {
		return nil, fmt.Errorf("no usable files after sanitization")
	}
	return out, nil
}
