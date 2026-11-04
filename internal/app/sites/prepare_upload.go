package sites

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"sort"
	"strings"

	"github.com/vsriram/simple-host/internal/tarball"
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
// Patch preparation deliberately omits the full-site index requirement.
func (s *Service) PrepareFiles(in FilePreparationInput) (map[string][]byte, string, error) {
	var budget int64
	if in.Override {
		budget = in.Limit
	}
	var clean map[string][]byte
	var err error
	if in.Patch {
		clean, err = tarball.SanitizeFilePatchWithLimit(in.Files, budget, s.deps.MaxFilesPerSite)
	} else {
		clean, err = tarball.SanitizeFilesWithLimit(in.Files, budget, s.deps.MaxFilesPerSite)
	}
	if err != nil {
		return nil, "", uploadFailure("invalid_files", err)
	}
	if err := tarball.ValidateExtensions(clean); err != nil {
		return nil, "", uploadFailure("blocked_file_type", err)
	}
	if in.Patch {
		return clean, "", nil
	}
	return clean, digestFiles(clean), nil
}

// ArchivePreparationInput contains the bounded upload bytes and declared integrity header.
type ArchivePreparationInput struct {
	Body                    []byte
	Filename, ContentDigest string
	Limit                   int64
	Override, Strict        bool
}

// PrepareArchive verifies integrity before extraction, then checks the root and file types in order.
func (s *Service) PrepareArchive(in ArchivePreparationInput) (map[string][]byte, string, error) {
	sum := sha256.Sum256(in.Body)
	digest := hex.EncodeToString(sum[:])
	if hdr := strings.TrimSpace(in.ContentDigest); hdr != "" {
		want := strings.ToLower(strings.TrimPrefix(hdr, "sha256="))
		if subtle.ConstantTimeCompare([]byte(want), []byte(digest)) != 1 {
			return nil, "", uploadFailure("digest_mismatch", errors.New("archive digest mismatch"))
		}
	}
	var budget int64
	if in.Override {
		budget = in.Limit
	}
	files, err := tarball.ExtractWithLimit(bytes.NewReader(in.Body), in.Filename, budget, s.deps.MaxFilesPerSite)
	if err != nil {
		return nil, "", uploadFailure("invalid_archive", err)
	}
	if in.Strict {
		if _, ok := files["index.html"]; !ok {
			return nil, "", uploadFailure("missing_index", errors.New("no index.html at the root of the archive"))
		}
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
