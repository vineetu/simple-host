package tarball

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
)

// maxTotalUncompressedSize caps the sum of all extracted file contents, and
// maxFileSize caps any single one. These are the ceiling the package will ever
// accept; SetSiteLimit lowers them to whatever the instance sized itself for.
//
// They are variables rather than constants because a hackathon box with a 25 GB
// disk cannot honour a 500 MB site, and a cap the server advertises but does not
// enforce is worse than no cap at all.
var (
	maxTotalUncompressedSize int64 = ceilingBytes
	maxFileSize              int64 = 100 * 1024 * 1024
	// maxEntryCount caps the number of files in an archive (defends against
	// many-tiny-files inode exhaustion, which the byte caps do not catch).
	maxEntryCount = entryCeiling
)

const (
	// ceilingBytes is the largest site this package will ever accept, whatever
	// an instance asks for. The rest of the pipeline was sized against it.
	ceilingBytes int64 = 500 * 1024 * 1024
	// blockSize is the allocation unit assumed when deriving a file-count cap
	// from a byte budget.
	blockSize = 4096
	// maxEntriesCeiling is the most files an archive may ever hold: the
	// pipeline (in-memory entry map, inodes per version) was sized against it.
	maxEntriesCeiling = 50_000
)

// entryCeiling is the file-count cap (MAX_FILES_PER_SITE, default 50,000);
// siteBudget is the byte budget SetSiteLimit was last given (0: never), from
// which a smaller count may be derived.
var (
	entryCeiling       = maxEntriesCeiling
	siteBudget   int64 = 0
)

// SetMaxEntries sets the most files one archive may hold (MAX_FILES_PER_SITE).
// When a per-site byte budget is in force the smaller of the two applies. Call
// once at startup, before serving. It only lowers: above maxEntriesCeiling is
// refused, like SetSiteLimit above ceilingBytes.
func SetMaxEntries(n int) {
	if n < 1 || n > maxEntriesCeiling {
		return
	}
	entryCeiling = n
	deriveEntryCount()
}

func deriveEntryCount() {
	maxEntryCount = entriesFor(siteBudget)
}

// entriesFor is the file-count cap for a byte budget (0: none in force).
func entriesFor(budget int64) int {
	n := entryCeiling
	if budget <= 0 {
		return n
	}
	// A filesystem allocates at least one block per file, so 49,000 one-byte
	// files occupy ~190 MB on a 4K filesystem while measuring 49 KB — the byte
	// cap alone does not bound what a site costs on disk. One block per entry
	// makes the budget hold.
	if b := int(budget / blockSize); b < n {
		n = b
	}
	if n < 1 {
		n = 1
	}
	return n
}

// caps is one set of extraction caps: the instance's (current) or one
// account's (capsFor).
type caps struct {
	total, file int64
	entries     int
}

func current() caps { return caps{maxTotalUncompressedSize, maxFileSize, maxEntryCount} }

// capsFor is the caps for a per-site budget in bytes, derived exactly as
// SetSiteLimit derives the instance's: the budget bounds the total and any one
// file, and the file count follows it. A budget of 0 or less is the instance's
// caps; one above the ceiling is held to the ceiling.
func capsFor(budget int64) caps {
	if budget <= 0 {
		return current()
	}
	if budget > ceilingBytes {
		budget = ceilingBytes
	}
	return caps{budget, budget, entriesFor(budget)}
}

// ErrTooLarge marks an upload refused for its size or file count, so the
// caller can answer 413 with the reason rather than "invalid archive".
var ErrTooLarge = errors.New("over the site size limit")

type sizeError string

func (e sizeError) Error() string        { return string(e) }
func (e sizeError) Is(target error) bool { return target == ErrTooLarge }

func tooLarge(format string, args ...any) error { return sizeError(fmt.Sprintf(format, args...)) }

// SetSiteLimit lowers the extraction caps to a per-site budget in bytes. Call
// once at startup, before serving. Raising them above the built-in ceiling is
// refused: the ceiling is what the rest of the pipeline was sized against.
func SetSiteLimit(bytes int64) {
	if bytes <= 0 || bytes > ceilingBytes {
		return
	}
	// Both caps are assigned outright rather than only lowered. Lowering-only
	// leaves the per-file cap stranded at an earlier, smaller value when the
	// limit is set twice — MAX_ARCHIVE_MB=1 at init followed by a stored 10 MB
	// at boot would advertise 10 MB while still refusing any file over 1 MB.
	maxTotalUncompressedSize = bytes
	maxFileSize = bytes

	// Entry count follows the byte budget.
	siteBudget = bytes
	deriveEntryCount()
}

const (
	// maxPathDepth / maxPathLen bound pathological directory nesting and names.
	maxPathDepth = 32
	maxPathLen   = 1024
)

// Extract unpacks an archive under the instance's caps.
func Extract(r io.Reader, filename string) (map[string][]byte, error) {
	return extract(r, filename, current())
}

// ExtractWithLimit unpacks an archive under a per-site budget in bytes (one
// account's size limit) instead of the instance's: the total, any one file
// and the file count are derived from it as SetSiteLimit derives them.
func ExtractWithLimit(r io.Reader, filename string, budget int64) (map[string][]byte, error) {
	return extract(r, filename, capsFor(budget))
}

func extract(r io.Reader, filename string, c caps) (map[string][]byte, error) {
	switch {
	case strings.HasSuffix(strings.ToLower(filename), ".tar.gz"):
		return extractTarGz(r, c)
	case strings.HasSuffix(strings.ToLower(filename), ".zip"):
		return extractZip(r, c)
	default:
		return nil, fmt.Errorf("unsupported archive format: %s", filepath.Ext(filename))
	}
}

func extractTarGz(r io.Reader, c caps) (map[string][]byte, error) {
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
		if count > c.entries {
			return nil, tooLarge("archive exceeds %d entry limit", c.entries)
		}

		content, err := readCapped(tarReader, totalSize, c)
		if err != nil {
			return nil, fmt.Errorf("read tar entry %q: %w", clean, err)
		}
		totalSize += int64(len(content))
		files[clean] = content
	}
}

func extractZip(r io.Reader, c caps) (map[string][]byte, error) {
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
		if count > c.entries {
			return nil, tooLarge("archive exceeds %d entry limit", c.entries)
		}

		reader, err := file.Open()
		if err != nil {
			return nil, fmt.Errorf("open zip entry %q: %w", clean, err)
		}
		// Bound by bytes ACTUALLY read, never the zip's self-declared
		// UncompressedSize64 (attacker-controlled — a decompression-bomb vector).
		content, readErr := readCapped(reader, totalSize, c)
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
func readCapped(r io.Reader, alreadyRead int64, c caps) ([]byte, error) {
	limit := c.file
	if remaining := c.total - alreadyRead; remaining < limit {
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
		return nil, tooLarge("archive exceeds size limit (max %dMB per file, %dMB total)",
			c.file/(1024*1024), c.total/(1024*1024))
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

// SiteLimitIs reports whether both extraction caps sit at bytes. Exported for
// the handler's test: the wiring between the upload cap and the extractor cap
// is the part that is easy to half-do and impossible to see from outside.
func SiteLimitIs(bytes int64) bool {
	return maxTotalUncompressedSize == bytes && maxFileSize == bytes
}

// MaxEntries reports the file-count cap currently in force.
func MaxEntries() int { return maxEntryCount }

// SnapshotLimits captures the current caps and returns a function that puts
// them back. The caps are package globals, so a test in another package that
// exercises the wiring into SetSiteLimit would otherwise leave them moved for
// every test that runs after it — an order-dependent failure, which is the
// worst kind to debug.
func SnapshotLimits() func() {
	total, file, entries, ceiling, budget := maxTotalUncompressedSize, maxFileSize, maxEntryCount, entryCeiling, siteBudget
	return func() {
		maxTotalUncompressedSize, maxFileSize, maxEntryCount, entryCeiling, siteBudget = total, file, entries, ceiling, budget
	}
}
