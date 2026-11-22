package access

import (
	"context"
	"time"

	"github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/model"
)

func (s *Service) ReplaceSSOPass(ctx context.Context, hash, keyHash, oldHash string, ttl, idle, keyIdle time.Duration) error {
	tx, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	err = repo.ReplaceSSOPass(ctx, tx, hash, keyHash, oldHash, ttl, idle, keyIdle)
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Service) RevokeVisitorSSO(ctx context.Context, sess model.VisitorSession, token, returnTo string) error {
	tx, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	err = repo.RevokeVisitorSSO(ctx, tx, sess, token, returnTo)
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Service) CompleteSSOSignout(ctx context.Context, token, cookieHash, formHash string) (string, error) {
	tx, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	back, err := repo.CompleteSSOSignout(ctx, tx, token, cookieHash, formHash)
	if err != nil {
		return "", err
	}
	return back, tx.Commit()
}

type SSOHandoff struct {
	Hop     model.SSOHop
	Once    string
	Status  int
	Failure string
}
