package data

import (
	"context"
	"log"
	"os"
	"path/filepath"

	model "github.com/vsriram/simple-host/host/model"
	runtime "github.com/vsriram/simple-host/host/storage"
)

// prepareResourceTables readies the SQLite database before its rules are
// saved: the author and approval columns the rules need, and, on a database
// pages wrote with SQL before presets, an empty database or cleared authors.
func (s *Service) prepareResourceTables(ctx context.Context, call Call, resource model.Resource, requireEmpty, clearIDs bool, own, hold func(string) bool) (map[string]bool, error) {
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
		existing, err = runtime.PrepareRulesDatabase(ctx, path, s.deps.SQLiteMaxMemory, SiteLimitBytes(s.deps.SiteLimit, s.deps.CombinedPool), s.BudgetUsed(usage), fi.Size(), requireEmpty, clearIDs, own, hold, func(err error) {
			log.Printf("storage: site %s %s: checkpoint after rules: %v", call.SiteID, resource.Name, err)
		}, s.deps.MarkChanged, func(tables map[string]bool) {
			existing = tables
			a.Committed()
		})
		return err
	})
	return existing, err
}
