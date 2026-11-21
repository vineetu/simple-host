package sites

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"unicode"

	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/tarball"
)

// Upload.Body caps the upload body. Default 100 MB; override with
// MAX_ARCHIVE_MB on an instance you run yourself (a hosted instance has other
// people's disk to protect, your own does not).
//
// It is a guard against one upload filling a disk, not a budget anyone spends:
// measured across the sites running on simple-host, the median is 25 KB and
// nine in ten are under a megabyte. Nothing should be derived from this number
// — what the disk is really holding is reported by /v1/admin/usage.
//
// Raising this alone is not enough to accept a bigger site: any reverse proxy
// in front needs its own body cap raised to match, or it rejects the request
// before Go ever sees it. Lowering it goes through Upload.Configure, which
// moves the extractor's uncompressed caps with it; setting Body on its own
// would bound compressed bytes and nothing else.
var Upload = UploadLimits{Body: 100 << 20}

func init() {
	if v := os.Getenv("MAX_ARCHIVE_MB"); v != "" {
		if mb, err := strconv.Atoi(v); err == nil && mb > 0 {
			Upload.Configure(int64(mb) << 20)
		}
	}
}

// UploadLimits keeps the wire-body allowance and archive extraction budgets together.
// Configure is called at startup, before requests are served.
type UploadLimits struct{ Body int64 }

// Configure applies the per-site cap, in bytes, to every place that enforces
// one: the upload body cap and the extractor's uncompressed caps.
//
// Both, not either. The upload cap alone bounds the compressed bytes, and a
// well-compressed archive is an order of magnitude smaller than what it expands
// to — the extractor is where a 2 MB upload becoming 200 MB on disk is stopped.
// Call once at startup, before serving.
func (l *UploadLimits) Configure(bytes int64) {
	if bytes <= 0 {
		return
	}
	l.Body = bytes
	tarball.SetSiteLimit(bytes)
}

// Snapshot restores both allowances after a temporary configuration, including
// the wider default extraction budget when Configure has never been called.
func (l *UploadLimits) Snapshot() func() {
	body := l.Body
	restore := tarball.SnapshotLimits()
	return func() { l.Body = body; restore() }
}

// StatedSiteMB is a per-site limit as it is reported: in MB, and never above
// what the extractor will take (500 MB), which is where a
// MAX_ARCHIVE_MB above it really stops.
func StatedSiteMB(limit int64) int {
	if mb := int(limit >> 20); mb < 500 {
		return mb
	}
	return 500
}

// StorageTips is the hint every too-large or storage-full refusal carries.
const StorageTips = "Keep zip files, installers and videos elsewhere and link to them. Remove files you no longer use. Delete old sites you don't need."

// TooLarge is the refusal of an upload over an account's per-site limit,
// naming the limit; detail is what exceeded it, when known.
func TooLarge(limit int64, detail string) *Error {
	msg := "request body too large"
	if detail != "" {
		msg = detail
	}
	return &Error{Code: "site_too_large", Message: fmt.Sprintf("%s: a site on this account may be at most %d MB", msg, StatedSiteMB(limit))}
}

// SiteLimitMB is an account's per-site limit as reported, from its handles
// (current first, then earlier ones): its MAX_ARCHIVE_MB_OVERRIDES entry, else
// the instance's cap.
func SiteLimitMB(l edition.Limits, handles ...string) int {
	if mb, ok := l.ArchiveOverrideMB(handles...); ok {
		return StatedSiteMB(int64(mb) << 20)
	}
	return StatedSiteMB(Upload.Body)
}

// LargestSiteLimit is the largest per-site cap any account has (MAX_ARCHIVE_MB
// or an override above it), for caps that sit in front of the account being
// known, like the connector's message size.
func LargestSiteLimit(l edition.Limits) int64 {
	max := Upload.Body
	for _, mb := range l.ArchiveOverrides() {
		if b := int64(mb) << 20; b > max {
			max = b
		}
	}
	return max
}

// JSONBodyLimit is a JSON deploy's body cap for a site limit: base64 for
// binary files (4/3 larger) and the JSON around it.
func JSONBodyLimit(limit int64) int64 { return limit*4/3 + (1 << 20) }

// PhotoCount is the most photos one upload takes; PhotoBodyLimit its body cap.
const PhotoCount = 30

func PhotoBodyLimit() int64 { return int64(PhotoCount*(PhotoMaxBytes()+4096) + 4096) }

// PhotoMaxBytes is the largest photo a site takes (PHOTO_MAX_BYTES), and
// PhotoMaxDimension its longest side in pixels (PHOTO_MAX_DIMENSION).
func PhotoMaxBytes() int     { return envInt("PHOTO_MAX_BYTES", 1000000) }
func PhotoMaxDimension() int { return envInt("PHOTO_MAX_DIMENSION", 2048) }

func envInt(key string, fallback int) int {
	if n, err := strconv.Atoi(os.Getenv(key)); err == nil && n > 0 {
		return n
	}
	return fallback
}

// MaxVersionAuthor bounds a version's author: a name or a short note.
const MaxVersionAuthor = 80

// VersionAuthor is who made a version, as kept on the row: control
// characters and surrounding spaces dropped, cut to MaxVersionAuthor runes.
func VersionAuthor(raw string) string {
	s := strings.TrimSpace(strings.Map(func(c rune) rune {
		if unicode.IsControl(c) {
			return -1
		}
		return c
	}, raw))
	if r := []rune(s); len(r) > MaxVersionAuthor {
		s = strings.TrimSpace(string(r[:MaxVersionAuthor]))
	}
	return s
}
