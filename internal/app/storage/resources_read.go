package storage

import (
	"context"

	repo "github.com/vsriram/simple-host/internal/repository/storage"
	rules "github.com/vsriram/simple-host/internal/rules/storage"
)

// ResourceReadInput identifies a site's resource to read.
type ResourceReadInput struct {
	SiteID, Name string
}

// LoadResource reads a resource and its SQLite table access rules.
func (s *Service) LoadResource(ctx context.Context, in ResourceReadInput) (rules.Resource, error) {
	return repo.LoadResource(ctx, s.db, in.SiteID, in.Name)
}

// ResourceListInput identifies the site whose resource records are needed.
type ResourceListInput struct {
	SiteID string
}

// ListResourceRecords reads resource records in name order with their table rules.
func (s *Service) ListResourceRecords(ctx context.Context, in ResourceListInput) ([]rules.Resource, error) {
	return repo.ListResources(ctx, s.db, in.SiteID)
}
