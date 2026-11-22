package sites

import (
	"context"

	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sites/repo"
	"github.com/vsriram/simple-host/host/sqlkit"
)

// SitemapFilter applies a product's exclusions in the given order. Nil keeps
// every site that meets the platform's eligibility checks.
type SitemapFilter func(ctx context.Context, q sqlkit.Querier, sites []repo.Site) ([]repo.Site, error)

func (s *Service) ListSitemapSites(ctx context.Context) ([]model.Site, error) {
	sites, err := repo.ListSitemapSites(ctx, s.DB)
	if err != nil {
		return nil, err
	}
	if s.Deps.Sitemap != nil {
		// Repository rows are closed before the product's second read. A
		// concurrent take-down has the same visibility window as the cache.
		return s.Deps.Sitemap(ctx, s.DB, sites)
	}
	return sites, nil
}
