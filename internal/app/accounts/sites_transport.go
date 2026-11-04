package accounts

import (
	"context"

	"github.com/vsriram/simple-host/internal/model"
	repo "github.com/vsriram/simple-host/internal/repository/accounts"
)

func (s *Service) GetUserByHandle(ctx context.Context, handle string) (model.User, error) {
	return repo.GetUserByHandle(ctx, s.database, handle)
}

func (s *Service) GetUserByHandleOrAlias(ctx context.Context, handle string) (model.User, error) {
	return repo.GetUserByHandleOrAlias(ctx, s.database, handle)
}

func (s *Service) ResolveHandleAlias(ctx context.Context, old string) (string, error) {
	return repo.ResolveHandleAlias(ctx, s.database, old)
}

func (s *Service) GetShowcaseBio(ctx context.Context, userID string) (string, error) {
	return repo.GetShowcaseBio(ctx, s.database, userID)
}

func (s *Service) SetShowcaseBio(ctx context.Context, userID, bio string) error {
	return repo.SetShowcaseBio(ctx, s.database, userID, bio)
}
