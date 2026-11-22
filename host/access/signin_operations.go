package access

import (
	"context"

	"github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/model"
)

func (s *Service) AddSignInKey(ctx context.Context, userID, key, name string) error {
	tx, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	suspended, err := repo.LockSignInAccount(ctx, tx, userID)
	if err != nil {
		return err
	}
	if suspended {
		return model.ErrAccountSuspended
	}
	if err := repo.AddAPIKey(ctx, tx, userID, key, name); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Service) GetSSOHop(ctx context.Context, state string, consume bool) (model.SSOHop, error) {
	return repo.GetSSOHop(ctx, s.database, state, consume)
}
func (s *Service) SignInAlertsOn(ctx context.Context, userID string) (bool, error) {
	return repo.SignInAlertsOn(ctx, s.database, userID)
}
