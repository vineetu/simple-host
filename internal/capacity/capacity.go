// Package capacity reports what an instance is actually using, and how much
// room is left.
//
// It used to project how many people would fit, by assuming every participant
// filled a per-site cap. Measured against 89 real sites, that model was wrong
// by more than three orders of magnitude: the median site is 25 KB, 89% are
// under 1 MB, and the whole set occupies 450 MB. A projection built on the cap
// rather than on real sizes produced numbers like "145 people on a 200 GB disk",
// and then refused to create accounts past them.
//
// So there is no projection here and no limit derived from one. There is what
// is used, what is free, and enough warning to act before it matters.
package capacity

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"syscall"
)

// Thresholds at which an instance should say something. Disk that fills during
// an event is unrecoverable in the moment, so the first warning comes early
// enough to do something about it.
const (
	FillingAt = 75.0
	FullAt    = 90.0
)

// Usage is what the instance is using and what it has left.
type Usage struct {
	// The filesystem holding site data. Everything on it counts, including
	// Postgres, logs and the OS — that is what runs out.
	DiskBytes     int64   `json:"disk_bytes"`
	DiskFreeBytes int64   `json:"disk_free_bytes"`
	DiskUsedBytes int64   `json:"disk_used_bytes"`
	DiskUsedPct   float64 `json:"disk_used_pct"`

	// Site files specifically: every version of every site, plus the live copy.
	SiteBytes int64 `json:"site_bytes"`
	Sites     int   `json:"sites"`

	// Biggest first, so an admin looking at a full disk sees the cause.
	Largest []SiteUsage `json:"largest"`

	Status  string `json:"status"`
	Message string `json:"message"`
}

// SiteUsage is one site's footprint on disk, versions included.
type SiteUsage struct {
	UserID string `json:"user_id"`
	Name   string `json:"name"`
	Bytes  int64  `json:"bytes"`
}

// Disk reports the size and free space of the filesystem holding path.
func Disk(path string) (total, available int64, err error) {
	var stat syscall.Statfs_t
	if err = syscall.Statfs(path, &stat); err != nil {
		return 0, 0, fmt.Errorf("read free space on %s: %w", path, err)
	}
	// Bavail, not Bfree: Bfree includes blocks reserved for root, which a
	// service running as a normal user cannot write to and must not count.
	return int64(stat.Blocks) * int64(stat.Bsize), int64(stat.Bavail) * int64(stat.Bsize), nil
}

// Measure walks the site tree and reads the filesystem. The walk is the slow
// part — it is why callers cache this rather than running it per request.
//
// topN limits the biggest-sites list; pass 0 for none.
func Measure(dataDir string, topN int) (Usage, error) {
	total, available, err := Disk(dataDir)
	if err != nil {
		return Usage{}, err
	}
	usage := Usage{
		DiskBytes:     total,
		DiskFreeBytes: available,
		DiskUsedBytes: total - available,
	}
	if total > 0 {
		usage.DiskUsedPct = float64(total-available) * 100 / float64(total)
	}

	// <dataDir>/by-id/<userID>/<siteName>/{v1,v2,…,current}
	root := filepath.Join(dataDir, "by-id")
	sites := map[string]*SiteUsage{}
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil //nolint:nilerr // an unreadable corner is not worth failing the whole report
		}
		// Symlinks report the length of their target path, not the target.
		if !d.Type().IsRegular() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		// <userID>/<siteName>/<version>/<file…>: anything shallower is not a
		// site's file and does not belong to anybody.
		segments := strings.Split(filepath.ToSlash(relative), "/")
		if len(segments) < 3 {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		key := segments[0] + "/" + segments[1]
		site := sites[key]
		if site == nil {
			site = &SiteUsage{UserID: segments[0], Name: segments[1]}
			sites[key] = site
		}
		site.Bytes += info.Size()
		usage.SiteBytes += info.Size()
		return nil
	})

	usage.Sites = len(sites)
	usage.Largest = topSites(sites, topN)
	usage.Status, usage.Message = describe(usage)
	return usage, nil
}

func topSites(sites map[string]*SiteUsage, topN int) []SiteUsage {
	if topN <= 0 {
		return nil
	}
	all := make([]SiteUsage, 0, len(sites))
	for _, s := range sites {
		all = append(all, *s)
	}
	// Selection over a sort: topN is small and this avoids ordering thousands
	// of sites to show five of them.
	if topN > len(all) {
		topN = len(all)
	}
	for i := 0; i < topN; i++ {
		max := i
		for j := i + 1; j < len(all); j++ {
			if all[j].Bytes > all[max].Bytes {
				max = j
			}
		}
		all[i], all[max] = all[max], all[i]
	}
	return all[:topN]
}

func describe(u Usage) (status, message string) {
	switch {
	case u.DiskUsedPct >= FullAt:
		return "full", fmt.Sprintf("This server is %.0f%% full, with %s left. Publishing will start failing. Free space or move to a bigger disk now.",
			u.DiskUsedPct, Human(u.DiskFreeBytes))
	case u.DiskUsedPct >= FillingAt:
		return "filling", fmt.Sprintf("This server is %.0f%% full, with %s left. Worth watching, and worth acting on before an event rather than during one.",
			u.DiskUsedPct, Human(u.DiskFreeBytes))
	default:
		return "ok", fmt.Sprintf("%s of %s used, %s free. Sites account for %s of it.",
			Human(u.DiskUsedBytes), Human(u.DiskBytes), Human(u.DiskFreeBytes), Human(u.SiteBytes))
	}
}

// Human formats a byte count the way an admin screen should show it.
func Human(bytes int64) string {
	switch {
	case bytes >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(bytes)/float64(1<<30))
	case bytes >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(bytes)/float64(1<<20))
	case bytes >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(bytes)/float64(1<<10))
	default:
		return fmt.Sprintf("%d bytes", bytes)
	}
}
