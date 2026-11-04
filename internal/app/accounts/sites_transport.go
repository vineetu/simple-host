package accounts

import (
	"context"

	repo "github.com/vsriram/simple-host/internal/repository/accounts"
)

func (s *Service) GetShowcaseBio(ctx context.Context, userID string) (string, error) {
	return repo.GetShowcaseBio(ctx, s.database, userID)
}

func (s *Service) SetShowcaseBio(ctx context.Context, userID, bio string) error {
	return repo.SetShowcaseBio(ctx, s.database, userID, bio)
}
