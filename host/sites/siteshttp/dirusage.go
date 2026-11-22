package siteshttp

import (
	"context"
	"io/fs"
	"path/filepath"
	"sync"
	"time"
)

// DirUsage returns the bytes of regular files under dir, measured at most
// every ten minutes (a walk of the hackathon platform's team sites, for
// HACK_INSTANCE_BUDGET_GB).
func DirUsage(dir string) func(ctx context.Context) (int64, error) {
	var (
		mu     sync.Mutex
		last   time.Time
		cached int64
	)
	return func(ctx context.Context) (int64, error) {
		mu.Lock()
		defer mu.Unlock()
		if !last.IsZero() && time.Since(last) < 10*time.Minute {
			return cached, nil
		}
		var total int64
		err := filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if d.Type().IsRegular() {
				if info, ierr := d.Info(); ierr == nil {
					total += info.Size()
				}
			}
			return nil
		})
		if err != nil {
			return 0, err
		}
		cached, last = total, time.Now()
		return total, nil
	}
}
