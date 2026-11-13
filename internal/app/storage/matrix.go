package storage

import (
	"context"
	"log"
	"os"
	"path/filepath"

	rules "github.com/vsriram/simple-host/internal/rules/storage"
	runtime "github.com/vsriram/simple-host/internal/storage"
)

// prepareResourceMatrix readies the SQLite database before saving its preset.
func (s *Service) prepareResourceMatrix(ctx context.Context, call Call, resource rules.Resource, requireEmpty, clearIDs bool) (map[string]bool, error) {
	existing := map[string]bool{}
	path := filepath.Join(call.RuntimeDir, "sqlite", resource.Name+".sqlite")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return existing, nil
	} else if err != nil {
		return existing, err
	}
	usage, err := s.MeasureSiteStorage(ctx, call.SiteID, call.RuntimeDir)
	if err != nil {
		return existing, err
	}
	fi, err := os.Stat(path)
	if err != nil {
		return existing, err
	}
	err = s.deps.SiteSlots.Admit(ctx, call.SiteID, func(ctx context.Context, a *Admission) error {
		var err error
		existing, err = runtime.PrepareMatrixDatabase(ctx, path, s.deps.SQLiteMaxMemory, SiteLimitBytes(s.deps.SiteLimit, s.deps.CombinedPool), s.BudgetUsed(usage), fi.Size(), requireEmpty, clearIDs, resource.OwnTables(), resource.Holds, func(err error) {
			log.Printf("storage: site %s %s: checkpoint after preset: %v", call.SiteID, resource.Name, err)
		}, s.deps.MarkChanged, func(tables map[string]bool) {
			existing = tables
			a.Committed()
		})
		return err
	})
	return existing, err
}
