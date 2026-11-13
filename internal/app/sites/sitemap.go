package sites

import (
	"context"

	"github.com/vsriram/simple-host/internal/model"
	repo "github.com/vsriram/simple-host/internal/repository/sites"
	"github.com/vsriram/simple-host/internal/repository/sqlkit"
)

// SitemapFilter applies a product's exclusions in the given order. Nil keeps
// every site that meets the platform's eligibility checks.
type SitemapFilter func(ctx context.Context, q sqlkit.Querier, sites []repo.Site) ([]repo.Site, error)

func (s *Service) ListSitemapSites(ctx context.Context) ([]model.Site, error) {
	sites, err := repo.ListSitemapSites(ctx, s.database)
	if err != nil {
		return nil, err
	}
	if s.deps.Sitemap != nil {
		// Repository rows are closed before the product's second read. A
		// concurrent take-down has the same visibility window as the cache.
		return s.deps.Sitemap(ctx, s.database, sites)
	}
	return sites, nil
}
