package data

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	storage "github.com/vsriram/simple-host/host/data/repo"
	runtime "github.com/vsriram/simple-host/host/storage"
)

func LimitBytes(raw string, fallback, max int64) int64 {
	if v, err := strconv.ParseInt(raw, 10, 64); err == nil && v > 0 && v <= max {
		return v
	}
	return fallback
}

// FileMaxBytes is the largest stored file (SITE_STORAGE_FILE_MAX_BYTES).
func FileMaxBytes() int64 {
	return LimitBytes(os.Getenv("SITE_STORAGE_FILE_MAX_BYTES"), 1000000, 10<<30)
}

// Simple Host defaults KV plus SQLite to 10 MB per site; Simple Hack keeps
// its 1,000,000-byte pool, which also covers files.
func SiteLimitBytes(raw string, combinedPool bool) int64 {
	fallback := int64(10000000)
	if combinedPool {
		fallback = 1000000
	}
	return LimitBytes(raw, fallback, 10<<30)
}
func ResultLimitBytes(raw string) int {
	return int(LimitBytes(raw, 1000000, 64<<20))
}

// Usage records the durable bytes used by a site.
type Usage struct {
	KV, SQLite, Files int64
	FileObjects       map[string]int
}

// Data returns KV and SQLite bytes.
func (u Usage) Data() int64 { return u.KV + u.SQLite }

// Total returns all durable bytes.
func (u Usage) Total() int64 { return u.KV + u.SQLite + u.Files }

// The owner report and all three write paths use this same durable-byte
// definition: normalized JSONB value text, SQLite main files after checkpoint,
// and raw object bytes. WAL, retained deploys and legacy data are separate.
func (s *Service) MeasureSiteStorage(ctx context.Context, siteID, root string) (Usage, error) {
	names, err := storage.KVNamespaces(ctx, s.db, siteID)
	if err != nil {
		return Usage{}, err
	}
	if len(names) == 0 {
		return s.measureSiteStorageAdmitted(ctx, siteID, root)
	}
	var u Usage
	err = s.deps.SiteSlots.Admit(ctx, siteID, func(ctx context.Context, _ *Admission) error {
		var err error
		u, err = s.measureSiteStorageAdmitted(ctx, siteID, root)
		return err
	})
	return u, err
}

// measureSiteStorageAdmitted never acquires a slot: its callers already own one
// whenever the site has KV namespaces.
func (s *Service) measureSiteStorageAdmitted(ctx context.Context, siteID, root string) (Usage, error) {
	u := Usage{FileObjects: map[string]int{}}
	var err error
	if u.KV, err = s.measureKVBytes(ctx, siteID, root); err != nil {
		return u, err
	}
	err = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		hidden := err == nil && strings.HasPrefix(d.Name(), ".")
		switch {
		case ctx.Err() != nil:
			return ctx.Err()
		case os.IsNotExist(err):
			return nil
		case err != nil:
			return err
		case hidden && d.IsDir():
			return filepath.SkipDir
		case hidden || !d.Type().IsRegular():
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		u.countFile(rel, fi.Size())
		return nil
	})
	return u, err
}

// measureKVBytes sums the logical bytes of the site's KV namespaces; a
// missing namespace file counts as zero.
func (s *Service) measureKVBytes(ctx context.Context, siteID, root string) (int64, error) {
	names, err := storage.KVNamespaces(ctx, s.db, siteID)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, name := range names {
		n, err := kvNamespaceBytes(ctx, runtime.RuntimeKVPath(root, name))
		if os.IsNotExist(err) {
			log.Printf("storage: missing KV file for site %s namespace %s; usage counted as zero", siteID, name)
			continue
		}
		if err != nil {
			return total, kvFileError(err)
		}
		total += n
	}
	return total, nil
}

// kvNamespaceBytes reads one namespace file's logical bytes.
func kvNamespaceBytes(ctx context.Context, path string) (int64, error) {
	conn, done, err := runtime.OpenRuntimeKV(ctx, path, false, false)
	if err != nil {
		return 0, err
	}
	defer closeSQLite(done)
	meta, err := runtime.RuntimeKVMeta(conn)
	return meta.LogicalBytes, err
}

// countFile charges a stored file at rel, relative to the site's runtime
// root: SQLite main files, and file-bucket objects per bucket.
func (u *Usage) countFile(rel string, size int64) {
	parts := strings.Split(rel, string(filepath.Separator))
	switch {
	case len(parts) > 1 && parts[0] == "sqlite" && strings.HasSuffix(rel, ".sqlite"):
		u.SQLite += size
	case len(parts) > 1 && parts[0] == "files":
		u.Files += size
		if len(parts) > 2 {
			u.FileObjects[parts[1]]++
		}
	}
}

// BudgetUsed returns the bytes charged to the site pool.
func (s *Service) BudgetUsed(u Usage) int64 {
	if s.deps.CombinedPool {
		return u.Total()
	}
	return u.Data()
}

// FilesLimitBytes returns the file pool limit.
func (s *Service) FilesLimitBytes() int64 {
	if s.deps.CombinedPool {
		return SiteLimitBytes(s.deps.SiteLimit, true)
	}
	return LimitBytes(s.deps.FilesLimit, 10000000, 10<<30)
}

// FilesRemaining returns the available file bytes.
func (s *Service) FilesRemaining(u Usage) int64 {
	used := u.Files
	if s.deps.CombinedPool {
		used = u.Total()
	}
	return max(int64(0), s.FilesLimitBytes()-used)
}

// GetStorageUsage prepares the owner's storage usage report.
func (s *Service) GetStorageUsage(ctx context.Context, call Call) (map[string]any, error) {
	if !call.Owner {
		return nil, &Error{Code: "forbidden", Message: "owner credential required"}
	}
	u, err := s.MeasureSiteStorage(ctx, call.SiteID, call.RuntimeDir)
	if err != nil {
		return nil, err
	}
	limit := SiteLimitBytes(s.deps.SiteLimit, s.deps.CombinedPool)
	used := s.BudgetUsed(u)
	remaining := max(int64(0), limit-used)
	return map[string]any{"used_bytes": used, "limit_bytes": limit, "remaining_bytes": remaining, "files_used_bytes": u.Files, "files_limit_bytes": s.FilesLimitBytes(), "files_remaining_bytes": s.FilesRemaining(u), "breakdown": map[string]int64{"kv_bytes": u.KV, "sqlite_bytes": u.SQLite, "files_bytes": u.Files}}, nil
}
