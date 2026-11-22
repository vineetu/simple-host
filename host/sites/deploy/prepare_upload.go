package deploy

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"sort"
	"strings"

	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/tarball"
)

// UploadError identifies the preparation stage so transports preserve their response contracts.
type UploadError struct {
	Code     string
	TooLarge bool
	Cause    error
}

func (e *UploadError) Error() string { return e.Cause.Error() }
func (e *UploadError) Unwrap() error { return e.Cause }
func uploadFailure(code string, err error) error {
	return &UploadError{Code: code, TooLarge: errors.Is(err, tarball.ErrTooLarge), Cause: err}
}

// FilePreparationInput holds decoded files and the account's resolved upload allowance.
type FilePreparationInput struct {
	Files    map[string][]byte
	Limit    int64
	Override bool
	Patch    bool
}

// PrepareFiles chooses the extraction budget and applies path, size, secret and extension guards.
// A patch (keep=1, checked before the deploy locks) may come out empty.
func (s Service) PrepareFiles(in FilePreparationInput) (map[string][]byte, string, error) {
	sanitize := tarball.SanitizeFilesWithLimit
	if in.Patch {
		sanitize = tarball.SanitizeFilePatchWithLimit
	}
	clean, err := sanitize(in.Files, sites.ExtractBudget(in.Limit, in.Override), s.Deps.MaxFilesPerSite)
	if err != nil {
		return nil, "", uploadFailure("invalid_files", err)
	}
	if err := tarball.ValidateExtensions(clean); err != nil {
		return nil, "", uploadFailure("blocked_file_type", err)
	}
	return clean, digestFiles(clean), nil
}

// ArchivePreparationInput contains the bounded upload bytes and declared integrity header.
type ArchivePreparationInput struct {
	Body                    []byte
	Filename, ContentDigest string
	Limit                   int64
	Override                bool
}

// PrepareArchive verifies integrity before extraction, then checks the root and file types in order.
func (s Service) PrepareArchive(in ArchivePreparationInput) (map[string][]byte, string, error) {
	sum := sha256.Sum256(in.Body)
	digest := hex.EncodeToString(sum[:])
	if hdr := strings.TrimSpace(in.ContentDigest); hdr != "" {
		want := strings.ToLower(strings.TrimPrefix(hdr, "sha256="))
		if subtle.ConstantTimeCompare([]byte(want), []byte(digest)) != 1 {
			return nil, "", uploadFailure("digest_mismatch", errors.New("archive digest mismatch"))
		}
	}
	budget := sites.ExtractBudget(in.Limit, in.Override)
	files, err := tarball.ExtractWithLimit(bytes.NewReader(in.Body), in.Filename, budget, s.Deps.MaxFilesPerSite)
	if err != nil {
		return nil, "", uploadFailure("invalid_archive", err)
	}
	if err := tarball.ValidateExtensions(files); err != nil {
		return nil, "", uploadFailure("blocked_file_type", err)
	}
	return files, digest, nil
}

func digestFiles(files map[string][]byte) string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)

	sum := sha256.New()
	for _, name := range names {
		sum.Write([]byte(name))
		sum.Write([]byte{0})
		sum.Write(files[name])
		sum.Write([]byte{0})
	}
	return hex.EncodeToString(sum.Sum(nil))
}

// normalizeFilePatch cleans a keep=1 update's paths with the one path rule
// (tarball.SafeRelPath) so ./about.html replaces about.html.
func normalizeFilePatch(files map[string][]byte, remove []string) (map[string]bool, error) {
	removed := map[string]bool{}
	for _, raw := range remove {
		clean, ok := tarball.SafeRelPath(raw)
		if !ok {
			return nil, errors.New("unsafe path in remove_files")
		}
		removed[clean] = true
	}
	// Normalize the patch before merging so ./about.html replaces about.html.
	patch := map[string][]byte{}
	for raw, b := range files {
		clean, ok := tarball.SafeRelPath(raw)
		if !ok {
			return nil, errors.New("unsafe file path")
		}
		if _, ok := patch[clean]; ok {
			return nil, errors.New("duplicate file path")
		}
		patch[clean] = b
		delete(files, raw)
	}
	for p, b := range patch {
		files[p] = b
	}
	return removed, nil
}
