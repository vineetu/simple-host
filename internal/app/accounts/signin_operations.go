package accounts

import (
	"context"
	"errors"
	"log"

	"github.com/vsriram/simple-host/internal/model"
	repo "github.com/vsriram/simple-host/internal/repository/accounts"
	sites "github.com/vsriram/simple-host/internal/repository/sites"
)

var ErrDisconnectAfterRotation = errors.New("key rotated, but connected apps could not be disconnected; try again")

// RotateKey commits replacement and visitor-session revocation before disconnecting apps.
func (s *Service) RotateKey(ctx context.Context, userID, currentKeyHash, newKey string) error {
	tx, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := repo.LockKeyIssuer(ctx, tx, userID, currentKeyHash); err != nil {
		return err
	}
	if err := repo.DeleteAllKeys(ctx, tx, userID); err != nil {
		return err
	}
	if err := repo.EndVisitorSessions(ctx, tx, userID); err != nil {
		return err
	}
	if err := repo.AddAPIKey(ctx, tx, userID, newKey, model.KeyNameRotated); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if err := repo.DeleteOAuthGrantsForUser(ctx, s.database, userID); err != nil {
		log.Printf("rotate key: disconnect apps user_id=%s: %v", userID, err)
		return ErrDisconnectAfterRotation
	}
	return nil
}
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
func (s *Service) HomeSite(ctx context.Context, userID string) (model.Site, bool, error) {
	return sites.HomeSite(ctx, s.database, userID)
}
