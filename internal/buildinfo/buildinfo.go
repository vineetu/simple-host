// Package buildinfo names the release, commit, and time this binary was built.
//
// They are set at build time with -ldflags -X, e.g.
//
//	-X github.com/vsriram/simple-host/internal/buildinfo.Version=$(git describe --tags --always --dirty)
//	-X github.com/vsriram/simple-host/internal/buildinfo.Commit=$(git rev-parse --short HEAD)
//	-X github.com/vsriram/simple-host/internal/buildinfo.BuildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)
//
// (the Dockerfile and the release workflow pass them). A plain `go build` from a
// git checkout still gets the commit from Go's own VCS stamp; anything else
// reports "dev" and "unknown" rather than guessing.
package buildinfo

import "runtime/debug"

var (
	// Version is the release tag, or "dev" for a build that was not given one.
	Version = "dev"
	// Commit is the short commit hash, or "unknown".
	Commit = "unknown"
	// BuildTime is an RFC 3339 UTC timestamp, set with -ldflags -X by release
	// builds. Plain go builds fall back to their embedded source revision time.
	// Builds without either stamp leave it empty rather than inventing a date.
	BuildTime = ""
)

func init() {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	var rev string
	var dirty bool
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		case "vcs.time":
			if BuildTime == "" {
				BuildTime = s.Value
			}
		}
	}
	if Commit != "unknown" && Commit != "" {
		return
	}
	Commit = "unknown"
	if len(rev) > 7 {
		rev = rev[:7]
	}
	if rev != "" {
		Commit = rev
		if dirty {
			Commit += "-dirty"
		}
	}
}

// String is the one-line form used in logs and by `simple-host version`.
func String() string {
	return "simple-host " + Version + " (commit " + Commit + ")"
}
