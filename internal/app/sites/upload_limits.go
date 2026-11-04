package sites

import "github.com/vsriram/simple-host/internal/tarball"

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
