package sites

import "github.com/vsriram/simple-host/internal/tarball"

func SetArchiveSiteLimit(bytes int64) { tarball.SetSiteLimit(bytes) }
func SnapshotArchiveLimits() func()   { return tarball.SnapshotLimits() }
