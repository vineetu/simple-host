package analytics

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Two rotation schemes put the previous log next to the live one:
//
//   - logrotate (simple-host.app, deploy/prod/logrotate-analytics.conf):
//     analytics.log.1, analytics.log.2.gz, ... with delaycompress, so .1 is
//     plain and everything older is gzip.
//   - Caddy (self-host and event boxes, deploy/compose/Caddyfile):
//     access-<timestamp>-<reason>.log, gzipped to .log.gz straight after the
//     roll. The timestamp (2006-01-02T15-04-05.000) sorts by name.

// numberedArchives returns logrotate's archives of logPath, oldest first
// (highest N first), plain or .gz.
func numberedArchives(logPath string) []string {
	dir, base := filepath.Split(logPath)
	entries, err := os.ReadDir(filepath.Clean(dir + "."))
	if err != nil {
		return nil
	}
	type arch struct {
		n    int
		path string
	}
	var found []arch
	for _, e := range entries {
		rest, ok := strings.CutPrefix(e.Name(), base+".")
		if !ok || e.IsDir() {
			continue
		}
		rest = strings.TrimSuffix(rest, ".gz")
		n, err := strconv.Atoi(rest)
		if err != nil || n < 1 {
			continue
		}
		found = append(found, arch{n, filepath.Join(dir, e.Name())})
	}
	sort.Slice(found, func(a, b int) bool { return found[a].n > found[b].n })
	out := make([]string, 0, len(found))
	for _, f := range found {
		out = append(out, f.path)
	}
	return out
}

// caddyRolls returns Caddy's rolled files of logPath, oldest first. When a
// roll exists both plain and gzipped (Caddy is mid-compression), only the
// plain one is returned: the .gz may still be being written.
func caddyRolls(logPath string) []string {
	dir, base := filepath.Split(logPath)
	ext := filepath.Ext(base)
	if ext == "" {
		return nil
	}
	prefix := strings.TrimSuffix(base, ext) + "-"
	entries, err := os.ReadDir(filepath.Clean(dir + "."))
	if err != nil {
		return nil
	}
	byStem := map[string]string{} // name without .gz -> chosen file name
	for _, e := range entries {
		name := e.Name()
		stem := strings.TrimSuffix(name, ".gz")
		rest, ok := strings.CutPrefix(stem, prefix)
		if e.IsDir() || !ok || !strings.HasSuffix(stem, ext) || rest == "" || rest[0] < '0' || rest[0] > '9' {
			continue
		}
		if _, seen := byStem[stem]; !seen || name == stem {
			byStem[stem] = name
		}
	}
	stems := make([]string, 0, len(byStem))
	for s := range byStem {
		stems = append(stems, s)
	}
	sort.Strings(stems)
	out := make([]string, 0, len(stems))
	for _, s := range stems {
		out = append(out, filepath.Join(dir, byStem[s]))
	}
	return out
}

// ReplayFiles lists every file an analytics rebuild reads, oldest first: the
// rotated archives of logPath (either scheme), then logPath itself when it
// exists.
func ReplayFiles(logPath string) []string {
	files := append(numberedArchives(logPath), caddyRolls(logPath)...)
	if _, err := os.Stat(logPath); err == nil {
		files = append(files, logPath)
	}
	return files
}
