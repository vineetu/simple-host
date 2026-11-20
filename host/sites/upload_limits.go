package sites

import (
	"os"
	"strconv"
	"strings"
	"unicode"

	"github.com/vsriram/simple-host/host/tarball"
)

// UploadLimits keeps the wire-body allowance and archive extraction budgets together.
// Configure is called at startup, before requests are served.
type UploadLimits struct{ Body int64 }

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
