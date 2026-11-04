package sites

import (
	"context"
	"github.com/vsriram/simple-host/internal/model"
	repo "github.com/vsriram/simple-host/internal/repository/sites"
)

func (s *Service) GetSiteByID(ctx context.Context, id string) (model.Site, error) {
	return repo.GetSiteByID(ctx, s.database, id)
}

func (s *Service) GetDeletedSiteByUser(ctx context.Context, userID, name string) (repo.DeletedSite, error) {
	return repo.GetDeletedSiteByUser(ctx, s.database, userID, name)
}
