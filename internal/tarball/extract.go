package tarball

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"path/filepath"
	"strings"
)

const (
	// maxTotalUncompressedSize caps the sum of all extracted file contents.
	maxTotalUncompressedSize = 500 * 1024 * 1024
	// maxFileSize caps any single extracted file.
	maxFileSize = 100 * 1024 * 1024
	// maxEntryCount caps the number of files in an archive (defends against
	// many-tiny-files inode exhaustion, which the byte caps do not catch).
	maxEntryCount = 50_000
	// maxPathDepth / maxPathLen bound pathological directory nesting and names.
	maxPathDepth = 32
	maxPathLen   = 1024
)

func Extract(r io.Reader, filename string) (map[string][]byte, error) {
	switch {
	case strings.HasSuffix(strings.ToLower(filename), ".tar.gz"):
		return extractTarGz(r)
	case strings.HasSuffix(strings.ToLower(filename), ".zip"):
		return extractZip(r)
	default:
		return nil, fmt.Errorf("unsupported archive format: %s", filepath.Ext(filename))
	}
}

func extractTarGz(r io.Reader) (map[string][]byte, error) {
	gzipReader, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("create gzip reader: %w", err)
	}
	defer gzipReader.Close()

	tarReader := tar.NewReader(gzipReader)
	files := make(map[string][]byte)
	var totalSize int64
	var count int

	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			return files, nil
		}
		if err != nil {
			return nil, fmt.Errorf("read tar entry: %w", err)
		}

		// Only ever extract regular files. Directories are recreated implicitly
		// on write; symlinks, hardlinks, device nodes and FIFOs are refused
		// here rather than silently coerced into empty files downstream.
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			continue
		}

		clean, ok := safeRelPath(header.Name)
		if !ok {
			return nil, fmt.Errorf("unsafe path %q in archive", header.Name)
		}
		if shouldSkip(clean) || isSecretPath(clean) {
			continue
		}
		if _, dup := files[clean]; dup {
			return nil, fmt.Errorf("duplicate entry %q in archive", clean)
		}

		count++
		if count > maxEntryCount {
			return nil, fmt.Errorf("archive exceeds %d entry limit", maxEntryCount)
		}

		content, err := readCapped(tarReader, totalSize)
		if err != nil {
			return nil, fmt.Errorf("read tar entry %q: %w", clean, err)
		}
		totalSize += int64(len(content))
		files[clean] = content
	}
}

func extractZip(r io.Reader) (map[string][]byte, error) {
	archiveBytes, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read zip archive: %w", err)
	}

	zipReader, err := zip.NewReader(bytes.NewReader(archiveBytes), int64(len(archiveBytes)))
	if err != nil {
		return nil, fmt.Errorf("open zip archive: %w", err)
	}

	files := make(map[string][]byte)
	var totalSize int64
	var count int

	for _, file := range zipReader.File {
		// Regular files only — skips directories and, crucially, symlink
		// entries (which carry a non-regular mode bit).
		if !file.Mode().IsRegular() {
			continue
		}

		clean, ok := safeRelPath(file.Name)
		if !ok {
			return nil, fmt.Errorf("unsafe path %q in archive", file.Name)
		}
		if shouldSkip(clean) || isSecretPath(clean) {
			continue
		}
		if _, dup := files[clean]; dup {
			return nil, fmt.Errorf("duplicate entry %q in archive", clean)
		}

		count++
		if count > maxEntryCount {
			return nil, fmt.Errorf("archive exceeds %d entry limit", maxEntryCount)
		}

		reader, err := file.Open()
		if err != nil {
			return nil, fmt.Errorf("open zip entry %q: %w", clean, err)
		}
		// Bound by bytes ACTUALLY read, never the zip's self-declared
		// UncompressedSize64 (attacker-controlled — a decompression-bomb vector).
		content, readErr := readCapped(reader, totalSize)
		closeErr := reader.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read zip entry %q: %w", clean, readErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("close zip entry %q: %w", clean, closeErr)
		}
		totalSize += int64(len(content))
		files[clean] = content
	}

	return files, nil
}

// readCapped reads at most the smaller of maxFileSize and the remaining total
// budget, plus one byte to detect overflow. It never trusts any self-declared
// entry size — the limit is enforced on bytes actually read.
func readCapped(r io.Reader, alreadyRead int64) ([]byte, error) {
	limit := int64(maxFileSize)
	if remaining := maxTotalUncompressedSize - alreadyRead; remaining < limit {
		limit = remaining
	}
	if limit < 0 {
		limit = 0
	}

	content, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(content)) > limit {
		return nil, fmt.Errorf("archive exceeds size limit (max %dMB per file, %dMB total)",
			maxFileSize/(1024*1024), maxTotalUncompressedSize/(1024*1024))
	}
	return content, nil
}

// safeRelPath normalizes an archive entry name and rejects anything that could
// escape the destination root or is otherwise pathological. It is the single
// traversal guard for extraction; storage.WriteFiles re-checks independently.
func safeRelPath(name string) (string, bool) {
	name = normalizePath(name)
	if name == "" || len(name) > maxPathLen {
		return "", false
	}
	for i := 0; i < len(name); i++ {
		if name[i] < 0x20 { // control chars, incl. NUL / newline
			return "", false
		}
	}
	if filepath.IsAbs(name) {
		return "", false
	}
	clean := filepath.Clean(name)
	if clean == "." || clean == ".." ||
		strings.HasPrefix(clean, "../") || strings.Contains(clean, "/../") {
		return "", false
	}
	if strings.Count(clean, "/")+1 > maxPathDepth {
		return "", false
	}
	return clean, true
}

// isSecretPath reports whether a path is a credential/VCS file that must never
// be published. These are skipped (like .DS_Store), not hard-rejected, so a
// deploy that happens to include one still succeeds — minus the secret.
func isSecretPath(clean string) bool {
	parts := strings.Split(clean, "/")
	for _, dir := range parts[:len(parts)-1] {
		switch dir {
		case ".git", ".hg", ".svn", ".bzr", ".ssh":
			return true
		}
	}

	base := parts[len(parts)-1]
	switch base {
	case ".htpasswd", ".htaccess", ".npmrc", ".netrc", "id_rsa", "id_ed25519", ".git-credentials":
		return true
	case ".env":
		return true
	}
	if strings.HasPrefix(base, ".env.") {
		switch base {
		case ".env.example", ".env.sample", ".env.template":
			return false
		}
		return true
	}
	return false
}

func normalizePath(name string) string {
	for strings.HasPrefix(name, "./") {
		name = strings.TrimPrefix(name, "./")
	}
	for strings.HasPrefix(name, "/") {
		name = strings.TrimPrefix(name, "/")
	}

	return name
}

func shouldSkip(name string) bool {
	base := filepath.Base(name)
	return base == ".DS_Store" || strings.HasPrefix(base, "._")
}
