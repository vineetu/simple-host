package data

import (
	"context"

	"github.com/vsriram/simple-host/host/data/repo"
	model "github.com/vsriram/simple-host/host/model"
)

// LoadResource reads a resource and its SQLite table access rules.
func (s *Service) LoadResource(ctx context.Context, siteID, name string) (model.Resource, error) {
	return repo.LoadResource(ctx, s.db, siteID, name)
}

// ListResourceRecords reads resource records in name order with their table rules.
func (s *Service) ListResourceRecords(ctx context.Context, siteID string) ([]model.Resource, error) {
	return repo.ListResources(ctx, s.db, siteID)
}
