package web

// KeepVersions reports the retention setting: how many deploys of a site are
// kept, or 0 for all of them.
func KeepVersions() int { return DefaultKeepVersions }
