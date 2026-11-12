package sites

import (
	"context"
	"github.com/vsriram/simple-host/internal/model"
	repo "github.com/vsriram/simple-host/internal/repository/sites"
)

func (s *Service) ListSitemapSites(ctx context.Context, teamsOnly bool) ([]model.Site, error) {
	return repo.ListSitemapSites(ctx, s.database, teamsOnly)
}
