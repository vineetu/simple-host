package storage

import (
	"context"
	"github.com/vsriram/simple-host/internal/repository/storage"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func LimitBytes(raw string, fallback, max int64) int64 {
	if v, err := strconv.ParseInt(raw, 10, 64); err == nil && v > 0 && v <= max {
		return v
	}
	return fallback
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
	u := Usage{FileObjects: map[string]int{}}
	if err := storage.KVUsage(ctx, s.db, siteID, &u.KV); err != nil {
		return u, err
	}
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, e error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if os.IsNotExist(e) {
			return nil
		}
		if e != nil {
			return e
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") || !d.Type().IsRegular() {
			return nil
		}
		rel, e := filepath.Rel(root, p)
		if e != nil {
			return e
		}
		fi, e := d.Info()
		if e != nil {
			return e
		}
		if strings.HasPrefix(rel, "sqlite"+string(filepath.Separator)) && strings.HasSuffix(rel, ".sqlite") {
			u.SQLite += fi.Size()
		}
		if strings.HasPrefix(rel, "files"+string(filepath.Separator)) {
			u.Files += fi.Size()
			parts := strings.Split(rel, string(filepath.Separator))
			if len(parts) > 2 {
				u.FileObjects[parts[1]]++
			}
		}
		return nil
	})
	return u, err
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
	u, err := s.MeasureSiteStorage(ctx, call.SiteID, call.RuntimeDir)
	if err != nil {
		return nil, err
	}
	limit := SiteLimitBytes(s.deps.SiteLimit, s.deps.CombinedPool)
	used := s.BudgetUsed(u)
	remaining := max(int64(0), limit-used)
	return map[string]any{"used_bytes": used, "limit_bytes": limit, "remaining_bytes": remaining, "files_used_bytes": u.Files, "files_limit_bytes": s.FilesLimitBytes(), "files_remaining_bytes": s.FilesRemaining(u), "breakdown": map[string]int64{"kv_bytes": u.KV, "sqlite_bytes": u.SQLite, "files_bytes": u.Files}}, nil
}
